package bench

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"llama-server-studio/internal/process"
	"llama-server-studio/internal/storage"
)

type Runner struct {
	db         *storage.DB
	supervisor *process.Supervisor
	mu         sync.Mutex
	running    map[string]bool // tracks profileID -> running benchmark
}

type llamaCompletionTiming struct {
	PredictedN         int     `json:"predicted_n"`
	PredictedMS        float64 `json:"predicted_ms"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
	PromptN            int     `json:"prompt_n"`
	PromptMS           float64 `json:"prompt_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
}

type llamaCompletionResponse struct {
	Content string                `json:"content"`
	Timings llamaCompletionTiming `json:"timings"`
}

func NewRunner(db *storage.DB, s *process.Supervisor) *Runner {
	return &Runner{
		db:         db,
		supervisor: s,
		running:    make(map[string]bool),
	}
}

func (br *Runner) RunBenchmark(
	profileID string,
	prompt string,
	maxTokens int,
	temperature float64,
	repeatCount int,
	warmupCount int,
) (*storage.BenchmarkRun, error) {
	br.mu.Lock()
	if br.running[profileID] {
		br.mu.Unlock()
		return nil, errors.New("a benchmark is already running for this profile")
	}
	br.running[profileID] = true
	br.mu.Unlock()

	defer func() {
		br.mu.Lock()
		delete(br.running, profileID)
		br.mu.Unlock()
	}()

	// 1. Fetch profile and model
	p, ok := br.db.GetProfile(profileID)
	if !ok {
		return nil, errors.New("profile not found")
	}
	m, ok := br.db.GetModel(p.ModelID)
	if !ok {
		return nil, errors.New("associated model not found")
	}

	// 2. Resolve server. If stopped, start it
	srv, active := br.db.GetServerByProfile(profileID)
	startedByBench := false
	if !active || (srv.Status != "healthy" && srv.Status != "starting") {
		// Load a temporary supervisor run
		srvID, err := br.supervisor.StartServer(profileID, "", 41000, 41999)
		if err != nil {
			return nil, fmt.Errorf("failed to start stopped server for benchmark: %w", err)
		}
		startedByBench = true

		// Wait for server to become healthy (poll up to 30s)
		for i := 0; i < 120; i++ {
			time.Sleep(250 * time.Millisecond)
			if s, ok := br.db.GetServer(srvID); ok && s.Status == "healthy" {
				srv = s
				break
			}
		}
		
		if srv.Status != "healthy" {
			_ = br.supervisor.StopServer(srvID)
			return nil, errors.New("server failed to start and become healthy for benchmark within 30 seconds")
		}
	}

	// Shut down server on completion if we started it
	defer func() {
		if startedByBench {
			_ = br.supervisor.StopServer(srv.ID)
		}
	}()

	// 3. Create BenchmarkRun record
	runID := fmt.Sprintf("bench_%d", time.Now().UnixNano())
	params := map[string]interface{}{
		"max_tokens":  maxTokens,
		"temperature": temperature,
		"repeat":      repeatCount,
		"warmup":      warmupCount,
	}

	run := storage.BenchmarkRun{
		ID:              runID,
		ProfileID:       p.ID,
		ServerID:        srv.ID,
		ModelID:         m.ID,
		Prompt:          prompt,
		Params:          params,
		ProfileSnapshot: p,
		StartedAt:       time.Now().Format(time.RFC3339),
		Status:          "running",
	}
	_ = br.db.SaveBenchmarkRun(run)

	client := &http.Client{Timeout: 120 * time.Second}
	completionURL := fmt.Sprintf("http://%s:%d/completion", srv.Host, srv.Port)

	// Helper to write to logs
	writeServerLog := func(msg string) {
		logPath := br.db.GetLogFilePath(srv.ID)
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(fmt.Sprintf("[BENCHMARK] %s\n", msg))
			_ = f.Close()
		}
	}

	writeServerLog(fmt.Sprintf("Starting benchmark run %s on server PID %d", runID, srv.PID))

	// 4. Run Warmup
	if warmupCount > 0 {
		writeServerLog(fmt.Sprintf("Executing %d warmup iterations...", warmupCount))
		for i := 0; i < warmupCount; i++ {
			body, _ := json.Marshal(map[string]interface{}{
				"prompt":    prompt,
				"n_predict": 5, // small predicting tokens for fast warmup
				"temp":      temperature,
			})
			
			req, _ := http.NewRequest("POST", completionURL, bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			
			resp, err := client.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
			}
		}
	}

	// 5. Active repetitions
	writeServerLog(fmt.Sprintf("Executing %d benchmark repetitions (tokens limit: %d)...", repeatCount, maxTokens))
	
	var totalTokens int
	var totalGenerationTimeMS float64
	var tokensPerSecondSum float64
	var latencySum float64
	var errorCount int
	var completions []string

	for i := 0; i < repeatCount; i++ {
		body, _ := json.Marshal(map[string]interface{}{
			"prompt":    prompt,
			"n_predict": maxTokens,
			"temp":      temperature,
		})

		reqStart := time.Now()
		req, _ := http.NewRequest("POST", completionURL, bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			errorCount++
			writeServerLog(fmt.Sprintf("Repetition %d failed: %s", i+1, err.Error()))
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK || readErr != nil {
			errorCount++
			writeServerLog(fmt.Sprintf("Repetition %d failed with status %d", i+1, resp.StatusCode))
			continue
		}

		duration := time.Since(reqStart)

		var lResp llamaCompletionResponse
		if err := json.Unmarshal(respBody, &lResp); err != nil {
			// fallback if timings format differs
			errorCount++
			writeServerLog(fmt.Sprintf("Repetition %d returned malformed timings structure: %s", i+1, err.Error()))
			continue
		}

		// Collect statistics
		completions = append(completions, lResp.Content)
		
		tps := lResp.Timings.PredictedPerSecond
		if tps == 0 && lResp.Timings.PredictedMS > 0 {
			// Manual math fallback
			tps = float64(lResp.Timings.PredictedN) / (lResp.Timings.PredictedMS / 1000.0)
		}

		totalTokens += lResp.Timings.PredictedN
		totalGenerationTimeMS += lResp.Timings.PredictedMS
		tokensPerSecondSum += tps
		latencySum += float64(duration.Milliseconds())

		writeServerLog(fmt.Sprintf("Repetition %d: %d tokens, %.2f tokens/sec, latency: %s", i+1, lResp.Timings.PredictedN, tps, duration))
	}

	// 6. Complete results
	run.CompletedAt = time.Now().Format(time.RFC3339)
	
	if errorCount == repeatCount {
		run.Status = "failed"
		run.Error = "All repetitions failed during benchmark execution."
	} else {
		run.Status = "completed"
		successCount := float64(repeatCount - errorCount)
		
		avgTPS := tokensPerSecondSum / successCount
		avgLatency := latencySum / successCount

		result := map[string]interface{}{
			"tokens_generated":   totalTokens,
			"avg_tokens_per_sec": avgTPS,
			"avg_latency_ms":     avgLatency,
			"success_rate":       (successCount / float64(repeatCount)) * 100,
			"error_count":        errorCount,
			"completions":        completions,
		}

		run.Result = result
		writeServerLog(fmt.Sprintf("Benchmark completed successfully! Average: %.2f tokens/sec, Latency: %.0fms", avgTPS, avgLatency))
	}

	_ = br.db.SaveBenchmarkRun(run)
	return &run, nil
}
