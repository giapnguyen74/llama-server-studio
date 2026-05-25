package router

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/config"
	"llama-server-studio/internal/process"
	"llama-server-studio/internal/storage"
)

type responseRecorder struct {
	http.ResponseWriter
	statusCode int
	bytesWritten int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.statusCode == 0 {
		r.statusCode = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytesWritten += n
	return n, err
}

func (r *responseRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Router handles API proxying by profile ID.
type Router struct {
	db         *storage.DB
	supervisor *process.Supervisor
	proxies    map[string]*httputil.ReverseProxy
	cfg        *config.Config
	mu         sync.RWMutex
}

// NewRouter creates a new API reverse-proxy router.
func NewRouter(db *storage.DB, s *process.Supervisor, cfg *config.Config) *Router {
	return &Router{
		db:         db,
		supervisor: s,
		proxies:    make(map[string]*httputil.ReverseProxy),
		cfg:        cfg,
	}
}

// ProxyRequest handles routing requests.
func (rt *Router) ProxyRequest(w http.ResponseWriter, r *http.Request, profileID string) {
	startTime := time.Now()

	// 1. Verify Gateway Token Security if configured
	if rt.cfg.GatewayToken != "" {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("token")
		}
		
		if token != rt.cfg.GatewayToken {
			writeJSONError(w, http.StatusUnauthorized, "Unauthorized: Invalid gateway token. Please specify Authorization: Bearer sk-xyz")
			return
		}
	}

	// 2. Validate profile
	p, ok := rt.db.GetProfile(profileID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Profile '%s' not found", profileID))
		return
	}

	// 2. Find active server for profile using primaryInstancePolicy (default "latest-ready")
	var activeSrv *storage.Server
	servers := rt.db.ListServers()
	
	var candidateServers []storage.Server
	for _, s := range servers {
		if s.ProfileID == profileID && (s.Status == "healthy" || s.Status == "ready" || s.Status == "starting") {
			candidateServers = append(candidateServers, s)
		}
	}
	
	if len(candidateServers) > 0 {
		best := candidateServers[0]
		for _, cs := range candidateServers[1:] {
			if cs.StartedAt > best.StartedAt {
				best = cs
			}
		}
		activeSrv = &best
	}

	// 3. Handle Auto-Start if stopped
	if activeSrv == nil {
		autoStart := false
		if p.Routing != nil && p.Routing.Enabled && p.Routing.AutoStart {
			autoStart = true
		}
		
		if !autoStart {
			writeJSONError(w, http.StatusConflict, fmt.Sprintf("Server for profile '%s' is stopped. autoStart is disabled.", p.Name))
			return
		}
		
		// Trigger server launch
		srvID, err := rt.supervisor.StartServer(profileID, rt.cfg.LlamaServerBin, rt.cfg.PortRangeStart, rt.cfg.PortRangeEnd)
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Failed to auto-start server: %v", err))
			return
		}
		
		// Poll until server status transitions to "healthy" or "ready"
		pollCtx, pollCancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer pollCancel()
		
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		
		var launchedSrv storage.Server
		foundReady := false
		for !foundReady {
			select {
			case <-pollCtx.Done():
				writeJSONError(w, http.StatusGatewayTimeout, "Gateway Timeout: Auto-started server failed to become ready in time")
				return
			case <-ticker.C:
				if s, ok := rt.db.GetServer(srvID); ok {
					if s.Status == "healthy" || s.Status == "ready" {
						launchedSrv = s
						foundReady = true
					} else if s.Status == "crashed" || s.Status == "stopped" {
						writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("Server crashed or stopped during auto-start: %s", s.LastError))
						return
					}
				}
			}
		}
		activeSrv = &launchedSrv
	}

	// 4. Obtain or initialize ReverseProxy
	targetURLStr := fmt.Sprintf("http://%s:%d", activeSrv.Host, activeSrv.Port)
	targetURL, err := url.Parse(targetURLStr)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Failed to resolve server URL")
		return
	}

	rt.mu.Lock()
	proxy, exists := rt.proxies[targetURLStr]
	if !exists {
		proxy = httputil.NewSingleHostReverseProxy(targetURL)
		
		// Set dynamic flush interval for fast real-time chat tokens stream
		proxy.FlushInterval = 50 * time.Millisecond
		
		// Configure custom director
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			
			// Strip '/profiles/{profile_id}' from the path
			prefix := fmt.Sprintf("/profiles/%s", profileID)
			req.URL.Path = strings.TrimPrefix(req.URL.Path, prefix)
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}
			
			// Retain Host header for safety
			req.Host = targetURL.Host
		}
		
		rt.proxies[targetURLStr] = proxy
	}
	rt.mu.Unlock()

	// 5. Capture request metrics using ResponseWriter wrapper
	recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	
	proxy.ServeHTTP(recorder, r)

	// 6. Track statistics
	duration := time.Since(startTime)
	isError := recorder.statusCode >= 500 || recorder.statusCode == 0

	go func() {
		// Log sample
		errCount := 0
		if isError {
			errCount = 1
		}

		cpuPercent, rssMemory, _ := rt.supervisor.GetProcessStats(activeSrv.ID)

		sample := storage.StatsSample{
			ServerID:        activeSrv.ID,
			SampledAt:       time.Now().Format(time.RFC3339),
			CPUPercent:      cpuPercent,
			MemoryRSSBytes:  rssMemory,
			RequestCount:    1,
			ErrorCount:      errCount,
			AvgLatencyMS:    float64(duration.Milliseconds()),
			P50LatencyMS:    float64(duration.Milliseconds()),
			P95LatencyMS:    float64(duration.Milliseconds()),
			TokensPerSecond: 0, // Injected via logs/benchmark where possible
		}
		_ = rt.db.AddStatsSample(sample)
	}()
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
