package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/process"
	"llama-server-studio/internal/storage"
)

type metricsHistory struct {
	sampledAt             time.Time
	promptTokensTotal     int64
	generationTokensTotal int64
}

type SystemMetrics struct {
	InstancesCPUSum float64 `json:"instances_cpu_sum"`
	InstancesMemSum int64   `json:"instances_mem_sum"`
	UpdatedAt       string  `json:"updated_at"`
}

var (
	metricsCache  = make(map[string]metricsHistory)
	cacheMu       sync.Mutex
	systemMetrics SystemMetrics
	sysMetricsMu  sync.RWMutex
)

func GetSystemMetrics() SystemMetrics {
	sysMetricsMu.RLock()
	defer sysMetricsMu.RUnlock()
	return systemMetrics
}

// StartMonitor launches a background ticker that records resource usage.
func StartMonitor(ctx context.Context, db *storage.DB, s *process.Supervisor) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	client := &http.Client{Timeout: 500 * time.Millisecond}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers := db.ListServers()
			var cpuSum float64
			var memSum int64

			for _, srv := range servers {
				if srv.Status != "healthy" && srv.Status != "starting" {
					continue
				}

				// 1. Scrape process CPU and RSS memory
				cpu, rss, err := s.GetProcessStats(srv.ID)
				if err != nil {
					continue // skip if error or process just exited
				}
				cpuSum += cpu
				memSum += rss

				// 2. Fetch metrics
				metrics := scrapeMetrics(client, srv.Host, srv.Port)

				// 3. Fetch slots
				slotCount, busySlots := scrapeSlots(client, srv.Host, srv.Port)

				// 4. Calculate token throughput rates dynamically via differences
				var promptRate, generationRate float64
				
				promptTokensTotal := int64(metrics["llama_prompt_tokens_total"])
				if promptTokensTotal == 0 {
					promptTokensTotal = int64(metrics["prompt_tokens_total"])
				}
				generationTokensTotal := int64(metrics["llama_tokens_predicted_total"])
				if generationTokensTotal == 0 {
					generationTokensTotal = int64(metrics["llama_generation_tokens_total"])
				}

				now := time.Now()

				cacheMu.Lock()
				prev, found := metricsCache[srv.ID]
				if found {
					durationSec := now.Sub(prev.sampledAt).Seconds()
					if durationSec > 0.1 {
						if promptTokensTotal >= prev.promptTokensTotal {
							promptRate = float64(promptTokensTotal-prev.promptTokensTotal) / durationSec
						}
						if generationTokensTotal >= prev.generationTokensTotal {
							generationRate = float64(generationTokensTotal-prev.generationTokensTotal) / durationSec
						}
					}
				}
				// Save history
				metricsCache[srv.ID] = metricsHistory{
					sampledAt:             now,
					promptTokensTotal:     promptTokensTotal,
					generationTokensTotal: generationTokensTotal,
				}
				cacheMu.Unlock()

				// Calculate observed context size
				var ctxSizeObserved int
				if srv.ProfileSnapshot.Args != nil {
					// Guess or read from metrics KV cache usage ratio if provided
					ratio := metrics["llama_kv_cache_usage_ratio"]
					if ratio > 0 {
						// Multiply by simple ctx limit or simple fallback
						ctxLimit := 8192
						for i := 0; i < len(srv.ProfileSnapshot.Args); i++ {
							if srv.ProfileSnapshot.Args[i] == "-c" && i+1 < len(srv.ProfileSnapshot.Args) {
								if val, err := strconv.Atoi(srv.ProfileSnapshot.Args[i+1]); err == nil {
									ctxLimit = val
								}
							}
						}
						ctxSizeObserved = int(ratio * float64(ctxLimit))
					}
				}

				sample := storage.StatsSample{
					ServerID:                  srv.ID,
					SampledAt:                 now.Format(time.RFC3339),
					CPUPercent:                cpu,
					MemoryRSSBytes:            rss,
					RequestCount:              0, // Incrementally updated via proxy routes
					ErrorCount:                0,
					AvgLatencyMS:              0,
					P50LatencyMS:              0,
					P95LatencyMS:              0,
					TokensPerSecond:           promptRate + generationRate,
					PromptTokensTotal:         promptTokensTotal,
					PromptSecondsTotal:        metrics["llama_prompt_seconds_total"],
					PromptTokensPerSecond:     promptRate,
					GenerationTokensTotal:     generationTokensTotal,
					GenerationSecondsTotal:    metrics["llama_tokens_predicted_seconds_total"],
					GenerationTokensPerSecond: generationRate,
					RequestsProcessing:         int(metrics["llama_requests_active"]),
					RequestsDeferred:           int(metrics["llama_requests_deferred"]),
					SlotCount:                 slotCount,
					BusySlots:                 busySlots,
					CtxSizeObserved:            ctxSizeObserved,
					GPUMemoryUsed:             0, // Mapped where platform API permits
				}

				_ = db.AddStatsSample(sample)
			}

			sysMetricsMu.Lock()
			systemMetrics = SystemMetrics{
				InstancesCPUSum: cpuSum,
				InstancesMemSum: memSum,
				UpdatedAt:       time.Now().Format(time.RFC3339),
			}
			sysMetricsMu.Unlock()
		}
	}
}

func scrapeMetrics(client *http.Client, host string, port int) map[string]float64 {
	metrics := make(map[string]float64)
	url := fmt.Sprintf("http://%s:%d/metrics", host, port)
	
	resp, err := client.Get(url)
	if err != nil {
		return metrics
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return metrics
	}

	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 {
			key := fields[0]
			if idx := strings.Index(key, "{"); idx != -1 {
				key = key[:idx]
			}
			val, err := strconv.ParseFloat(fields[1], 64)
			if err == nil {
				metrics[key] = val
			}
		}
	}

	return metrics
}

func scrapeSlots(client *http.Client, host string, port int) (int, int) {
	url := fmt.Sprintf("http://%s:%d/slots", host, port)
	
	resp, err := client.Get(url)
	if err != nil {
		return 0, 0
	}
	defer resp.Body.Close()

	var slots []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&slots); err != nil {
		return 0, 0
	}

	slotCount := len(slots)
	busySlots := 0

	for _, slot := range slots {
		busy := false
		
		stateVal, hasState := slot["state"]
		if hasState {
			if f, ok := stateVal.(float64); ok && f != 0 {
				busy = true
			}
		}
		if isProc, ok := slot["is_processing"].(bool); ok && isProc {
			busy = true
		}
		// Fallback simple checks
		if slot["prompt"] != nil || slot["n_predict"] != nil {
			busy = true
		}

		if busy {
			busySlots++
		}
	}

	return slotCount, busySlots
}
