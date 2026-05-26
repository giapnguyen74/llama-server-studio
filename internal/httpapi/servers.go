package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"llama-server-studio/internal/stats"
)

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

	_ = s.supervisor.StopServer(id)

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := s.db.GetServer(id)
		if (cur.Status == "stopped" || cur.Status == "crashed") &&
			s.supervisor.IsPortAvailable(srv.Host, srv.Port) {
			break
		}
		select {
		case <-r.Context().Done():
			writeJSONError(w, 499, "client cancelled")
			return
		case <-time.After(150 * time.Millisecond):
		}
	}

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
	limit := 60
	if limitStr != "" {
		if val, err := strconv.Atoi(limitStr); err == nil {
			limit = val
		}
	}

	writeJSON(w, http.StatusOK, s.db.GetServerStats(id, limit))
}

func (s *Server) handleGetSystemMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, stats.GetSystemMetrics())
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
	proxyHost := srv.Host
	if proxyHost == "0.0.0.0" || proxyHost == "::" || proxyHost == "" {
		proxyHost = "127.0.0.1"
	}
	completionURL := fmt.Sprintf("http://%s:%d/completion", proxyHost, srv.Port)

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
