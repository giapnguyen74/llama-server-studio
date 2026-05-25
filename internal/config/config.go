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
}

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
		DataDir:          filepath.Join(home, ".local", "share", "llama-server-studio"),
	}
}

// LoadConfig loads the configuration from a file or returns defaults.
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "llama-server-studio", "config.json")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Save default config
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

// SaveConfig saves the configuration to the specified path.
func SaveConfig(cfg *Config, path string) error {
	if path == "" {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".config", "llama-server-studio", "config.json")
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
