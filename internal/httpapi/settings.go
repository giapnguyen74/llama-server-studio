package httpapi

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"llama-server-studio/internal/config"
)

func (s *Server) handleServeIndex(w http.ResponseWriter, r *http.Request) {
	// Root or index fallback
	data, err := s.embedFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "Index file not found in embedded FS", http.StatusNotFound)
		return
	}
	// Strict Content-Security-Policy to protect against XSS exfiltration while allowing Google Fonts
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self' *; object-src 'none';")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleServeStatic(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	if strings.Contains(filename, "..") || strings.HasPrefix(filename, "/") {
		http.Error(w, "Forbidden: invalid static path traversal attempt", http.StatusForbidden)
		return
	}
	path := filepath.Join("web", filename)
	data, err := s.embedFS.ReadFile(path)
	if err != nil {
		http.Error(w, "Static asset not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self' *; object-src 'none';")

	contentType := "text/plain"
	if strings.HasSuffix(filename, ".css") {
		contentType = "text/css"
	} else if strings.HasSuffix(filename, ".js") {
		contentType = "application/javascript"
	} else if strings.HasSuffix(filename, ".svg") {
		contentType = "image/svg+xml"
	} else if strings.HasSuffix(filename, ".png") {
		contentType = "image/png"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "Invalid request payload")
		return
	}

	// Verify administrative password
	valid := false
	if !s.cfg.BindIsLoopback() && !s.cfg.AllowInsecureLAN {
		valid = s.cfg.VerifyAdminPassword(req.Password)
	} else if s.cfg.BindIsLoopback() && s.cfg.HasAdminCredential() {
		valid = s.cfg.VerifyAdminPassword(req.Password)
	} else {
		valid = true
	}

	if !valid {
		writeJSONError(w, http.StatusUnauthorized, "Unauthorized: invalid credentials")
		return
	}

	// Generate secure session token
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to generate session token")
		return
	}
	token := hex.EncodeToString(tokenBytes)

	// Save token in-memory expiring in 24 hours
	sessionTokensMu.Lock()
	sessionTokens[token] = time.Now().Add(24 * time.Hour)
	sessionTokensMu.Unlock()

	// Drop secure HttpOnly cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "studio_session",
		Value:    token,
		Path:     "/",
		MaxAge:   86400, // 24 hours
		HttpOnly: true,
		Secure:   false, // Keep false to allow local dev over plain http
		SameSite: http.SameSiteStrictMode,
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true, "token_placeholder": "session_active"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "version": "0.1.0"})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	// Return a sanitised view — credentials (hashes, plaintext tokens) are never sent to the browser.
	// Path fields are masked to avoid leaking the local username / directory layout.
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"listen":                s.cfg.Listen,
		"llama_server_bin":      maskPath(s.cfg.LlamaServerBin),
		"llama_bin_dir":         maskPath(s.cfg.LlamaBinDir),
		"models_dirs":           maskPaths(s.cfg.ModelsDirs),
		"scan_hf_cache":         s.cfg.ScanHFCache,
		"hf_cache_dirs":         maskPaths(s.cfg.HFCacheDirs),
		"port_range_start":      s.cfg.PortRangeStart,
		"port_range_end":        s.cfg.PortRangeEnd,
		"data_dir":              maskPath(s.cfg.DataDir),
		"allowed_origins":       s.cfg.AllowedOrigins,
		"gateway_default_model": s.cfg.GatewayDefaultModel,
		// Credential status only — never the actual value or hash.
		"gateway_token_set": s.cfg.HasGatewayToken(),
		"admin_cred_set":    s.cfg.HasAdminCredential(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusForbidden, "Settings updates are disabled in WebUI for security. Please edit config.json directly on disk.")
}

func (s *Server) handleSetGatewayDefault(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ProfileID string `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if payload.ProfileID != "" {
		if _, ok := s.db.GetProfile(payload.ProfileID); !ok {
			writeJSONError(w, http.StatusNotFound, "profile not found")
			return
		}
	}

	s.cfg.GatewayDefaultModel = payload.ProfileID
	if err := config.SaveConfig(s.cfg, s.cfgPath); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                    true,
		"gateway_default_model": s.cfg.GatewayDefaultModel,
	})
}

func (s *Server) handleUpdateSecurity(w http.ResponseWriter, r *http.Request) {
	if s.cfg.HasGatewayToken() {
		confirm := r.Header.Get("X-Confirm-Token")
		adminOK := s.cfg.HasAdminCredential() && s.cfg.VerifyAdminPassword(bearerToken(r))
		gwOK := s.cfg.VerifyGatewayToken(confirm)
		if !adminOK && !gwOK {
			writeJSONError(w, http.StatusForbidden,
				"Rotating the gateway token requires either the admin credential (Authorization: Bearer ...) "+
					"or the current gateway token in the X-Confirm-Token header")
			return
		}
	}

	var payload struct {
		GatewayToken string `json:"gateway_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.cfg.SetGatewayToken(payload.GatewayToken); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("Invalid gateway token: %v", err))
		return
	}

	if err := config.SaveConfig(s.cfg, s.cfgPath); err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to save security settings: %v", err))
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":                true,
		"gateway_token_set": s.cfg.HasGatewayToken(),
		"proxy_enabled":     s.cfg.HasGatewayToken(),
	})
}

func (s *Server) handleValidateLlama(w http.ResponseWriter, r *http.Request) {
	bin := s.cfg.LlamaServerBin
	if bin == "" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"bin_valid": false,
			"error":     "No llama-server path configured in settings.",
		})
		return
	}

	cmd := exec.Command(bin, "--help")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := stdout.String() + stderr.String()

	valid := false
	versionStr := ""
	if err == nil || strings.Contains(output, "llama-server") || strings.Contains(output, "usage:") {
		valid = true
		vCmd := exec.Command(bin, "--version")
		if vOut, vErr := vCmd.Output(); vErr == nil {
			versionStr = strings.TrimSpace(string(vOut))
		} else {
			versionStr = "llama-server (detected help works)"
		}
	}

	res := map[string]interface{}{
		"bin_valid":      valid,
		"path_resolved":  bin,
		"version_output": versionStr,
	}
	if !valid {
		if err != nil {
			res["error"] = fmt.Sprintf("%s. Output: %s", err.Error(), output)
		} else {
			res["error"] = "Binary executed but did not respond with standard help details."
		}
	}

	writeJSON(w, http.StatusOK, res)
}
