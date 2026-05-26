package httpapi

import (
	"encoding/json"
	"net/http"
)

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
