package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config represents the studio settings.
type Config struct {
	Listen           string   `json:"listen"`
	LlamaServerBin   string   `json:"llama_server_bin"`
	LlamaBinDir      string   `json:"llama_bin_dir"`
	ModelsDirs       []string `json:"models_dirs"`
	ScanHFCache      bool     `json:"scan_hf_cache"`
	HFCacheDirs      []string `json:"hf_cache_dirs"`
	PortRangeStart   int      `json:"port_range_start"`
	PortRangeEnd     int      `json:"port_range_end"`
	AdminToken       string   `json:"admin_token"`
	AllowInsecureLAN bool     `json:"allow_insecure_lan"`
	DataDir          string   `json:"data_dir"`
	GatewayToken     string   `json:"gateway_token"`
	// AllowedOrigins lists explicit HTTP Origins permitted for CORS. Empty = deny all cross-origin.
	AllowedOrigins []string `json:"allowed_origins"`

	// unexported: computed at startup, not serialised
	bindIsLoopback bool
}

// BindIsLoopback returns true when the listen address resolves to a loopback interface.
func (c *Config) BindIsLoopback() bool { return c.bindIsLoopback }

// SetBindIsLoopback stores the result of the startup loopback computation.
func (c *Config) SetBindIsLoopback(v bool) { c.bindIsLoopback = v }

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()

	// Default HF cache dir
	hfCache := filepath.Join(home, ".cache", "huggingface", "hub")

	return &Config{
		Listen:           "127.0.0.1:3100",
		LlamaServerBin:   "",
		LlamaBinDir:      "",
		ModelsDirs:       []string{filepath.Join(home, "models")},
		ScanHFCache:      true,
		HFCacheDirs:      []string{hfCache},
		PortRangeStart:   41000,
		PortRangeEnd:     41999,
		AdminToken:       "",
		AllowInsecureLAN: false,
		DataDir:          filepath.Join(home, ".llama-server-studio"),
		GatewayToken:     "",
		AllowedOrigins:   []string{},
	}
}

// LoadConfig loads the configuration from a file or returns defaults.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".llama-server-studio", "config.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Save default config with restricted permissions
			_ = SaveConfig(cfg, path)
			return cfg, nil
		}
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// SaveConfig saves the configuration to the specified path with restricted permissions (0600).
func SaveConfig(cfg *Config, path string) error {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".llama-server-studio", "config.json")
	}

	// Create directory with 0700 — owner-only access
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	// Write with 0600 — owner read/write only
	return os.WriteFile(path, data, 0600)
}

