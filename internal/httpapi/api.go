package httpapi

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"llama-server-studio/internal/bench"
	"llama-server-studio/internal/config"
	"llama-server-studio/internal/models"
	"llama-server-studio/internal/process"
	"llama-server-studio/internal/profiles"
	"llama-server-studio/internal/router"
	"llama-server-studio/internal/storage"
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
	// Authentication Middleware wrapping standard mux handlers
	auth := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// CORS headers
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}

			// Validate token if external binding is requested
			if s.cfg.Listen != "127.0.0.1:3100" && !s.cfg.AllowInsecureLAN && s.cfg.AdminToken != "" {
				authHeader := r.Header.Get("Authorization")
				token := strings.TrimPrefix(authHeader, "Bearer ")
				if token == "" {
					token = r.URL.Query().Get("token")
				}
				if token != s.cfg.AdminToken {
					writeJSONError(w, http.StatusUnauthorized, "Unauthorized: Invalid admin token")
					return
				}
			}
			h(w, r)
		}
	}

	// 1. Static Assets & Embedded Web UI
	mux.HandleFunc("GET /", s.handleServeIndex)
	mux.HandleFunc("GET /static/{filename}", s.handleServeStatic)

	// 2. Health & Diagnostic API
	mux.HandleFunc("GET /api/health", auth(s.handleHealth))
	mux.HandleFunc("GET /api/settings", auth(s.handleGetSettings))
	mux.HandleFunc("PUT /api/settings", auth(s.handlePutSettings))
	mux.HandleFunc("POST /api/settings/validate-llama", auth(s.handleValidateLlama))

	// 3. Models API
	mux.HandleFunc("GET /api/models", auth(s.handleListModels))
	mux.HandleFunc("POST /api/models/rescan", auth(s.handleRescanModels))
	mux.HandleFunc("GET /api/models/{model_id}", auth(s.handleGetModel))
	mux.HandleFunc("POST /api/models/{model_id}/hide", auth(s.handleHideModel))

	// 4. Profiles API
	mux.HandleFunc("GET /api/profiles", auth(s.handleListProfiles))
	mux.HandleFunc("POST /api/profiles", auth(s.handleCreateProfile))
	mux.HandleFunc("GET /api/profiles/{profile_id}", auth(s.handleGetProfile))
	mux.HandleFunc("PUT /api/profiles/{profile_id}", auth(s.handleUpdateProfile))
	mux.HandleFunc("DELETE /api/profiles/{profile_id}", auth(s.handleDeleteProfile))
	mux.HandleFunc("POST /api/profiles/{profile_id}/clone", auth(s.handleCloneProfile))
	mux.HandleFunc("GET /api/profiles/{profile_id}/export.json", s.handleExportProfileJSON)
	mux.HandleFunc("GET /api/profiles/{profile_id}/export.sh", s.handleExportProfileSH)

	// 5. Managed Servers API
	mux.HandleFunc("GET /api/servers", auth(s.handleListServers))
	mux.HandleFunc("POST /api/profiles/{profile_id}/start", auth(s.handleStartServer))
	mux.HandleFunc("POST /api/servers/{server_id}/stop", auth(s.handleStopServer))
	mux.HandleFunc("POST /api/servers/{server_id}/restart", auth(s.handleRestartServer))
	mux.HandleFunc("GET /api/servers/{server_id}", auth(s.handleGetServer))
	mux.HandleFunc("GET /api/servers/{server_id}/logs", auth(s.handleGetServerLogs))
	mux.HandleFunc("GET /api/servers/{server_id}/logs/download", s.handleDownloadServerLogs)
	mux.HandleFunc("GET /api/servers/{server_id}/stats", auth(s.handleGetServerStats))
	mux.HandleFunc("POST /api/servers/{server_id}/test", auth(s.handleTestServer))

	// 6. Benchmarks API
	mux.HandleFunc("GET /api/benchmarks", auth(s.handleListBenchmarks))
	mux.HandleFunc("POST /api/benchmarks", auth(s.handleRunBenchmark))
	mux.HandleFunc("DELETE /api/benchmarks/{run_id}", auth(s.handleDeleteBenchmark))

	// 7. Stable Profile Routing Gateway Proxy
	mux.HandleFunc("POST /profiles/{profile_id}/v1/chat/completions", s.handleProxyRoute)
	mux.HandleFunc("POST /profiles/{profile_id}/v1/completions", s.handleProxyRoute)
	mux.HandleFunc("POST /profiles/{profile_id}/v1/embeddings", s.handleProxyRoute)
	mux.HandleFunc("GET /profiles/{profile_id}/health", s.handleProxyRoute)
}

// --- Handlers Implementation ---

func (s *Server) handleServeIndex(w http.ResponseWriter, r *http.Request) {
	// Root or index fallback
	data, err := s.embedFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "Index file not found in embedded FS", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleServeStatic(w http.ResponseWriter, r *http.Request) {
	filename := r.PathValue("filename")
	path := filepath.Join("web", filename)
	data, err := s.embedFS.ReadFile(path)
	if err != nil {
		http.Error(w, "Static asset not found", http.StatusNotFound)
		return
	}

	contentType := "text/plain"
	if strings.HasSuffix(filename, ".css") {
		contentType = "text/css"
	} else if strings.HasSuffix(filename, ".js") {
		contentType = "application/javascript"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true, "version": "0.1.0"})
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.cfg)
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	writeJSONError(w, http.StatusForbidden, "Settings updates are disabled in WebUI for security. Please edit config.json directly on disk.")
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

	// Try running binary --help
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
		// Guess version by executing --version
		vCmd := exec.Command(bin, "--version")
		if vOut, vErr := vCmd.Output(); vErr == nil {
			versionStr = strings.TrimSpace(string(vOut))
		} else {
			versionStr = "llama-server (detected help works)"
		}
	}

	res := map[string]interface{}{
		"bin_valid":       valid,
		"path_resolved":   bin,
		"version_output":  versionStr,
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

// --- Models handlers ---

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListModels())
}

func (s *Server) handleRescanModels(w http.ResponseWriter, r *http.Request) {
	err := models.ScanDirectories(s.db, s.cfg.ModelsDirs, s.cfg.ScanHFCache, s.cfg.HFCacheDirs)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "rescan completed"})
}

func (s *Server) handleGetModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model_id")
	m, ok := s.db.GetModel(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "model not found")
		return
	}
	writeJSON(w, http.StatusOK, m)
}

func (s *Server) handleHideModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model_id")
	if err := s.db.HideModel(id, true); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

// --- Profiles handlers ---

func (s *Server) handleListProfiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListProfiles())
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	var p storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Generate stable UUID alternative for Go standard library
	p.ID = generateUUID()
	p.CreatedAt = time.Now().Format(time.RFC3339)
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	existing, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}

	var p storage.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	p.ID = id
	p.CreatedAt = existing.CreatedAt
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	if err := s.db.DeleteProfile(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCloneProfile(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "profile not found")
		return
	}

	p.ID = generateUUID()
	p.Name = p.Name + " (Clone)"
	p.CreatedAt = time.Now().Format(time.RFC3339)
	p.UpdatedAt = time.Now().Format(time.RFC3339)

	if err := s.db.SaveProfile(p); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, p)
}

func (s *Server) handleExportProfileJSON(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	m, _ := s.db.GetModel(p.ModelID)
	data, err := profiles.ExportJSON(&p, &m, s.cfg.LlamaServerBin)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=profile-%s.json", id))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(data))
}

func (s *Server) handleExportProfileSH(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	p, ok := s.db.GetProfile(id)
	if !ok {
		http.Error(w, "Profile not found", http.StatusNotFound)
		return
	}

	m, _ := s.db.GetModel(p.ModelID)
	data := profiles.ExportShell(&p, &m, s.cfg.LlamaServerBin)

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=profile-%s.sh", id))
	w.Header().Set("Content-Type", "application/x-sh")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(data))
}

// --- Servers handlers ---

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListServers())
}

func (s *Server) handleStartServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("profile_id")
	srvID, err := s.supervisor.StartServer(id, s.cfg.LlamaServerBin, s.cfg.PortRangeStart, s.cfg.PortRangeEnd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	srv, _ := s.db.GetServer(srvID)
	writeJSON(w, http.StatusOK, srv)
}

func (s *Server) handleStopServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	if err := s.supervisor.StopServer(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "stopping request sent"})
}

func (s *Server) handleRestartServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	srv, ok := s.db.GetServer(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}

	// 1. Stop it
	_ = s.supervisor.StopServer(id)
	
	// Wait a moment
	time.Sleep(1 * time.Second)

	// 2. Start it again
	newSrvID, err := s.supervisor.StartServer(srv.ProfileID, s.cfg.LlamaServerBin, s.cfg.PortRangeStart, s.cfg.PortRangeEnd)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	newSrv, _ := s.db.GetServer(newSrvID)
	writeJSON(w, http.StatusOK, newSrv)
}

func (s *Server) handleGetServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	srv, ok := s.db.GetServer(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}
	writeJSON(w, http.StatusOK, srv)
}

func (s *Server) handleGetServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	limitStr := r.URL.Query().Get("limit")
	limit := 300
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	lines, err := s.db.GetLogFileLines(id, limit)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lines)
}

func (s *Server) handleDownloadServerLogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	path := s.db.GetLogFilePath(id)
	file, err := os.Open(path)
	if err != nil {
		http.Error(w, "Logs file not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=server-%s.log", id))
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, file)
}

func (s *Server) handleGetServerStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	limitStr := r.URL.Query().Get("limit")
	limit := 60 // Keep default last 60 samples
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	writeJSON(w, http.StatusOK, s.db.GetServerStats(id, limit))
}

func (s *Server) handleTestServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("server_id")
	srv, ok := s.db.GetServer(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "server not found")
		return
	}

	var reqPayload struct {
		Prompt    string  `json:"prompt"`
		Temp      float64 `json:"temp"`
		MaxTokens int     `json:"max_tokens"`
		Stream    bool    `json:"stream"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	client := &http.Client{Timeout: 120 * time.Second}
	completionURL := fmt.Sprintf("http://%s:%d/completion", srv.Host, srv.Port)

	// Hit llama-server directly
	body, _ := json.Marshal(map[string]interface{}{
		"prompt":    reqPayload.Prompt,
		"n_predict": reqPayload.MaxTokens,
		"temp":      reqPayload.Temp,
		"stream":    reqPayload.Stream,
	})

	req, err := http.NewRequest("POST", completionURL, bytes.NewBuffer(body))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusServiceUnavailable, fmt.Sprintf("Failed to query child server: %s", err.Error()))
		return
	}
	defer resp.Body.Close()

	if reqPayload.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		// Read and stream back chunks
		flusher, ok := w.(http.Flusher)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				_, _ = w.Write(buf[:n])
				if ok {
					flusher.Flush()
				}
			}
			if err != nil {
				break
			}
		}
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}
}

// --- Benchmarks handlers ---

func (s *Server) handleListBenchmarks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListBenchmarkRuns())
}

func (s *Server) handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	var reqPayload struct {
		ProfileID string  `json:"profile_id"`
		Prompt    string  `json:"prompt"`
		MaxTokens int     `json:"max_tokens"`
		Temp      float64 `json:"temperature"`
		Repeats   int     `json:"repeats"`
		Warmups   int     `json:"warmups"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	run, err := s.benchRunner.RunBenchmark(
		reqPayload.ProfileID,
		reqPayload.Prompt,
		reqPayload.MaxTokens,
		reqPayload.Temp,
		reqPayload.Repeats,
		reqPayload.Warmups,
	)

	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, run)
}

func (s *Server) handleDeleteBenchmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run_id")
	if err := s.db.DeleteBenchmarkRun(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Stable Proxy Route Handler ---

func (s *Server) handleProxyRoute(w http.ResponseWriter, r *http.Request) {
	profileID := r.PathValue("profile_id")
	s.proxyRouter.ProxyRequest(w, r, profileID)
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
