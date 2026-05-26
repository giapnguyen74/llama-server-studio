package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"
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
	AdminToken         string   `json:"admin_token"`
	AdminPasswordHash  string   `json:"admin_password_hash,omitempty"`
	AllowInsecureLAN   bool     `json:"allow_insecure_lan"`
	DataDir          string   `json:"data_dir"`
	HFToken          string   `json:"hf_token,omitempty"`
	GatewayToken     string   `json:"gateway_token,omitempty"`      // legacy plaintext — migrated on first save
	GatewayTokenHash string   `json:"gateway_token_hash,omitempty"` // bcrypt hash (preferred)
	// AllowedOrigins lists explicit HTTP Origins permitted for CORS. Empty = deny all cross-origin.
	AllowedOrigins []string `json:"allowed_origins"`

	// unexported: computed at startup, not serialised
	bindIsLoopback bool
}

// BindIsLoopback returns true when the listen address resolves to a loopback interface.
func (c *Config) BindIsLoopback() bool { return c.bindIsLoopback }

// SetBindIsLoopback stores the result of the startup loopback computation.
func (c *Config) SetBindIsLoopback(v bool) { c.bindIsLoopback = v }

// HashPassword hashes a plaintext password with bcrypt and stores it in AdminPasswordHash.
// Call SaveConfig afterwards to persist the change.
func (c *Config) HashPassword(plaintext string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c.AdminPasswordHash = string(hash)
	// Clear any legacy plaintext token so only the hash is used for auth.
	c.AdminToken = ""
	return nil
}

// VerifyAdminPassword returns true when the supplied token/password matches the
// configured credential.  Precedence:
//  1. If AdminPasswordHash is set: bcrypt comparison (constant-time).
//  2. Else if AdminToken is set: direct constant-time string comparison.
//  3. Otherwise returns false (no credential configured).
func (c *Config) VerifyAdminPassword(token string) bool {
	if c.AdminPasswordHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(c.AdminPasswordHash), []byte(token)) == nil
	}
	if c.AdminToken != "" {
		// Constant-time compare to avoid timing side-channels even for plain tokens.
		a, b := []byte(token), []byte(c.AdminToken)
		if len(a) != len(b) {
			return false
		}
		var diff byte
		for i := range a {
			diff |= a[i] ^ b[i]
		}
		return diff == 0
	}
	return false
}

// HasAdminCredential returns true when any admin credential (hash or token) is set.
func (c *Config) HasAdminCredential() bool {
	return c.AdminPasswordHash != "" || c.AdminToken != ""
}

// SetGatewayToken hashes plaintext with bcrypt, stores it in GatewayTokenHash and
// clears the legacy plaintext GatewayToken.  Pass an empty string to disable the gateway.
// The token must start with "sk-" and be at least 10 characters long.
func (c *Config) SetGatewayToken(plaintext string) error {
	if plaintext == "" {
		c.GatewayToken = ""
		c.GatewayTokenHash = ""
		return nil
	}
	if !strings.HasPrefix(plaintext, "sk-") || len(plaintext) < 10 {
		return fmt.Errorf("gateway token must start with 'sk-' and be at least 10 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	c.GatewayTokenHash = string(hash)
	c.GatewayToken = "" // clear legacy plaintext
	return nil
}

// VerifyGatewayToken returns true when token matches the configured gateway credential.
// Checks GatewayTokenHash (bcrypt) first, then falls back to plaintext GatewayToken.
func (c *Config) VerifyGatewayToken(token string) bool {
	if c.GatewayTokenHash != "" {
		return bcrypt.CompareHashAndPassword([]byte(c.GatewayTokenHash), []byte(token)) == nil
	}
	if c.GatewayToken != "" {
		a, b := []byte(token), []byte(c.GatewayToken)
		if len(a) != len(b) {
			return false
		}
		var diff byte
		for i := range a {
			diff |= a[i] ^ b[i]
		}
		return diff == 0
	}
	return false
}

// HasGatewayToken returns true when any gateway credential is configured.
func (c *Config) HasGatewayToken() bool {
	return c.GatewayTokenHash != "" || c.GatewayToken != ""
}

// HFDownloadRoot is the directory under which the Hugging Face Hub downloader
// creates per-repo subfolders, as specified in docs/hf_support.md §4.
//
// It is always derived from DataDir (the studio's home dir) rather than from
// ModelsDirs, so downloads have a single canonical home that doesn't shift
// when the user reconfigures scan paths.
func (c *Config) HFDownloadRoot() string {
	return filepath.Join(c.DataDir, "models")
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, ".llama-server-studio")
	hfCache := filepath.Join(home, ".cache", "huggingface", "hub")

	return &Config{
		Listen:           "127.0.0.1:3100",
		LlamaServerBin:   "",
		LlamaBinDir:      "",
		ModelsDirs:       []string{filepath.Join(dataDir, "models")},
		ScanHFCache:      true,
		HFCacheDirs:      []string{hfCache},
		PortRangeStart:   41000,
		PortRangeEnd:     41999,
		AdminToken:       "",
		AllowInsecureLAN: false,
		DataDir:          dataDir,
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

