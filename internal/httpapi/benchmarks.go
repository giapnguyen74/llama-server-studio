package httpapi

import (
	"encoding/json"
	"net/http"

	"llama-server-studio/internal/bench"
)

func (s *Server) handleListBenchmarks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.db.ListBenchmarkRuns())
}

func (s *Server) handleListCorpus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, bench.GetWorkloads(s.db.GetDataDir()))
}

func (s *Server) handleRunBenchmark(w http.ResponseWriter, r *http.Request) {
	var reqPayload struct {
		ProfileID       string   `json:"profile_id"`
		Kind            string   `json:"kind"` // "single_shot" | "sweep" | "concurrency"
		SweepFlag       string   `json:"sweep_flag"`
		SweepValues     []string `json:"sweep_values"`
		WorkloadID      string   `json:"workload_id"`
		ConcurrencyPlan []int    `json:"concurrency_plan"`
		Repeats         int      `json:"repeats"`
		Warmups         int      `json:"warmups"`
	}

	if err := json.NewDecoder(r.Body).Decode(&reqPayload); err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	if reqPayload.Repeats <= 0 {
		reqPayload.Repeats = 5
	}
	if reqPayload.Warmups < 0 {
		reqPayload.Warmups = 2
	}

	// Spawning benchmark asynchronously so sweeps don't time out the HTTP request
	go func() {
		_, _ = s.benchRunner.RunBenchmark(
			reqPayload.ProfileID,
			reqPayload.Kind,
			reqPayload.SweepFlag,
			reqPayload.SweepValues,
			reqPayload.WorkloadID,
			reqPayload.ConcurrencyPlan,
			reqPayload.Repeats,
			reqPayload.Warmups,
		)
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

func (s *Server) handleDeleteBenchmark(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("run_id")
	if err := s.db.DeleteBenchmarkRun(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
