package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"llama-server-studio/internal/config"
	"llama-server-studio/internal/models"
)

func (s *Server) handleListModels(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, maskModels(s.db.ListModels()))
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
	writeJSON(w, http.StatusOK, maskModel(m))
}

func (s *Server) handleHideModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model_id")
	if err := s.db.HideModel(id, true); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleHFRepo(w http.ResponseWriter, r *http.Request) {
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSONError(w, http.StatusBadRequest, "query param 'repo' is required")
		return
	}

	files, err := models.FetchRepoFiles(repoID)
	if err != nil {
		switch {
		case errors.Is(err, models.ErrRepoNotFound):
			writeJSONError(w, http.StatusNotFound, fmt.Sprintf("Hugging Face repo %q not found", repoID))
		case errors.Is(err, models.ErrRepoGated):
			writeJSONError(w, http.StatusUnauthorized, "Authentication required for this repo. Set HF_TOKEN in the environment and restart the studio.")
		case errors.Is(err, models.ErrRepoUpstream):
			writeJSONError(w, http.StatusBadGateway, err.Error())
		default:
			if strings.HasPrefix(err.Error(), "invalid repo id") || strings.HasPrefix(err.Error(), "repo id is required") {
				writeJSONError(w, http.StatusBadRequest, err.Error())
			} else {
				writeJSONError(w, http.StatusInternalServerError, err.Error())
			}
		}
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"repo_id":             repoID,
		"files":               files,
		"hf_token_configured": models.HFTokenConfigured(),
	})
}

func (s *Server) handleHFStartJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Repo  string             `json:"repo"`
		Files []models.RepoFile  `json:"files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.Repo) == "" {
		writeJSONError(w, http.StatusBadRequest, "repo is required")
		return
	}
	if len(req.Files) == 0 {
		writeJSONError(w, http.StatusBadRequest, "at least one file must be selected")
		return
	}

	job, err := models.StartJob(req.Repo, req.Files, s.cfg.HFDownloadRoot(), s.db)
	if err != nil {
		if errors.Is(err, models.ErrJobInProgress) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error":      err.Error(),
				"active_job": job,
			})
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleHFCurrentJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, models.GetActiveJob())
}

func (s *Server) handleHFCancelJob(w http.ResponseWriter, r *http.Request) {
	if !models.CancelJob() {
		writeJSONError(w, http.StatusConflict, "no active job to cancel")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleHFResumable(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, models.ListResumableJobs(s.cfg.HFDownloadRoot()))
}

func (s *Server) handleHFRemoveResumable(w http.ResponseWriter, r *http.Request) {
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSONError(w, http.StatusBadRequest, "query param 'repo' is required")
		return
	}

	err := models.RemoveResumableJob(repoID, s.cfg.HFDownloadRoot())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleDownloadModelLegacy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ModelString string `json:"model_string"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	modelStr := strings.TrimSpace(req.ModelString)
	if modelStr == "" {
		writeJSONError(w, http.StatusBadRequest, "model_string is required")
		return
	}

	parts := strings.SplitN(modelStr, ":", 2)
	repoID := strings.TrimSpace(parts[0])
	quant := "Q4_K_M"
	if len(parts) == 2 {
		quant = strings.TrimSpace(parts[1])
	}

	files, err := models.FetchRepoFiles(repoID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	var chosen *models.RepoFile
	for i, f := range files {
		lower := strings.ToLower(f.Filename)
		if strings.HasSuffix(lower, ".gguf") && strings.Contains(lower, strings.ToLower(quant)) {
			chosen = &files[i]
			break
		}
	}
	if chosen == nil {
		for i, f := range files {
			if strings.HasSuffix(strings.ToLower(f.Filename), ".gguf") {
				chosen = &files[i]
				break
			}
		}
	}
	if chosen == nil {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("no GGUF matching %q in repo %s", quant, repoID))
		return
	}

	job, err := models.StartJob(repoID, []models.RepoFile{*chosen}, s.cfg.HFDownloadRoot(), s.db)
	if err != nil {
		if errors.Is(err, models.ErrJobInProgress) {
			writeJSONError(w, http.StatusConflict, err.Error())
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":        true,
		"file_name": chosen.Filename,
		"job":       job,
	})
}

func (s *Server) handleGetModelDownloadsLegacy(w http.ResponseWriter, r *http.Request) {
	job := models.GetActiveJob()
	if job == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	type legacyEntry struct {
		ModelString string  `json:"model_string"`
		FileName    string  `json:"file_name"`
		Progress    float64 `json:"progress"`
		Status      string  `json:"status"`
		Error       string  `json:"error,omitempty"`
		BytesLoaded int64   `json:"bytes_loaded"`
		BytesTotal  int64   `json:"bytes_total"`
	}
	out := make([]legacyEntry, 0, len(job.Files))
	for _, f := range job.Files {
		out = append(out, legacyEntry{
			ModelString: job.RepoID,
			FileName:    f.Filename,
			Progress:    f.Progress,
			Status:      f.Status,
			Error:       f.Error,
			BytesLoaded: f.BytesLoaded,
			BytesTotal:  f.SizeBytes,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("model_id")
	deleteFile := r.URL.Query().Get("delete_file") == "true"

	m, ok := s.db.GetModel(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "model not found")
		return
	}

	if err := s.db.DeleteModel(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if deleteFile {
		isAllowedDir := false
		resolvedPath, err := filepath.Abs(filepath.Clean(m.Path))
		if err == nil {
			for _, d := range s.cfg.ModelsDirs {
				if absD, err := filepath.Abs(filepath.Clean(d)); err == nil {
					if strings.HasPrefix(resolvedPath, absD) {
						isAllowedDir = true
						break
					}
				}
			}
		}

		if isAllowedDir {
			if err := os.Remove(resolvedPath); err != nil && !os.IsNotExist(err) {
				writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("failed to delete file from disk: %v", err))
				return
			}

			// Clean up any hf downloader metadata (.job.json and .part files) in the same directory
			dir := filepath.Dir(resolvedPath)
			jobJSON := filepath.Join(dir, ".job.json")
			if _, err := os.Stat(jobJSON); err == nil {
				_ = os.Remove(jobJSON)
				
				// Also clean up any lingering .part files in that directory
				if entries, err := os.ReadDir(dir); err == nil {
					for _, ent := range entries {
						if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".part") {
							_ = os.Remove(filepath.Join(dir, ent.Name()))
						}
					}
				}
				// Remove the directory if it's now empty
				_ = os.Remove(dir)
			}
		}
	}

	// Always clear the active job in memory if the deleted model's repo ID matches
	if m.RepoID != "" {
		models.ClearActiveJobIfMatches(m.RepoID)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scanning": models.IsScanning(),
	})
}

func (s *Server) handleGetState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"models":     maskModels(s.db.ListModels()),
		"profiles":   s.db.ListProfiles(),
		"servers":    s.db.ListServers(),
		"benchmarks": s.db.ListBenchmarkRuns(),
		"hf_job":     models.GetActiveJob(),
		"hf_history": s.db.ListDownloadHistory(),
		"scanning":   models.IsScanning(),
	})
}


func (s *Server) handleHFRepoReadme(w http.ResponseWriter, r *http.Request) {
	repoID := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repoID == "" {
		writeJSONError(w, http.StatusBadRequest, "query param 'repo' is required")
		return
	}

	readme, err := models.FetchRepoReadme(repoID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"repo_id": repoID,
		"readme":  readme,
	})
}

// handleGetHFTokenStatus returns whether a token is configured and from which source,
// without ever revealing the token value.
func (s *Server) handleGetHFTokenStatus(w http.ResponseWriter, r *http.Request) {
	envSet := strings.TrimSpace(os.Getenv("HF_TOKEN")) != "" ||
		strings.TrimSpace(os.Getenv("HF_API_TOKEN")) != ""
	configSet := strings.TrimSpace(s.cfg.HFToken) != ""

	var source string
	switch {
	case envSet:
		source = "env" // env var wins; config value is unused while env is set
	case configSet:
		source = "config"
	default:
		source = "none"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured": envSet || configSet,
		"source":     source,
	})
}

// handleSetHFToken saves (or clears) the Hugging Face token in config.json and
// hot-updates the in-memory token used by the downloader. The env var always
// takes priority at request time — this endpoint only affects the config.json value.
func (s *Server) handleSetHFToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"` // empty string = clear
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	tok := strings.TrimSpace(req.Token)

	// Basic sanity: HF tokens start with "hf_" (user/write tokens) or are empty.
	// We allow anything non-empty to be forward-compatible with future token formats.
	s.cfg.HFToken = tok
	if err := config.SaveConfig(s.cfg, s.cfgPath); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to save config: "+err.Error())
		return
	}

	// Hot-update the downloader — no restart required.
	models.SetHFToken(tok)

	source := "config"
	if tok == "" {
		source = "none"
	}
	// If env var is set it overrides config at request time anyway.
	if strings.TrimSpace(os.Getenv("HF_TOKEN")) != "" || strings.TrimSpace(os.Getenv("HF_API_TOKEN")) != "" {
		source = "env"
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":     true,
		"source": source,
	})
}
