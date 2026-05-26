package httpapi

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
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

	mux.HandleFunc("POST /profiles/{profile_id}/v1/chat/completions", gatewayAuth(s.handleProxyRoute))
	mux.HandleFunc("POST /profiles/{profile_id}/v1/completions", gatewayAuth(s.handleProxyRoute))
	mux.HandleFunc("POST /profiles/{profile_id}/v1/embeddings", gatewayAuth(s.handleProxyRoute))
	mux.HandleFunc("POST /profiles/{profile_id}/rerank", gatewayAuth(s.handleProxyRoute))
	mux.HandleFunc("GET /profiles/{profile_id}/health", gatewayAuth(s.handleProxyRoute))
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
