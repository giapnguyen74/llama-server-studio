package profiles

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"llama-server-studio/internal/storage"
)

// BuiltCommand represents a fully resolved command-line ready to execute.
type BuiltCommand struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Env        []string          `json:"env"`
	WorkDir    string            `json:"work_dir"`
	Host       string            `json:"host"`
	Port       int               `json:"port"`
	Warnings   []string          `json:"warnings"`
	CmdString  string            `json:"cmd_string"` // For previewing in terminal format
}

// KnownFlag registry definitions for UI convenience
type KnownFlag struct {
	Name        string   `json:"name"`
	Flag        string   `json:"flag"`
	Type        string   `json:"type"` // number, string, boolean
	Description string   `json:"description"`
	Category    string   `json:"category"` // resource, performance, networking, formatting
	Default     string   `json:"default,omitempty"`
	Options     []string `json:"options,omitempty"`
}

// GetKnownFlags returns the registry of standard llama-server arguments.
func GetKnownFlags() []KnownFlag {
	return []KnownFlag{
		{Name: "Context Size", Flag: "-c", Type: "number", Description: "Size of the prompt context (tokens)", Category: "resource", Default: "2048"},
		{Name: "GPU Layers", Flag: "-ngl", Type: "number", Description: "Number of layers to store in VRAM", Category: "resource", Default: "0"},
		{Name: "Thread Count", Flag: "-t", Type: "number", Description: "Number of CPU threads to use", Category: "resource", Default: "4"},
		{Name: "Batch Size", Flag: "-b", Type: "number", Description: "Logical batch size for prompt processing", Category: "performance", Default: "2048"},
		{Name: "Parallel Sequences", Flag: "-np", Type: "number", Description: "Number of sequences/slots to serve concurrently", Category: "performance", Default: "1"},
		{Name: "Flash Attention", Flag: "--flash-attn", Type: "boolean", Description: "Enable flash attention optimization", Category: "performance", Default: "false"},
		{Name: "Embeddings Only", Flag: "--embeddings", Type: "boolean", Description: "Enable embeddings endpoints only", Category: "formatting", Default: "false"},
		{Name: "System Prompt", Flag: "--system-prompt", Type: "string", Description: "Override system prompt", Category: "formatting"},
		{Name: "Chat Template", Flag: "--chat-template", Type: "string", Description: "Override default chat template", Category: "formatting"},
		{Name: "No MMap", Flag: "--no-mmap", Type: "boolean", Description: "Disable memory mapping", Category: "resource", Default: "false"},
		{Name: "Continuous Batching", Flag: "-cb", Type: "boolean", Description: "Enable continuous batching", Category: "performance", Default: "false"},
	}
}

// BuildCommand generates the exact execution details from a profile and model.
func BuildCommand(p *storage.Profile, m *storage.Model, binPath string, runtimePort int) (*BuiltCommand, error) {
	if binPath == "" {
		binPath = "llama-server" // default to path lookup if not specified
	}

	cmd := &BuiltCommand{
		Executable: binPath,
		WorkDir:    p.WorkingDir,
		Host:       p.DefaultHost,
		Port:       runtimePort,
		Warnings:   make([]string, 0),
	}

	if cmd.Host == "" {
		cmd.Host = "127.0.0.1"
	}

	// Prepare environment variables array
	for k, v := range p.Env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Build the argument array
	var finalArgs []string
	
	// Keep track of host, port, model and check if we duplicate them
	hasModel := false
	hasHost := false
	hasPort := false

	// Iterate over the stored args
	args := p.Args
	for i := 0; i < len(args); i++ {
		arg := args[i]

		switch arg {
		case "-m", "--model":
			hasModel = true
			finalArgs = append(finalArgs, arg)
			if i+1 < len(args) {
				// Use the actual model's path (overriding profile-specific hardcoded path if model details changed)
				if m != nil && m.Path != "" {
					finalArgs = append(finalArgs, m.ResolvedPath)
				} else {
					finalArgs = append(finalArgs, args[i+1])
				}
				i++
			} else if m != nil {
				finalArgs = append(finalArgs, m.ResolvedPath)
			}
		case "--host":
			hasHost = true
			finalArgs = append(finalArgs, arg)
			if i+1 < len(args) {
				// Overwrite with default host if defined
				finalArgs = append(finalArgs, cmd.Host)
				i++
			} else {
				finalArgs = append(finalArgs, cmd.Host)
			}
		case "--port", "-p":
			hasPort = true
			finalArgs = append(finalArgs, arg)
			if i+1 < len(args) {
				// Use resolved runtime port
				finalArgs = append(finalArgs, strconv.Itoa(cmd.Port))
				i++
			} else {
				finalArgs = append(finalArgs, strconv.Itoa(cmd.Port))
			}
		default:
			// Self-healing check: if this argument is an mmproj file but isn't preceded by "--mmproj",
			// automatically insert the "--mmproj" flag before it to prevent child server crashes.
			if strings.HasSuffix(strings.ToLower(arg), ".gguf") && strings.Contains(strings.ToLower(filepath.Base(arg)), "mmproj") {
				lastArgWasMMProj := false
				if len(finalArgs) > 0 && finalArgs[len(finalArgs)-1] == "--mmproj" {
					lastArgWasMMProj = true
				}
				if !lastArgWasMMProj {
					finalArgs = append(finalArgs, "--mmproj")
				}
			}
			finalArgs = append(finalArgs, arg)
		}
	}

	// Inject defaults if not present
	if !hasModel && m != nil {
		finalArgs = append(finalArgs, "-m", m.ResolvedPath)
	}
	if !hasHost {
		finalArgs = append(finalArgs, "--host", cmd.Host)
	}
	if !hasPort && cmd.Port > 0 {
		finalArgs = append(finalArgs, "--port", strconv.Itoa(cmd.Port))
	}

	cmd.Args = finalArgs

	// Generate clean string representation for terminal logs / preview
	var cmdStrBuilder strings.Builder
	cmdStrBuilder.WriteString(cmd.Executable)
	
	for _, arg := range cmd.Args {
		if strings.Contains(arg, " ") || strings.Contains(arg, "\\") {
			cmdStrBuilder.WriteString(fmt.Sprintf(" %q", arg))
		} else {
			cmdStrBuilder.WriteString(" " + arg)
		}
	}
	cmd.CmdString = cmdStrBuilder.String()

	// Check warnings
	if m == nil {
		cmd.Warnings = append(cmd.Warnings, "No model associated with this profile.")
	} else {
		// Warn if model file doesn't exist
		if _, err := os.Stat(m.ResolvedPath); os.IsNotExist(err) {
			cmd.Warnings = append(cmd.Warnings, fmt.Sprintf("Model file does not exist: %s", m.ResolvedPath))
		}
	}

	return cmd, nil
}

// ExportJSON generates a standardized JSON export of the profile.
func ExportJSON(p *storage.Profile, m *storage.Model, binPath string) (string, error) {
	type JSONExport struct {
		SchemaVersion  int               `json:"schema_version"`
		Name           string            `json:"name"`
		LlamaServerBin string            `json:"llama_server_bin"`
		ModelPath      string            `json:"model_path"`
		Args           []string          `json:"args"`
		Env            map[string]string `json:"env"`
		Notes          string            `json:"notes"`
	}

	modelPath := ""
	if m != nil {
		modelPath = m.ResolvedPath
	}

	exp := JSONExport{
		SchemaVersion:  1,
		Name:           p.Name,
		LlamaServerBin: binPath,
		ModelPath:      modelPath,
		Args:           p.Args,
		Env:            p.Env,
		Notes:          p.Description,
	}

	data, err := json.MarshalIndent(exp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ExportShell generates a copyable, executable bash launcher script.
func ExportShell(p *storage.Profile, m *storage.Model, binPath string) string {
	if binPath == "" {
		binPath = "/opt/llama.cpp/build/bin/llama-server"
	}

	var sb strings.Builder
	sb.WriteString("#!/usr/bin/env bash\n")
	sb.WriteString("set -euo pipefail\n\n")
	sb.WriteString(fmt.Sprintf("# Profile: %s\n", p.Name))
	if p.Description != "" {
		sb.WriteString(fmt.Sprintf("# Notes: %s\n", p.Description))
	}
	sb.WriteString("\n")

	// Environment vars
	for k, v := range p.Env {
		sb.WriteString(fmt.Sprintf("export %s=%q\n", k, v))
	}
	
	sb.WriteString(fmt.Sprintf("LLAMA_SERVER_BIN=\"${LLAMA_SERVER_BIN:-%s}\"\n\n", binPath))

	if p.WorkingDir != "" {
		sb.WriteString(fmt.Sprintf("cd %q\n\n", p.WorkingDir))
	}

	sb.WriteString("exec \"$LLAMA_SERVER_BIN\" \\\n")

	// Render args beautifully with indent and continuation backslashes
	args := p.Args
	
	// Create fully built args (expanding model path)
	var finalArgs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if (arg == "-m" || arg == "--model") && i+1 < len(args) && m != nil {
			finalArgs = append(finalArgs, arg, m.ResolvedPath)
			i++
		} else {
			finalArgs = append(finalArgs, arg)
		}
	}
	if m != nil {
		hasModel := false
		for _, a := range args {
			if a == "-m" || a == "--model" {
				hasModel = true
				break
			}
		}
		if !hasModel {
			finalArgs = append(finalArgs, "-m", m.ResolvedPath)
		}
	}

	for i := 0; i < len(finalArgs); i++ {
		arg := finalArgs[i]
		
		// Quote values if needed
		valQuote := arg
		if strings.Contains(arg, " ") || strings.Contains(arg, "$") || strings.Contains(arg, "\\") {
			valQuote = fmt.Sprintf("%q", arg)
		}

		if i == len(finalArgs)-1 {
			sb.WriteString(fmt.Sprintf("  %s\n", valQuote))
		} else {
			sb.WriteString(fmt.Sprintf("  %s \\\n", valQuote))
		}
	}

	return sb.String()
}
