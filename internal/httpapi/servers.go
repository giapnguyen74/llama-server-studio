package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"llama-server-studio/internal/stats"
	"llama-server-studio/internal/storage"
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

type quickTestReq struct {
    Prompt      string  `json:"prompt"`
    MaxTokens   int     `json:"max_tokens"`
    Temperature float64 `json:"temperature"`
    Stream      bool    `json:"stream"`
    Attachment  *struct {
        Kind string `json:"kind"` // "image" | "audio"
        MIME string `json:"mime"`
        Data string `json:"data"` // base64, no prefix
    } `json:"attachment,omitempty"`
}

func (s *Server) handleTestServer(w http.ResponseWriter, r *http.Request) {
    id := r.PathValue("server_id")
    srv, ok := s.db.GetServer(id)
    if !ok { writeJSONError(w, 404, "server not found"); return }
    if srv.Status != "healthy" && srv.Status != "ready" {
        writeJSONError(w, 409, "server not healthy"); return
    }

    // Bound request body (envelope + 10 MB attachment + slack).
    r.Body = http.MaxBytesReader(w, r.Body, 11*1024*1024)

    var req quickTestReq
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeJSONError(w, 400, "invalid request body: "+err.Error()); return
    }
    if req.MaxTokens <= 0 { req.MaxTokens = 2048 }
    if req.Temperature == 0 { req.Temperature = 0.7 } // only when caller omitted

    if req.Prompt == "" && req.Attachment == nil {
        writeJSONError(w, 400, "prompt or attachment is required"); return
    }

    if req.Attachment != nil && !profileHasMMProj(srv.ProfileSnapshot) {
        writeJSONError(w, 400,
            "Server has no --mmproj projector loaded; attachments require a multimodal profile.")
        return
    }

    // Build content parts.
    parts := []map[string]any{{"type": "text", "text": req.Prompt}}
    if a := req.Attachment; a != nil {
        if err := validateMIME(a.MIME); err != nil {
            writeJSONError(w, 400, "attachment.mime invalid: "+err.Error()); return
        }
        switch a.Kind {
        case "image":
            parts = append(parts, map[string]any{
                "type": "image_url",
                "image_url": map[string]any{
                    "url": "data:" + a.MIME + ";base64," + a.Data,
                },
            })
        case "audio":
            fmtStr, ok := audioFormatFromMIME(a.MIME)
            if !ok { writeJSONError(w, 400, "unsupported audio MIME: "+a.MIME); return }
            parts = append(parts, map[string]any{
                "type": "input_audio",
                "input_audio": map[string]any{
                    "data": a.Data, "format": fmtStr,
                },
            })
        default:
            writeJSONError(w, 400, "attachment.kind must be 'image' or 'audio'"); return
        }
    }

    upstreamBody, _ := json.Marshal(map[string]any{
        "model":       srv.ProfileSnapshot.Name,
        "messages":    []map[string]any{{"role": "user", "content": parts}},
        "max_tokens":  req.MaxTokens,
        "temperature": req.Temperature,
        "stream":      req.Stream,
    })

    host := srv.Host
    if host == "0.0.0.0" || host == "::" || host == "" { host = "127.0.0.1" }
    upURL := fmt.Sprintf("http://%s:%d/v1/chat/completions", host, srv.Port)

    // Log quicktest proxy action to console
    fmt.Printf("[QuickTest] [REQUEST] Server ID: %s | URL: %s | MaxTokens: %d | Temp: %.2f | Stream: %t | HasAttachment: %t\n",
        id, upURL, req.MaxTokens, req.Temperature, req.Stream, req.Attachment != nil)

    upReq, err := http.NewRequestWithContext(r.Context(), "POST", upURL, bytes.NewReader(upstreamBody))
    if err != nil { writeJSONError(w, 500, err.Error()); return }
    upReq.Header.Set("Content-Type", "application/json")

    // No client timeout; rely on context + ResponseHeaderTimeout.
    client := &http.Client{Transport: &http.Transport{
        ResponseHeaderTimeout: 30 * time.Second,
    }}
    resp, err := client.Do(upReq)
    if err != nil { writeJSONError(w, 503, "child server unreachable: "+err.Error()); return }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        preview, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
        writeJSONError(w, 502,
            fmt.Sprintf("upstream %d: %s", resp.StatusCode, strings.TrimSpace(string(preview))))
        return
    }

    if req.Stream {
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("Connection", "keep-alive")
        w.Header().Set("X-Accel-Buffering", "no")
        w.WriteHeader(200)
        flusher, _ := w.(http.Flusher)
        buf := make([]byte, 4096)
        for {
            n, rerr := resp.Body.Read(buf)
            if n > 0 {
                if _, werr := w.Write(buf[:n]); werr != nil { return }
                if flusher != nil { flusher.Flush() }
            }
            if rerr != nil { return }
        }
    }

    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(resp.StatusCode)
    _, _ = io.Copy(w, resp.Body)
}

func profileHasMMProj(p storage.Profile) bool {
    for i, a := range p.Args {
        if a == "--mmproj" && i+1 < len(p.Args) && p.Args[i+1] != "" {
            return true
        }
    }
    return false
}

// validateMIME rejects MIME strings that contain characters outside the safe
// token set defined by RFC 2045 §5.1. This prevents a crafted attachment.mime
// from injecting content into the data-URL string (e.g. via semicolons,
// newlines, or quotes) before it is passed to the upstream llama-server.
//
// Allowed: letters, digits, '/', '.', '+', '-', '_'
// Rejected: whitespace, control chars, quotes, angle brackets, semicolons, …
func validateMIME(m string) error {
    if m == "" {
        return fmt.Errorf("empty MIME")
    }
    if !strings.Contains(m, "/") {
        return fmt.Errorf("missing type/subtype separator")
    }
    for _, c := range m {
        ok := (c >= 'a' && c <= 'z') ||
            (c >= 'A' && c <= 'Z') ||
            (c >= '0' && c <= '9') ||
            c == '/' || c == '.' || c == '+' || c == '-' || c == '_'
        if !ok {
            return fmt.Errorf("character %q is not allowed in a MIME type", c)
        }
    }
    return nil
}

func audioFormatFromMIME(m string) (string, bool) {
    switch strings.ToLower(m) {
    case "audio/wav", "audio/x-wav", "audio/wave": return "wav", true
    case "audio/mpeg", "audio/mp3":                return "mp3", true
    case "audio/flac", "audio/x-flac":             return "flac", true
    case "audio/ogg":                              return "ogg", true
    }
    return "", false
}
