package httpapi

import (
	"os"
	"strings"

	"llama-server-studio/internal/storage"
)

// homeDir is resolved once at startup.
var homeDir = func() string {
	h, _ := os.UserHomeDir()
	return h
}()

// maskPath replaces the user's home directory prefix with "~" so that
// absolute paths sent to the browser never reveal the local username or
// directory layout.
func maskPath(p string) string {
	if homeDir == "" || p == "" {
		return p
	}
	// Normalize: trim trailing slash from home so we don't double-mask "~/"
	home := strings.TrimRight(homeDir, "/")
	if strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	if p == home {
		return "~"
	}
	return p
}

// maskPaths applies maskPath to every element of a string slice.
func maskPaths(paths []string) []string {
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = maskPath(p)
	}
	return out
}

// maskModel returns a copy of the model with path fields masked.
func maskModel(m storage.Model) storage.Model {
	m.Path         = maskPath(m.Path)
	m.ResolvedPath = maskPath(m.ResolvedPath)
	return m
}

// maskModels applies maskModel to every element of a slice.
func maskModels(models []storage.Model) []storage.Model {
	out := make([]storage.Model, len(models))
	for i, m := range models {
		out[i] = maskModel(m)
	}
	return out
}
