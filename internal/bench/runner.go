package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/process"
	"llama-server-studio/internal/storage"
)

type Runner struct {
	db         *storage.DB
	supervisor *process.Supervisor
	mu         sync.Mutex
	running    map[string]bool // profileID -> running benchmark
}

type sseChunk struct {
	Content string `json:"content"`
	Stop    bool   `json:"stop"`
	Timings *struct {
		PredictedN         int     `json:"predicted_n"`
		PredictedMS        float64 `json:"predicted_ms"`
		PredictedPerSecond float64 `json:"predicted_per_second"`
		PromptN            int     `json:"prompt_n"`
		PromptMS           float64 `json:"prompt_ms"`
		PromptPerSecond    float64 `json:"prompt_per_second"`
	} `json:"timings"`
}

func NewRunner(db *storage.DB, s *process.Supervisor) *Runner {
	return &Runner{
		db:         db,
		supervisor: s,
		running:    make(map[string]bool),
	}
}

// RunBenchmark runs a redesigned benchmark suite (single-shot, sweep, grid, or concurrency).
func (br *Runner) RunBenchmark(
	profileID string,
	kind string,
	sweepFlag string,
	sweepValues []string,
	workloadID string,
	concurrencyPlan []int,
	repeats int,
	warmups int,
) (*storage.BenchmarkRun, error) {
	br.mu.Lock()
	if br.running[profileID] {
		br.mu.Unlock()
		return nil, errors.New("a benchmark is already running for this profile")
	}
	br.running[profileID] = true
	br.mu.Unlock()

	log.Printf("[BENCHMARK] Starting %s suite for profile %s (workload: %s, repeats: %d, warmups: %d)", kind, profileID, workloadID, repeats, warmups)
	if kind == "sweep" {
		log.Printf("[BENCHMARK] Sweep config: flag %s, values %v", sweepFlag, sweepValues)
	} else if kind == "concurrency" {
		log.Printf("[BENCHMARK] Concurrency levels: %v", concurrencyPlan)
	}

	defer func() {
		br.mu.Lock()
		delete(br.running, profileID)
		br.mu.Unlock()
	}()

	// 1. Fetch workload
	workloads := GetWorkloads(br.db.GetDataDir())
	var workload Workload
	foundwl := false
	for _, wl := range workloads {
		if wl.ID == workloadID {
			workload = wl
			foundwl = true
			break
		}
	}
	if !foundwl && len(workloads) > 0 {
		workload = workloads[0] // fallback to first
	}

	p, ok := br.db.GetProfile(profileID)
	if !ok {
		log.Printf("[BENCHMARK] Startup aborted: profile %s not found", profileID)
		return nil, errors.New("profile not found")
	}
	if _, ok := br.db.GetModel(p.ModelID); !ok {
		log.Printf("[BENCHMARK] Startup aborted: GGUF model for profile %s not found", profileID)
		return nil, errors.New("associated GGUF model not found")
	}

	runID := fmt.Sprintf("bench_%d", time.Now().UnixNano())
	run := storage.BenchmarkRun{
		ID:              runID,
		ProfileID:       profileID,
		ModelID:         p.ModelID,
		Prompt:          workload.Prompt,
		Params: map[string]interface{}{
			"max_tokens":  workload.MaxTokens,
			"temperature": workload.Temperature,
			"warmups":     warmups,
			"repeats":     repeats,
		},
		ProfileSnapshot: p,
		StartedAt:       time.Now().Format(time.RFC3339),
		Status:          "running",
		Kind:            kind,
		SweepFlag:       sweepFlag,
		SweepValues:     sweepValues,
		WorkloadID:      workload.ID,
		ConcurrencyPlan: concurrencyPlan,
		Cells:           []storage.BenchmarkCell{},
	}
	_ = br.db.SaveBenchmarkRun(run)

	// Save original server details to restore later if running
	origSrv, origActive := br.db.GetServerByProfile(profileID)
	if origActive {
		// Stop active server so it doesn't conflict with our overrides or ports
		log.Printf("[BENCHMARK] Stopping active user server %s to avoid port collision", origSrv.ID)
		_ = br.supervisor.StopServer(origSrv.ID)
	}

	// Helper to restore server
	defer func() {
		if origActive {
			log.Printf("[BENCHMARK] Restoring original server for profile %s", profileID)
			_, _ = br.supervisor.StartServer(profileID, br.supervisor.GetLlamaServerBin(), 41000, 41999)
		}
	}()

	var cells []storage.BenchmarkCell
	var runErr error

	if kind == "single_shot" {
		cell, err := br.executeCell(profileID, "Single-Shot", nil, workload, warmups, repeats)
		if err != nil {
			runErr = err
		} else {
			cells = append(cells, cell)
		}
	} else if kind == "sweep" {
		for _, val := range sweepValues {
			label := fmt.Sprintf("%s = %s", sweepFlag, val)
			overrides := map[string]string{sweepFlag: val}
			cell, err := br.executeCell(profileID, label, overrides, workload, warmups, repeats)
			if err != nil {
				runErr = err
				break
			}
			cells = append(cells, cell)
		}
	} else if kind == "concurrency" {
		// Concurrency load testing
		for _, cLevel := range concurrencyPlan {
			label := fmt.Sprintf("Concurrency: %d", cLevel)
			cell, err := br.executeConcurrencyCell(profileID, label, cLevel, workload, warmups)
			if err != nil {
				runErr = err
				break
			}
			cells = append(cells, cell)
		}
	}

	run.CompletedAt = time.Now().Format(time.RFC3339)
	if runErr != nil {
		run.Status = "failed"
		run.Error = runErr.Error()
		log.Printf("[BENCHMARK] Suite %s FAILED: %v", runID, runErr)
	} else {
		run.Status = "completed"
		run.Cells = cells
		run.Recommendation = br.generateRecommendation(cells)
		log.Printf("[BENCHMARK] Suite %s COMPLETED successfully. Recommendation: %s", runID, run.Recommendation)
	}

	_ = br.db.SaveBenchmarkRun(run)
	return &run, nil
}

func (br *Runner) executeCell(
	profileID string,
	label string,
	overrides map[string]string,
	workload Workload,
	warmups int,
	repeats int,
) (storage.BenchmarkCell, error) {
	log.Printf("[BENCHMARK] Executing variant cell %q", label)
	cell := storage.BenchmarkCell{
		Label:         label,
		FlagOverrides: overrides,
		StartedAt:     time.Now().Format(time.RFC3339),
		Status:        "running",
		Samples:       []storage.BenchmarkSample{},
	}

	// 1. Spawn temporary server with overrides
	srvID, err := br.supervisor.StartServerWithOverrides(profileID, br.supervisor.GetLlamaServerBin(), 41000, 41999, overrides)
	if err != nil {
		log.Printf("[BENCHMARK] Failed to start variant server for %q: %v", label, err)
		return cell, fmt.Errorf("failed to start sweep server: %w", err)
	}
	defer func() {
		_ = br.supervisor.StopServer(srvID)
	}()

	// Wait for healthy state
	var srv storage.Server
	healthy := false
	for i := 0; i < 80; i++ {
		time.Sleep(250 * time.Millisecond)
		if s, ok := br.db.GetServer(srvID); ok && s.Status == "healthy" {
			srv = s
			healthy = true
			break
		}
	}
	if !healthy {
		log.Printf("[BENCHMARK] Sweep server %s failed to become healthy within 20s", srvID)
		return cell, errors.New("sweep server failed to become healthy within 20s")
	}

	client := &http.Client{Timeout: 120 * time.Second}
	completionURL := fmt.Sprintf("http://%s:%d/completion", srv.Host, srv.Port)

	// Discard first cold-start request entirely
	br.streamRequest(client, completionURL, workload, 5)

	// Run warmups
	for i := 0; i < warmups; i++ {
		br.streamRequest(client, completionURL, workload, 5)
	}

	// Run measurement repetitions
	var samples []storage.BenchmarkSample
	for i := 0; i < repeats; i++ {
		sample := br.streamRequest(client, completionURL, workload, workload.MaxTokens)
		sample.RequestIndex = i + 1
		samples = append(samples, sample)
	}

	cell.CompletedAt = time.Now().Format(time.RFC3339)
	cell.Status = "completed"
	cell.Samples = samples
	cell.Aggregates = br.computeAggregates(samples)

	// Noise gate check
	if cell.Aggregates.TGSpeedStdDev > 0 && cell.Aggregates.TGSpeedMean > 0 {
		if (cell.Aggregates.TGSpeedStdDev / cell.Aggregates.TGSpeedMean) > 0.20 {
			cell.Noisy = true
		}
	}

	log.Printf("[BENCHMARK] Variant cell %q completed. Mean TG speed: %.2f t/s, Error rate: %.1f%%", label, cell.Aggregates.TGSpeedMean, cell.Aggregates.ErrorRatePercent)
	return cell, nil
}

func (br *Runner) executeConcurrencyCell(
	profileID string,
	label string,
	concurrency int,
	workload Workload,
	warmups int,
) (storage.BenchmarkCell, error) {
	log.Printf("[BENCHMARK] Executing concurrency cell %q (level: %d)", label, concurrency)
	cell := storage.BenchmarkCell{
		Label:         label,
		FlagOverrides: nil,
		StartedAt:     time.Now().Format(time.RFC3339),
		Status:        "running",
		Samples:       []storage.BenchmarkSample{},
	}

	srvID, err := br.supervisor.StartServer(profileID, br.supervisor.GetLlamaServerBin(), 41000, 41999)
	if err != nil {
		log.Printf("[BENCHMARK] Failed to start concurrency server for %q: %v", label, err)
		return cell, fmt.Errorf("failed to start concurrency server: %w", err)
	}
	defer func() {
		_ = br.supervisor.StopServer(srvID)
	}()

	var srv storage.Server
	healthy := false
	for i := 0; i < 80; i++ {
		time.Sleep(250 * time.Millisecond)
		if s, ok := br.db.GetServer(srvID); ok && s.Status == "healthy" {
			srv = s
			healthy = true
			break
		}
	}
	if !healthy {
		log.Printf("[BENCHMARK] Concurrency server %s failed to become healthy within 20s", srvID)
		return cell, errors.New("concurrency server failed to become healthy within 20s")
	}

	client := &http.Client{Timeout: 60 * time.Second}
	completionURL := fmt.Sprintf("http://%s:%d/completion", srv.Host, srv.Port)

	// Warmup
	for i := 0; i < warmups; i++ {
		br.streamRequest(client, completionURL, workload, 5)
	}

	// Concurrency load execution
	var wg sync.WaitGroup
	var mu sync.Mutex
	var samples []storage.BenchmarkSample

	// Run concurrent requests in parallel
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			s := br.streamRequest(client, completionURL, workload, workload.MaxTokens)
			s.RequestIndex = idx + 1
			mu.Lock()
			samples = append(samples, s)
			mu.Unlock()
		}(i)
	}
	wg.Wait()

	cell.CompletedAt = time.Now().Format(time.RFC3339)
	cell.Status = "completed"
	cell.Samples = samples
	cell.Aggregates = br.computeAggregates(samples)

	// Calculate concurrent throughput
	var totalGeneratedTokens float64
	var maxDuration float64
	for _, s := range samples {
		if s.Error == "" {
			totalGeneratedTokens += float64(s.OutputTokens)
			if s.EndToEndMS > maxDuration {
				maxDuration = s.EndToEndMS
			}
		}
	}
	if maxDuration > 0 {
		cell.Aggregates.ThroughputTGS = (totalGeneratedTokens / (maxDuration / 1000.0))
	}

	log.Printf("[BENCHMARK] Concurrency cell %q completed. Throughput: %.2f t/s, Error rate: %.1f%%", label, cell.Aggregates.ThroughputTGS, cell.Aggregates.ErrorRatePercent)
	return cell, nil
}

func (br *Runner) streamRequest(
	client *http.Client,
	url string,
	workload Workload,
	maxTokens int,
) storage.BenchmarkSample {
	sample := storage.BenchmarkSample{
		ITLMS: []float64{},
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"prompt":    workload.Prompt,
		"n_predict": maxTokens,
		"temp":      workload.Temperature,
		"stream":    true,
	})

	reqStart := time.Now()
	req, _ := http.NewRequest("POST", url, bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		sample.Error = err.Error()
		sample.HTTPStatus = 500
		return sample
	}
	defer resp.Body.Close()

	sample.HTTPStatus = resp.StatusCode
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		sample.Error = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
		return sample
	}

	reader := bufio.NewReader(resp.Body)
	var firstTokenReceived bool
	var lastTokenTime time.Time

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		dataJSON := strings.TrimPrefix(line, "data: ")

		var chunk sseChunk
		if err := json.Unmarshal([]byte(dataJSON), &chunk); err == nil {
			if chunk.Content != "" && !firstTokenReceived {
				firstTokenReceived = true
				sample.TTFTMS = float64(time.Since(reqStart).Milliseconds())
				lastTokenTime = time.Now()
			} else if chunk.Content != "" && firstTokenReceived {
				sample.ITLMS = append(sample.ITLMS, float64(time.Since(lastTokenTime).Milliseconds()))
				lastTokenTime = time.Now()
			}

			if chunk.Timings != nil {
				sample.PromptTokens = chunk.Timings.PromptN
				sample.OutputTokens = chunk.Timings.PredictedN
				sample.PromptPerSec = chunk.Timings.PromptPerSecond
				sample.PredictedPerSec = chunk.Timings.PredictedPerSecond
			}
			if chunk.Stop {
				break
			}
		}
	}

	sample.EndToEndMS = float64(time.Since(reqStart).Milliseconds())

	// Cap ITL elements to prevent JSON storage bloot
	if len(sample.ITLMS) > 100 {
		sample.ITLMS = append(sample.ITLMS[:50], sample.ITLMS[len(sample.ITLMS)-50:]...)
	}

	return sample
}

func (br *Runner) computeAggregates(samples []storage.BenchmarkSample) storage.BenchmarkAggregates {
	var tgSpeeds, ppSpeeds, ttfts, e2es, itls []float64
	var totalTG float64
	var errCount int

	for _, s := range samples {
		if s.Error != "" || s.HTTPStatus != http.StatusOK {
			errCount++
			continue
		}
		tgSpeeds = append(tgSpeeds, s.PredictedPerSec)
		ppSpeeds = append(ppSpeeds, s.PromptPerSec)
		ttfts = append(ttfts, s.TTFTMS)
		e2es = append(e2es, s.EndToEndMS)
		totalTG += float64(s.OutputTokens)
		itls = append(itls, s.ITLMS...)
	}

	n := float64(len(samples))
	var errorRate float64
	if n > 0 {
		errorRate = (float64(errCount) / n) * 100
	}

	sort.Float64s(tgSpeeds)
	sort.Float64s(ppSpeeds)
	sort.Float64s(ttfts)
	sort.Float64s(e2es)
	sort.Float64s(itls)

	mean := func(arr []float64) float64 {
		if len(arr) == 0 {
			return 0
		}
		var sum float64
		for _, v := range arr {
			sum += v
		}
		return sum / float64(len(arr))
	}

	stddev := func(arr []float64, avg float64) float64 {
		if len(arr) <= 1 {
			return 0
		}
		var sum float64
		for _, v := range arr {
			sum += (v - avg) * (v - avg)
		}
		return math.Sqrt(sum / float64(len(arr)-1))
	}

	p50 := func(arr []float64) float64 {
		if len(arr) == 0 {
			return 0
		}
		return arr[len(arr)/2]
	}

	p90 := func(arr []float64) float64 {
		if len(arr) == 0 {
			return 0
		}
		idx := int(float64(len(arr)) * 0.9)
		if idx >= len(arr) {
			idx = len(arr) - 1
		}
		return arr[idx]
	}

	p95 := func(arr []float64) float64 {
		if len(arr) == 0 {
			return 0
		}
		idx := int(float64(len(arr)) * 0.95)
		if idx >= len(arr) {
			idx = len(arr) - 1
		}
		return arr[idx]
	}

	tgMean := mean(tgSpeeds)
	return storage.BenchmarkAggregates{
		TGSpeedMean:      tgMean,
		TGSpeedP50:       p50(tgSpeeds),
		TGSpeedP90:       p90(tgSpeeds),
		TGSpeedP99:       p95(tgSpeeds), // cap standard
		TGSpeedStdDev:    stddev(tgSpeeds, tgMean),
		PPSpeedMean:      mean(ppSpeeds),
		PPSpeedP50:       p50(ppSpeeds),
		TTFTP50:          p50(ttfts),
		TTFTP95:          p95(ttfts),
		ITLP50:           p50(itls),
		ITLP95:           p95(itls),
		E2EP50:           p50(e2es),
		E2EP95:           p95(e2es),
		ErrorRatePercent: errorRate,
	}
}

func (br *Runner) generateRecommendation(cells []storage.BenchmarkCell) string {
	if len(cells) == 0 {
		return "No completed test variants to recommend."
	}

	var bestCell *storage.BenchmarkCell
	maxSpeed := -1.0

	for i := range cells {
		c := &cells[i]
		if c.Status == "completed" && c.Aggregates.TGSpeedMean > maxSpeed {
			maxSpeed = c.Aggregates.TGSpeedMean
			bestCell = c
		}
	}

	if bestCell == nil {
		return "Failed to evaluate any successful variants."
	}

	return fmt.Sprintf("Recommended settings: %s. Reached peak output throughput of %.1f tokens/s (warmup: done, stddev: %.2f). Use the Apply button to update your profile.",
		bestCell.Label, bestCell.Aggregates.TGSpeedMean, bestCell.Aggregates.TGSpeedStdDev)
}
