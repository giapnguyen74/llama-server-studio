package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

// Model represents a local GGUF file discovered by the studio.
type Model struct {
	ID              string                 `json:"id"`
	Path            string                 `json:"path"`
	ResolvedPath    string                 `json:"resolved_path"`
	DisplayName     string                 `json:"display_name"`
	Source          string                 `json:"source"` // local_dir, huggingface_cache, manual
	RepoID          string                 `json:"repo_id,omitempty"`
	SizeBytes       int64                  `json:"size_bytes"`
	ModifiedAt      string                 `json:"modified_at"`
	Architecture    string                 `json:"architecture,omitempty"`
	Quantization    string                 `json:"quantization,omitempty"`
	ContextLength   int                    `json:"context_length,omitempty"`
	EmbeddingLength int                    `json:"embedding_length,omitempty"`
	BlockCount      int                    `json:"block_count,omitempty"`
	TokenizerModel  string                 `json:"tokenizer_model,omitempty"`
	ChatTemplate    string                 `json:"chat_template,omitempty"`
	Capabilities    []string               `json:"capabilities"`
	MetadataRaw     map[string]interface{} `json:"metadata_raw,omitempty"`
	Hidden          bool                   `json:"hidden"`
	ScannedAt       string                 `json:"scanned_at"`
	MMProjCandidates []string              `json:"mmproj_candidates,omitempty"`
	MMProjSizesBytes []int64                `json:"mmproj_sizes_bytes,omitempty"`
}

// ProfileRouting holds proxy configurations for profile-specific routing gateways.
type ProfileRouting struct {
	Enabled               bool   `json:"enabled"`
	AutoStart             bool   `json:"autoStart"`
	PrimaryInstancePolicy string `json:"primaryInstancePolicy"`
	PublicPath            string `json:"publicPath"`
}

// Profile represents saved llama-server configuration.
type Profile struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Description        string          `json:"description,omitempty"`
	ModelID            string          `json:"model_id"`
	Args               []string        `json:"args"`
	Env                map[string]string `json:"env,omitempty"`
	WorkingDir         string          `json:"working_dir,omitempty"`
	DefaultHost        string          `json:"default_host"`
	DefaultPortPolicy  string          `json:"default_port_policy"` // auto, fixed
	FixedPort          int             `json:"fixed_port,omitempty"`
	Routing            *ProfileRouting `json:"routing,omitempty"`
	Tags               []string        `json:"tags,omitempty"`
	CreatedAt          string          `json:"created_at"`
	UpdatedAt          string          `json:"updated_at"`
}

// ServerHealth holds the last check timestamp and state.
type ServerHealth struct {
	LastCheckAt string `json:"lastCheckAt"`
	State       string `json:"state"`
}

// Server represents a managed llama-server child process.
type Server struct {
	ID              string       `json:"id"`
	InstanceID      string       `json:"instanceId"` // Alias for spec compatibility
	ProfileID       string       `json:"profile_id"`
	ProfileIDSpec   string       `json:"profileId"`  // Alias for spec compatibility
	ModelID         string       `json:"model_id"`
	PID             int          `json:"pid"`
	Host            string       `json:"host"`
	Port            int          `json:"port"`
	BaseURL         string       `json:"baseUrl"` // Alias for spec compatibility
	Status          string       `json:"status"` // stopped, starting, healthy, unhealthy, stopping, crashed, unknown
	StartedAt       string       `json:"started_at,omitempty"`
	StartedAtSpec   string       `json:"startedAt,omitempty"` // Alias for spec compatibility
	StoppedAt       string       `json:"stopped_at,omitempty"`
	ExitCode        int          `json:"exit_code"`
	LastError       string       `json:"last_error,omitempty"`
	ProfileSnapshot Profile      `json:"profile_snapshot"`
	Argv            []string     `json:"argv"`
	LogPath         string       `json:"logPath"`
	Health          ServerHealth `json:"health"`
	CreatedAt       string       `json:"created_at"`
	UpdatedAt       string       `json:"updated_at"`
}

// StatsSample holds process and proxy metrics.
type StatsSample struct {
	ID                        int       `json:"id"`
	ServerID                  string    `json:"server_id"`
	SampledAt                 string    `json:"sampled_at"`
	CPUPercent                float64   `json:"cpu_percent"`
	MemoryRSSBytes            int64     `json:"memory_rss_bytes"`
	RequestCount              int       `json:"request_count"`
	ErrorCount                int       `json:"error_count"`
	AvgLatencyMS              float64   `json:"avg_latency_ms"`
	P50LatencyMS              float64   `json:"p50_latency_ms"`
	P95LatencyMS              float64   `json:"p95_latency_ms"`
	TokensPerSecond           float64   `json:"tokens_per_second"`
	PromptTokensTotal         int64     `json:"prompt_tokens_total"`
	PromptSecondsTotal        float64   `json:"prompt_seconds_total"`
	PromptTokensPerSecond     float64   `json:"prompt_tokens_per_second"`
	GenerationTokensTotal     int64     `json:"generation_tokens_total"`
	GenerationSecondsTotal    float64   `json:"generation_seconds_total"`
	GenerationTokensPerSecond float64   `json:"generation_tokens_per_second"`
	RequestsProcessing        int       `json:"requests_processing"`
	RequestsDeferred          int       `json:"requests_deferred"`
	SlotCount                 int       `json:"slot_count"`
	BusySlots                 int       `json:"busy_slots"`
	CtxSizeObserved           int       `json:"ctx_size_observed"`
	GPUMemoryUsed             int64     `json:"gpu_memory_used"`
}

// BenchmarkRun holds prompt completion and throughput results.
type BenchmarkRun struct {
	ID              string                 `json:"id"`
	ProfileID       string                 `json:"profile_id"`
	ServerID        string                 `json:"server_id,omitempty"`
	ModelID         string                 `json:"model_id"`
	Prompt          string                 `json:"prompt"`
	Params          map[string]interface{} `json:"params"`
	Result          map[string]interface{} `json:"result,omitempty"`
	ProfileSnapshot Profile                `json:"profile_snapshot"`
	StartedAt       string                 `json:"started_at"`
	CompletedAt     string                 `json:"completed_at,omitempty"`
	Status          string                 `json:"status"` // running, completed, failed
	Error           string                 `json:"error,omitempty"`

	// Redesigned Benchmark extensions
	Kind            string                 `json:"kind,omitempty"`            // "single_shot" | "sweep" | "grid" | "concurrency" | "ab"
	SweepFlag       string                 `json:"sweep_flag,omitempty"`      // e.g. "-b"
	SweepValues     []string               `json:"sweep_values,omitempty"`    // values swept
	WorkloadID      string                 `json:"workload_id,omitempty"`     // corpus ID
	ConcurrencyPlan []int                  `json:"concurrency_plan,omitempty"`
	Cells           []BenchmarkCell        `json:"cells,omitempty"`           // one per variant
	Recommendation  string                 `json:"recommendation,omitempty"`  // human-readable text
}

type BenchmarkCell struct {
	Label          string              `json:"label"`
	FlagOverrides  map[string]string   `json:"flag_overrides"`
	StartedAt      string              `json:"started_at"`
	CompletedAt    string              `json:"completed_at"`
	Status         string              `json:"status"`
	Samples        []BenchmarkSample   `json:"samples"`
	Aggregates     BenchmarkAggregates `json:"aggregates"`
	PeakVRAMBytes  int64               `json:"peak_vram_bytes"`
	IdleCPUPercent float64             `json:"idle_cpu_percent"`
	Noisy          bool                `json:"noisy"`
}

type BenchmarkSample struct {
	RequestIndex    int       `json:"request_index"`
	PromptTokens    int       `json:"prompt_tokens"`
	OutputTokens    int       `json:"output_tokens"`
	TTFTMS          float64   `json:"ttft_ms"`
	EndToEndMS      float64   `json:"end_to_end_ms"`
	ITLMS           []float64 `json:"itl_ms"` // inter-token latencies
	PromptPerSec    float64   `json:"prompt_per_sec"`
	PredictedPerSec float64   `json:"predicted_per_sec"`
	HTTPStatus      int       `json:"http_status"`
	Error           string    `json:"error,omitempty"`
}

type BenchmarkAggregates struct {
	TGSpeedMean      float64 `json:"tg_speed_mean"`
	TGSpeedP50       float64 `json:"tg_speed_p50"`
	TGSpeedP90       float64 `json:"tg_speed_p90"`
	TGSpeedP99       float64 `json:"tg_speed_p99"`
	TGSpeedStdDev    float64 `json:"tg_speed_std_dev"`
	PPSpeedMean      float64 `json:"pp_speed_mean"`
	PPSpeedP50       float64 `json:"pp_speed_p50"`
	TTFTP50          float64 `json:"ttft_p50"`
	TTFTP95          float64 `json:"ttft_p95"`
	ITLP50           float64 `json:"itl_p50"`
	ITLP95           float64 `json:"itl_p95"`
	E2EP50           float64 `json:"e2e_p50"`
	E2EP95           float64 `json:"e2e_p95"`
	ThroughputTGS    float64 `json:"throughput_tgs"`
	ErrorRatePercent float64 `json:"error_rate_percent"`
}

// DownloadJobHistory represents a completed or terminated HF download job record.
type DownloadJobHistory struct {
	ID         string `json:"id"`
	RepoID     string `json:"repo_id"`
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	FilesCount int    `json:"files_count"`
	TotalBytes int64  `json:"total_bytes"`
}

// DB represents our thread-safe JSON file-based database store.
type DB struct {
	mu              sync.RWMutex
	dataDir         string
	models          map[string]Model
	profiles        map[string]Profile
	servers         map[string]Server
	stats           []StatsSample
	benchmarks      map[string]BenchmarkRun
	downloadHistory []DownloadJobHistory
	nextSampleID    int
}

func (db *DB) GetDataDir() string {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.dataDir
}

// Open initializes and loads the JSON database from dataDir.
func Open(dataDir string) (*DB, error) {
	// Ensure directories exist
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "logs"), 0700); err != nil {
		return nil, err
	}

	db := &DB{
		dataDir:         dataDir,
		models:          make(map[string]Model),
		profiles:        make(map[string]Profile),
		servers:         make(map[string]Server),
		stats:           make([]StatsSample, 0),
		benchmarks:      make(map[string]BenchmarkRun),
		downloadHistory: make([]DownloadJobHistory, 0),
	}


	// Load files, ignoring NotExist errors (empty DB on first run)
	if err := db.load("models.json", &db.models); err != nil {
		return nil, err
	}
	if err := db.load("profiles.json", &db.profiles); err != nil {
		return nil, err
	}
	if err := db.load("servers.json", &db.servers); err != nil {
		return nil, err
	}
	// stats are intentionally not loaded from disk — they are ephemeral
	if err := db.load("benchmarks.json", &db.benchmarks); err != nil {
		return nil, err
	}
	if err := db.load("download_history.json", &db.downloadHistory); err != nil {
		return nil, err
	}


	// Sanity clean-up: delete stopped/crashed records or dead orphaned servers.
	dirty := false
	for id, s := range db.servers {
		// 1. Purge servers belonging to profiles that no longer exist
		if _, exists := db.profiles[s.ProfileID]; !exists {
			delete(db.servers, id)
			dirty = true
			continue
		}

		// 2. Purge dead or stopped/crashed server processes
		if s.Status == "stopped" || s.Status == "crashed" {
			delete(db.servers, id)
			dirty = true
		} else {
			if s.PID > 0 && processAlive(s.PID) {
				// Process is alive and running; keep it for reattaching
				continue
			}
			// Process is dead; clean it up
			delete(db.servers, id)
			dirty = true
		}
	}
	if dirty {
		_ = db.save("servers.json", db.servers)
	}

	return db, nil
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// Helpers for atomic JSON save/load

func (db *DB) load(filename string, dest interface{}) error {
	path := filepath.Join(db.dataDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, dest)
}

func (db *DB) save(filename string, src interface{}) error {
	path := filepath.Join(db.dataDir, filename)
	tmpPath := path + ".tmp"

	data, err := json.MarshalIndent(src, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}

	// Atomic replace
	return os.Rename(tmpPath, path)
}

// --- Models CRUD ---

func (db *DB) SaveModel(m Model) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.models[m.ID] = m
	return db.save("models.json", db.models)
}

func (db *DB) GetModel(id string) (Model, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	m, ok := db.models[id]
	return m, ok
}

func (db *DB) GetModelByPath(path string) (Model, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, m := range db.models {
		if m.Path == path || m.ResolvedPath == path {
			return m, true
		}
	}
	return Model{}, false
}

func (db *DB) ListModels() []Model {
	db.mu.RLock()
	defer db.mu.RUnlock()

	list := make([]Model, 0, len(db.models))
	for _, m := range db.models {
		list = append(list, m)
	}
	return list
}

func (db *DB) HideModel(id string, hidden bool) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	m, ok := db.models[id]
	if !ok {
		return errors.New("model not found")
	}
	m.Hidden = hidden
	db.models[id] = m
	return db.save("models.json", db.models)
}

func (db *DB) DeleteModel(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, ok := db.models[id]; !ok {
		return errors.New("model not found")
	}
	delete(db.models, id)
	return db.save("models.json", db.models)
}


func (db *DB) ClearScannedModels() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for id, m := range db.models {
		if m.Source == "local_dir" || m.Source == "huggingface_cache" {
			delete(db.models, id)
		}
	}
	return db.save("models.json", db.models)
}

func (db *DB) ReplaceScannedModels(newModels []Model) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	for id, m := range db.models {
		if m.Source == "local_dir" || m.Source == "huggingface_cache" {
			delete(db.models, id)
		}
	}

	for _, m := range newModels {
		db.models[m.ID] = m
	}

	return db.save("models.json", db.models)
}


// --- Profiles CRUD ---

func (db *DB) SaveProfile(p Profile) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.profiles[p.ID] = p
	return db.save("profiles.json", db.profiles)
}

func (db *DB) GetProfile(id string) (Profile, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	p, ok := db.profiles[id]
	return p, ok
}

func (db *DB) ListProfiles() []Profile {
	db.mu.RLock()
	defer db.mu.RUnlock()

	list := make([]Profile, 0, len(db.profiles))
	for _, p := range db.profiles {
		list = append(list, p)
	}
	return list
}

func (db *DB) DeleteProfile(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, ok := db.profiles[id]; !ok {
		return errors.New("profile not found")
	}
	delete(db.profiles, id)

	// Clean up any servers associated with this profile
	for sID, s := range db.servers {
		if s.ProfileID == id {
			delete(db.servers, sID)
		}
	}
	_ = db.save("servers.json", db.servers)

	return db.save("profiles.json", db.profiles)
}

// --- Servers CRUD ---

func (db *DB) SaveServer(s Server) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.servers[s.ID] = s
	return db.save("servers.json", db.servers)
}

func (db *DB) GetServer(id string) (Server, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	s, ok := db.servers[id]
	return s, ok
}

func (db *DB) GetServerByProfile(profileID string) (Server, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	for _, s := range db.servers {
		if s.ProfileID == profileID {
			return s, true
		}
	}
	return Server{}, false
}

func (db *DB) ListServers() []Server {
	db.mu.RLock()
	defer db.mu.RUnlock()

	list := make([]Server, 0, len(db.servers))
	for _, s := range db.servers {
		list = append(list, s)
	}
	return list
}

// --- Stats Samples (in-memory only — never persisted to disk) ---

func (db *DB) AddStatsSample(s StatsSample) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	s.ID = db.nextSampleID
	db.nextSampleID++
	db.stats = append(db.stats, s)

	// Cap the ring buffer at 5000 samples to bound memory usage.
	if len(db.stats) > 5000 {
		db.stats = db.stats[len(db.stats)-5000:]
	}

	return nil // intentionally not saved to disk
}

func (db *DB) GetServerStats(serverID string, limit int) []StatsSample {
	db.mu.RLock()
	defer db.mu.RUnlock()

	var list []StatsSample
	for i := len(db.stats) - 1; i >= 0; i-- {
		if db.stats[i].ServerID == serverID {
			list = append([]StatsSample{db.stats[i]}, list...)
			if limit > 0 && len(list) >= limit {
				break
			}
		}
	}
	return list
}

// --- Benchmark Runs CRUD ---

func (db *DB) SaveBenchmarkRun(r BenchmarkRun) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.benchmarks[r.ID] = r
	return db.save("benchmarks.json", db.benchmarks)
}

func (db *DB) GetBenchmarkRun(id string) (BenchmarkRun, bool) {
	db.mu.RLock()
	defer db.mu.RUnlock()

	r, ok := db.benchmarks[id]
	return r, ok
}

func (db *DB) ListBenchmarkRuns() []BenchmarkRun {
	db.mu.RLock()
	defer db.mu.RUnlock()

	list := make([]BenchmarkRun, 0, len(db.benchmarks))
	for _, r := range db.benchmarks {
		list = append(list, r)
	}
	return list
}

func (db *DB) DeleteBenchmarkRun(id string) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	if _, ok := db.benchmarks[id]; !ok {
		return errors.New("benchmark run not found")
	}
	delete(db.benchmarks, id)
	return db.save("benchmarks.json", db.benchmarks)
}

func (db *DB) AddDownloadHistory(h DownloadJobHistory) error {
	db.mu.Lock()
	defer db.mu.Unlock()

	db.downloadHistory = append(db.downloadHistory, h)

	if len(db.downloadHistory) > 100 {
		db.downloadHistory = db.downloadHistory[len(db.downloadHistory)-100:]
	}

	return db.save("download_history.json", db.downloadHistory)
}

func (db *DB) ListDownloadHistory() []DownloadJobHistory {
	db.mu.RLock()
	defer db.mu.RUnlock()

	list := make([]DownloadJobHistory, len(db.downloadHistory))
	copy(list, db.downloadHistory)
	return list
}

// GetLogFilePath returns the file path on disk for a server's logs

func (db *DB) GetLogFilePath(serverID string) string {
	return filepath.Join(db.dataDir, "logs", fmt.Sprintf("server-%s.log", serverID))
}

// GetLogFileLines retrieves the last N lines from the server log
func (db *DB) GetLogFileLines(serverID string, maxLines int) ([]string, error) {
	path := db.GetLogFilePath(serverID)
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	// Read lines efficiently by seeking or reading the whole file if small.
	// Since these are local log files, a scanner is fast enough for v1.
	var lines []string
	
	// Read entire file (max 10MB to avoid OOM)
	fi, err := file.Stat()
	if err != nil {
		return nil, err
	}
	
	size := fi.Size()
	var limit int64 = 10 * 1024 * 1024 // 10MB
	if size > limit {
		_, _ = file.Seek(size-limit, io.SeekStart)
	}

	// Simple scanner
	var current []string
	var buf []byte = make([]byte, 32*1024)
	var leftOver []byte
	
	for {
		n, err := file.Read(buf)
		if n > 0 {
			chunk := append(leftOver, buf[:n]...)
			start := 0
			for i, b := range chunk {
				if b == '\n' {
					current = append(current, string(chunk[start:i]))
					start = i + 1
				}
			}
			leftOver = chunk[start:]
		}
		if err != nil {
			if len(leftOver) > 0 {
				current = append(current, string(leftOver))
			}
			break
		}
	}

	lines = current
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return lines, nil
}
