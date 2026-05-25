package stats

import (
	"context"
	"time"

	"llama-server-studio/internal/process"
	"llama-server-studio/internal/storage"
)

// StartMonitor launches a background ticker that records resource usage.
func StartMonitor(ctx context.Context, db *storage.DB, s *process.Supervisor) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Fetch all servers
			servers := db.ListServers()
			for _, srv := range servers {
				if srv.Status != "healthy" && srv.Status != "starting" {
					continue
				}

				// Scrape process CPU and RSS memory
				cpu, rss, err := s.GetProcessStats(srv.ID)
				if err != nil {
					continue // skip if error or process just exited
				}

				sample := storage.StatsSample{
					ServerID:        srv.ID,
					SampledAt:       time.Now().Format(time.RFC3339),
					CPUPercent:      cpu,
					MemoryRSSBytes:  rss,
					RequestCount:    0,
					ErrorCount:      0,
					AvgLatencyMS:    0,
					P50LatencyMS:    0,
					P95LatencyMS:    0,
					TokensPerSecond: 0,
				}

				_ = db.AddStatsSample(sample)
			}
		}
	}
}
