package httpapi

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/bench"
	"llama-server-studio/internal/config"
	"llama-server-studio/internal/process"
	"llama-server-studio/internal/router"
	"llama-server-studio/internal/storage"
)

var (
	sessionTokens   = make(map[string]time.Time)
	sessionTokensMu sync.RWMutex
)

// Server encapsulates our API services and handles routing.
type Server struct {
	db          *storage.DB
	supervisor  *process.Supervisor
	benchRunner *bench.Runner
	proxyRouter *router.Router
	cfg         *config.Config
	cfgPath     string
	embedFS     embed.FS
}

// NewServer creates a new API server instance.
func NewServer(
	db *storage.DB,
	s *process.Supervisor,
	br *bench.Runner,
	r *router.Router,
	cfg *config.Config,
	cfgPath string,
	embedFS embed.FS,
) *Server {
	return &Server{
		db:          db,
		supervisor:  s,
		benchRunner: br,
		proxyRouter: r,
		cfg:         cfg,
		cfgPath:     cfgPath,
		embedFS:     embedFS,
	}
}

// RegisterRoutes sets up Go 1.22+ native REST routes.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Authentication + CORS Middleware
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// -- Host-header validation (DNS Rebinding protection) --
			reqHost := r.Host
			if sh, _, err := net.SplitHostPort(r.Host); err == nil {
				reqHost = sh
			}
			isLoopbackHost := reqHost == "localhost" || reqHost == "127.0.0.1" || reqHost == "::1"
			if !isLoopbackHost {
				listenHost := ""
				if lh, _, err := net.SplitHostPort(s.cfg.Listen); err == nil {
					listenHost = lh
				} else {
					listenHost = s.cfg.Listen
				}
				hostAllowed := false
				if listenHost != "" && listenHost != "0.0.0.0" && listenHost != "[::]" && listenHost != "::" && strings.EqualFold(reqHost, listenHost) {
					hostAllowed = true
				} else {
					for _, o := range s.cfg.AllowedOrigins {
						if u, err := url.Parse(o); err == nil {
							if strings.EqualFold(u.Hostname(), reqHost) {
								hostAllowed = true
								break
							}
						}
					}
					if net.ParseIP(reqHost) != nil {
						hostAllowed = true
					}
				}
				if !hostAllowed {
					writeJSONError(w, http.StatusForbidden, "Forbidden: invalid Host header (DNS rebinding protection)")
					return
				}
			}

			// -- CORS: restrict to allowed origins only --
			if origin := r.Header.Get("Origin"); origin != "" {
				if isSameOrigin(origin, s.cfg.Listen) || isAllowedOrigin(origin, s.cfg.AllowedOrigins) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Confirm-Token")
					w.Header().Set("Vary", "Origin")
				} else {
					writeJSONError(w, http.StatusForbidden, "cross-origin requests not allowed")
					return
				}
			}

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// -- Admin credential enforcement --
			hasValidSession := false
			if cookie, err := r.Cookie("studio_session"); err == nil {
				sessionTokensMu.RLock()
				expiry, exists := sessionTokens[cookie.Value]
				sessionTokensMu.RUnlock()
				if exists && time.Now().Before(expiry) {
					hasValidSession = true
				}
			}

			if !hasValidSession {
				if !s.cfg.BindIsLoopback() && !s.cfg.AllowInsecureLAN {
					if !s.cfg.VerifyAdminPassword(bearerToken(r)) {
						writeJSONError(w, http.StatusUnauthorized, "Unauthorized: invalid admin credential")
						return
					}
				} else if s.cfg.BindIsLoopback() && s.cfg.HasAdminCredential() {
					if !s.cfg.VerifyAdminPassword(bearerToken(r)) {
						writeJSONError(w, http.StatusUnauthorized, "Unauthorized: invalid admin credential")
						return
					}
				}
			}

			h(w, r)
		}
	}

	// 1. Static Assets & Embedded Web UI
	mux.HandleFunc("GET /", s.handleServeIndex)
	mux.HandleFunc("GET /static/{filename}", s.handleServeStatic)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)

	// 2. Health & Diagnostic API
	mux.HandleFunc("GET /api/health", auth(s.handleHealth))
	mux.HandleFunc("GET /api/settings", auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", auth(s.handlePutSettings))
	mux.HandleFunc("POST /api/settings/gateway-default", auth(s.handleSetGatewayDefault))
	mux.HandleFunc("POST /api/settings/security", auth(s.handleUpdateSecurity))
	mux.HandleFunc("POST /api/settings/validate-llama", auth(s.handleValidateLlama))

	// 3. Models API
	mux.HandleFunc("GET /api/models", auth(s.handleListModels))
	mux.HandleFunc("POST /api/models/rescan", auth(s.handleRescanModels))
	mux.HandleFunc("GET /api/models/{model_id}", auth(s.handleGetModel))
	mux.HandleFunc("POST /api/models/{model_id}/hide", auth(s.handleHideModel))
	mux.HandleFunc("DELETE /api/models/{model_id}", auth(s.handleDeleteModel))
	mux.HandleFunc("GET /api/models/scan-status", auth(s.handleScanStatus))
	mux.HandleFunc("GET /api/state", auth(s.handleGetState))


	// 3b. Hugging Face Hub downloader — see docs/hf_support.md §6.
	mux.HandleFunc("GET /api/hf/repo", auth(s.handleHFRepo))
	mux.HandleFunc("GET /api/hf/repo/readme", auth(s.handleHFRepoReadme))
	mux.HandleFunc("POST /api/hf/jobs", auth(s.handleHFStartJob))
	mux.HandleFunc("GET /api/hf/jobs/current", auth(s.handleHFCurrentJob))
	mux.HandleFunc("POST /api/hf/jobs/current/cancel", auth(s.handleHFCancelJob))
	mux.HandleFunc("GET /api/hf/jobs/resumable", auth(s.handleHFResumable))


	// 3c. Legacy single-file shim
	mux.HandleFunc("POST /api/models/download", auth(s.handleDownloadModelLegacy))
	mux.HandleFunc("GET /api/models/downloads", auth(s.handleGetModelDownloadsLegacy))

	// 4. Profiles API
	mux.HandleFunc("GET /api/profiles", auth(s.handleListProfiles))
	mux.HandleFunc("POST /api/profiles", auth(s.handleCreateProfile))
	mux.HandleFunc("GET /api/profiles/{profile_id}", auth(s.handleGetProfile))
	mux.HandleFunc("PUT /api/profiles/{profile_id}", auth(s.handleUpdateProfile))
	mux.HandleFunc("DELETE /api/profiles/{profile_id}", auth(s.handleDeleteProfile))
	mux.HandleFunc("POST /api/profiles/{profile_id}/clone", auth(s.handleCloneProfile))
	mux.HandleFunc("GET /api/profiles/{profile_id}/export.json", auth(s.handleExportProfileJSON))
	mux.HandleFunc("GET /api/profiles/{profile_id}/export.sh", auth(s.handleExportProfileSH))

	// 5. Managed Servers API
	mux.HandleFunc("GET /api/servers", auth(s.handleListServers))
	mux.HandleFunc("POST /api/profiles/{profile_id}/start", auth(s.handleStartServer))
	mux.HandleFunc("POST /api/servers/{server_id}/stop", auth(s.handleStopServer))
	mux.HandleFunc("POST /api/servers/{server_id}/restart", auth(s.handleRestartServer))
	mux.HandleFunc("GET /api/servers/{server_id}", auth(s.handleGetServer))
	mux.HandleFunc("GET /api/servers/{server_id}/logs", auth(s.handleGetServerLogs))
	mux.HandleFunc("GET /api/servers/{server_id}/logs/download", auth(s.handleDownloadServerLogs))
	mux.HandleFunc("GET /api/servers/{server_id}/stats", auth(s.handleGetServerStats))
	mux.HandleFunc("GET /api/system/metrics", auth(s.handleGetSystemMetrics))
	mux.HandleFunc("POST /api/servers/{server_id}/test", auth(s.handleTestServer))

	// 6. Benchmarks API
	mux.HandleFunc("GET /api/benchmarks", auth(s.handleListBenchmarks))
	mux.HandleFunc("GET /api/benchmarks/corpus", auth(s.handleListCorpus))
	mux.HandleFunc("POST /api/benchmarks", auth(s.handleRunBenchmark))
	mux.HandleFunc("DELETE /api/benchmarks/{run_id}", auth(s.handleDeleteBenchmark))
}

// RegisterGatewayRoutes sets up Go 1.22+ native REST gateway proxy routes on a separate port.
func (s *Server) RegisterGatewayRoutes(mux *http.ServeMux) {
	// CORS is '*' and requests are guarded by the gateway token
	gatewayAuth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// If no gateway token is configured, the proxy is disabled entirely.
			if !s.cfg.HasGatewayToken() {
				writeJSONError(w, http.StatusServiceUnavailable,
					"Proxy gateway is disabled: no gateway_token is configured. "+
						"Set one in the Security Gateway settings to enable the proxy endpoint.")
				return
			}

			authHeader := r.Header.Get("Authorization")
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == "" {
				token = r.URL.Query().Get("token")
			}
			if !s.cfg.VerifyGatewayToken(token) {
				writeJSONError(w, http.StatusUnauthorized, "Unauthorized: invalid gateway token. Use Authorization: Bearer <gateway_token>")
				return
			}

			h(w, r)
		}
	}

	// Intercepted OpenAI model list & endpoint health checks
	mux.HandleFunc("GET /models", gatewayAuth(s.handleGatewayListModels))
	mux.HandleFunc("GET /v1/models", gatewayAuth(s.handleGatewayListModels))
	mux.HandleFunc("GET /v1/models/{model_id}", gatewayAuth(s.handleGatewayGetModel))
	mux.HandleFunc("GET /health", gatewayAuth(s.handleGatewayHealth))


	// Catch-all model routed endpoint
	mux.HandleFunc("/", gatewayAuth(s.handleGatewayCatchAll))
}

// --- Helpers ---

func writeJSON(w http.ResponseWriter, code int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func generateUUID() string {
	bytes := make([]byte, 16)
	_, _ = rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func slugify(name string) string {
	s := strings.ToLower(name)
	s = strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			return r
		}
		return '-'
	}, s)

	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	s = strings.Trim(s, "-")
	if s == "" {
		s = "profile"
	}
	return s
}

// bearerToken extracts a Bearer token from the Authorization header or ?token= query param.
func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// isSameOrigin returns true when the Origin header matches the studio listen address.
func isSameOrigin(origin, listen string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	listenHost, listenPort, _ := net.SplitHostPort(listen)
	originHost := u.Hostname()
	originPort := u.Port()

	if listenPort != originPort {
		return false
	}

	loopback := map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true}
	if loopback[listenHost] && loopback[originHost] {
		return true
	}
	return listenHost == originHost
}

// isAllowedOrigin checks if the origin appears in the explicit allowlist from config.
func isAllowedOrigin(origin string, allowed []string) bool {
	for _, a := range allowed {
		if strings.EqualFold(a, origin) {
			return true
		}
	}
	return false
}

func (s *Server) handleGatewayHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleGatewayListModels(w http.ResponseWriter, r *http.Request) {
	profilesList := s.db.ListProfiles()
	var data []map[string]interface{}

	servers := s.db.ListServers()

	for _, p := range profilesList {
		var created int64 = 0
		if t, err := time.Parse(time.RFC3339, p.CreatedAt); err == nil {
			created = t.Unix()
		}

		status := "stopped"
		var port int = 0
		for _, srv := range servers {
			if srv.ProfileID == p.ID && (srv.Status == "healthy" || srv.Status == "ready") {
				status = "ready"
				port = srv.Port
				break
			}
		}

		m := map[string]interface{}{
			"id":                  p.ID,
			"object":              "model",
			"created":             created,
			"owned_by":            "llama-server-studio",
			"studio_status":       status,
			"studio_profile_name": p.Name,
			"studio_port":         port,
		}
		if p.ID == s.cfg.GatewayDefaultModel {
			m["studio_is_default"] = true
		}
		data = append(data, m)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

func (s *Server) handleGatewayGetModel(w http.ResponseWriter, r *http.Request) {
	modelID := r.PathValue("model_id")
	p, ok := s.db.GetProfile(modelID)
	if !ok {
		writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("Model '%s' not found", modelID), "invalid_request_error", "model_not_found", "model")
		return
	}

	var created int64 = 0
	if t, err := time.Parse(time.RFC3339, p.CreatedAt); err == nil {
		created = t.Unix()
	}

	status := "stopped"
	var port int = 0
	for _, srv := range s.db.ListServers() {
		if srv.ProfileID == p.ID && (srv.Status == "healthy" || srv.Status == "ready") {
			status = "ready"
			port = srv.Port
			break
		}
	}

	m := map[string]interface{}{
		"id":                  p.ID,
		"object":              "model",
		"created":             created,
		"owned_by":            "llama-server-studio",
		"studio_status":       status,
		"studio_profile_name": p.Name,
		"studio_port":         port,
	}
	if p.ID == s.cfg.GatewayDefaultModel {
		m["studio_is_default"] = true
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleGatewayCatchAll(w http.ResponseWriter, r *http.Request) {
	model, err := s.extractModel(r)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, fmt.Sprintf("Invalid request body: %v", err), "invalid_request_error", "invalid_body", "model")
		return
	}

	if model == "" {
		if s.cfg.GatewayDefaultModel != "" {
			model = s.cfg.GatewayDefaultModel
		} else {
			writeOpenAIError(w, http.StatusBadRequest, "No model specified in request payload and no default model is configured", "invalid_request_error", "missing_model", "model")
			return
		}
	}

	p, ok := s.db.GetProfile(model)
	if !ok {
		if model == s.cfg.GatewayDefaultModel {
			writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("Configuration default model '%s' could not be resolved (the profile may have been deleted)", model), "studio_default_stale", "default_profile_missing", "model")
		} else {
			writeOpenAIError(w, http.StatusNotFound, fmt.Sprintf("Model '%s' not found", model), "invalid_request_error", "model_not_found", "model")
		}
		return
	}

	s.proxyRouter.ProxyByModel(w, r, p)
}

func (s *Server) extractModel(r *http.Request) (string, error) {
	ct := r.Header.Get("Content-Type")

	if r.Method == "GET" || r.Method == "DELETE" || r.Method == "HEAD" || r.ContentLength == 0 {
		return r.URL.Query().Get("model"), nil
	}

	if strings.Contains(ct, "application/json") {
		limit := s.cfg.GatewayMaxJSONBytes
		if limit <= 0 {
			limit = 16 * 1024 * 1024
		}
		if r.ContentLength > limit {
			return "", fmt.Errorf("Request Content-Length exceeds maximum allowed payload size")
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, limit))
		if err != nil {
			return "", err
		}
		r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		var probe struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal(bodyBytes, &probe); err != nil {
			return "", err
		}
		return probe.Model, nil
	}

	if strings.Contains(ct, "multipart/form-data") || strings.Contains(ct, "application/x-www-form-urlencoded") {
		limit := s.cfg.GatewayMaxUploadBytes
		if limit <= 0 {
			limit = 64 * 1024 * 1024
		}
		if r.ContentLength > limit {
			return "", fmt.Errorf("Request Content-Length exceeds maximum allowed payload size")
		}

		bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, limit))
		if err != nil {
			return "", err
		}
		r.Body.Close()

		r.Body = io.NopCloser(bytes.NewReader(bodyBytes))

		tempReq, _ := http.NewRequest(r.Method, r.URL.String(), bytes.NewReader(bodyBytes))
		tempReq.Header.Set("Content-Type", ct)

		if strings.Contains(ct, "multipart/form-data") {
			if err := tempReq.ParseMultipartForm(32 << 20); err != nil {
				return "", err
			}
			return tempReq.FormValue("model"), nil
		} else {
			if err := tempReq.ParseForm(); err != nil {
				return "", err
			}
			return tempReq.FormValue("model"), nil
		}
	}

	return r.URL.Query().Get("model"), nil
}

func writeOpenAIError(w http.ResponseWriter, code int, message, errType, errCode, param string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	errObj := map[string]interface{}{
		"message": message,
		"type":    errType,
		"code":    errCode,
	}
	if param != "" {
		errObj["param"] = param
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": errObj})
}
