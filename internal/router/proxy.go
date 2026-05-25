package router

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

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
	mu         sync.RWMutex
}

// NewRouter creates a new API reverse-proxy router.
func NewRouter(db *storage.DB, s *process.Supervisor) *Router {
	return &Router{
		db:         db,
		supervisor: s,
		proxies:    make(map[string]*httputil.ReverseProxy),
	}
}

// ServeHTTP handles routing requests.
func (rt *Router) ProxyRequest(w http.ResponseWriter, r *http.Request, profileID string) {
	startTime := time.Now()

	// 1. Validate profile
	p, ok := rt.db.GetProfile(profileID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Profile '%s' not found", profileID))
		return
	}

	// 2. Find active server for profile
	srv, ok := rt.db.GetServerByProfile(profileID)
	if !ok || (srv.Status != "healthy" && srv.Status != "starting") {
		writeJSONError(w, http.StatusServiceUnavailable, fmt.Sprintf("Server for profile '%s' is stopped. Start the server first.", p.Name))
		return
	}

	// 3. Obtain or initialize ReverseProxy
	targetURLStr := fmt.Sprintf("http://%s:%d", srv.Host, srv.Port)
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

	// 4. Capture request metrics using ResponseWriter wrapper
	recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
	
	proxy.ServeHTTP(recorder, r)

	// 5. Track statistics
	duration := time.Since(startTime)
	isError := recorder.statusCode >= 500 || recorder.statusCode == 0

	go func() {
		// Log sample
		errCount := 0
		if isError {
			errCount = 1
		}

		cpuPercent, rssMemory, _ := rt.supervisor.GetProcessStats(srv.ID)

		sample := storage.StatsSample{
			ServerID:        srv.ID,
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
