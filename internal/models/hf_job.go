package models

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"llama-server-studio/internal/storage"
)

// Job and per-file status constants.  These are the canonical values that get
// serialised in API responses and persisted to .job.json — keep them stable.
const (
	StatusPending    = "pending"
	StatusRunning    = "running"
	StatusCancelling = "cancelling"
	StatusCancelled  = "cancelled"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusPartial    = "partial"
	StatusSkipped    = "skipped"
)

// ErrJobInProgress is returned by StartJob when another job is already active.
// The HTTP handler turns this into a 409 Conflict with the current job body.
var ErrJobInProgress = errors.New("another download job is already in progress")

// DownloadFile is one row inside a DownloadJob.  Field names are chosen to
// match the JSON shape consumed by the frontend.
type DownloadFile struct {
	Filename    string  `json:"filename"`
	URL         string  `json:"-"` // resolved CDN URL, not exposed to API
	SizeBytes   int64   `json:"size_bytes"`
	BytesLoaded int64   `json:"bytes_loaded"`
	Progress    float64 `json:"progress"` // 0..100
	IsLFS       bool    `json:"is_lfs"`
	SHA256      string  `json:"sha256,omitempty"`
	Status      string  `json:"status"`
	Error       string  `json:"error,omitempty"`
}

// DownloadJob is the unit the user starts and cancels.  At most one
// DownloadJob is active in the process at any moment.
type DownloadJob struct {
	ID         string          `json:"id"`
	RepoID     string          `json:"repo_id"`
	SafeRepo   string          `json:"safe_repo"`
	TargetDir  string          `json:"target_dir"`
	Files      []*DownloadFile `json:"files"`
	Status     string          `json:"status"`
	Error      string          `json:"error,omitempty"`
	StartedAt  string          `json:"started_at"`
	FinishedAt string          `json:"finished_at,omitempty"`

	// Cancellation handle.  Cancel() tears down whichever per-file HTTP read
	// loop is currently active; the worker checks ctx.Err() between chunks.
	ctx    context.Context //nolint:containedctx // single owner, lifecycle bound to job
	cancel context.CancelFunc

	// Persistence target — directory that holds <filename>.part and .job.json.
	// Cached so the persistence ticker doesn't need to recompute it.
	persistPath string
}

// Singleton job manager.  Only one DownloadJob can be active at any moment,
// matching the invariant in docs/hf_support.md §3.
var (
	activeJob   *DownloadJob
	activeJobMu sync.Mutex
)

// GetActiveJob returns a JSON-safe snapshot of the active or most-recently
// finished job, or nil if no job has ever run in this process.
func GetActiveJob() *DownloadJob {
	activeJobMu.Lock()
	defer activeJobMu.Unlock()
	if activeJob == nil {
		return nil
	}
	return snapshotJob(activeJob)
}

// StartJob spawns a new DownloadJob against the given repo and files.
// downloadRoot is the directory under which <safe_repo>/ is created
// (typically cfg.DataDir + "/models").  db is used to register completed
// GGUF files into the catalog as they finish.
func StartJob(repoID string, files []RepoFile, downloadRoot string, db *storage.DB) (*DownloadJob, error) {
	repoID = strings.TrimSpace(repoID)
	if repoID == "" {
		return nil, errors.New("repo id is required")
	}
	if !validRepoID(repoID) {
		return nil, fmt.Errorf("invalid repo id %q", repoID)
	}
	if len(files) == 0 {
		return nil, errors.New("at least one file must be selected")
	}
	if strings.TrimSpace(downloadRoot) == "" {
		return nil, errors.New("download root is not configured")
	}

	activeJobMu.Lock()
	// NOTE: do NOT `defer Unlock` here.  persistJob() (called below) takes
	// the same mutex, so we must release it manually before any helper that
	// re-enters the lock.

	if activeJob != nil && (activeJob.Status == StatusRunning || activeJob.Status == StatusCancelling) {
		snap := snapshotJob(activeJob)
		activeJobMu.Unlock()
		// 409 path — give the caller back the active job so it can re-sync
		return snap, ErrJobInProgress
	}

	safe := SafeRepoDir(repoID)
	dir := filepath.Join(downloadRoot, safe)
	if err := os.MkdirAll(dir, 0755); err != nil {
		activeJobMu.Unlock()
		return nil, fmt.Errorf("failed to create target dir: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	job := &DownloadJob{
		ID:          newJobID(),
		RepoID:      repoID,
		SafeRepo:    safe,
		TargetDir:   dir,
		Status:      StatusRunning,
		StartedAt:   time.Now().Format(time.RFC3339),
		ctx:         ctx,
		cancel:      cancel,
		persistPath: filepath.Join(dir, ".job.json"),
	}

	for _, f := range files {
		job.Files = append(job.Files, &DownloadFile{
			Filename:  f.Filename,
			URL:       resolveURL(repoID, f.Filename),
			SizeBytes: f.SizeBytes,
			IsLFS:     f.IsLFS,
			SHA256:    f.SHA256,
			Status:    StatusPending,
		})
	}

	activeJob = job
	initialSnap := snapshotJob(job)
	activeJobMu.Unlock()

	// Write the initial .job.json snapshot so a hard crash during the first
	// few seconds of work still leaves a resumable trail.  Done outside the
	// lock because persistJob reacquires it internally.
	_ = persistJob(job)

	// Workers, progress flushing, and persistence each run as goroutines so
	// that StartJob returns immediately to the HTTP handler.
	go runJob(job, db)
	go persistJobLoop(job)

	return initialSnap, nil
}

// CancelJob signals the active job to stop.  Returns false if no job is
// running.  The job transitions to cancelling immediately, then to cancelled
// once the worker observes ctx.Err().
func CancelJob() bool {
	activeJobMu.Lock()
	defer activeJobMu.Unlock()
	if activeJob == nil || activeJob.Status != StatusRunning {
		return false
	}
	activeJob.Status = StatusCancelling
	activeJob.cancel()
	return true
}

// ResumableJob is what we surface from the .job.json files left on disk after
// a previous run.  It is a stripped-down view of DownloadJob suitable for
// rendering a "resume" list in the UI.
type ResumableJob struct {
	RepoID    string          `json:"repo_id"`
	SafeRepo  string          `json:"safe_repo"`
	TargetDir string          `json:"target_dir"`
	StartedAt string          `json:"started_at,omitempty"`
	Files     []*DownloadFile `json:"files"`
}

// ListResumableJobs walks `<downloadRoot>/*/.job.json`, returns the snapshots
// that still have at least one .part file on disk.  A job with no remaining
// .part files isn't actually resumable — we filter those out so the UI list
// doesn't fill up with completed work.
//
// Always returns a non-nil slice so the JSON encoder emits `[]` rather than
// `null` for an empty result — that keeps the frontend's `.length` checks
// simple without null-guards.
func ListResumableJobs(downloadRoot string) []ResumableJob {
	out := make([]ResumableJob, 0)
	if downloadRoot == "" {
		return out
	}
	entries, err := os.ReadDir(downloadRoot)
	if err != nil {
		return out
	}
	for _, ent := range entries {
		if !ent.IsDir() {
			continue
		}
		dir := filepath.Join(downloadRoot, ent.Name())
		job, err := readPersistedJob(dir)
		if err != nil {
			continue
		}
		// Filter to only files that still need work — has a matching .part
		// file, OR the final file doesn't yet exist.
		var pending []*DownloadFile
		for _, f := range job.Files {
			finalPath := filepath.Join(dir, f.Filename)
			partPath := finalPath + ".part"
			if _, err := os.Stat(partPath); err == nil {
				pending = append(pending, f)
				continue
			}
			if _, err := os.Stat(finalPath); errors.Is(err, os.ErrNotExist) {
				pending = append(pending, f)
			}
		}
		if len(pending) == 0 {
			continue
		}
		out = append(out, ResumableJob{
			RepoID:    job.RepoID,
			SafeRepo:  job.SafeRepo,
			TargetDir: dir,
			StartedAt: job.StartedAt,
			Files:     pending,
		})
	}
	return out
}

// SafeRepoDir sanitises a repo id into a single filesystem-safe directory
// name.  Forward slashes become "__"; anything outside [A-Za-z0-9._-] becomes
// "_".  Result is never empty for a non-empty input.
func SafeRepoDir(repoID string) string {
	if repoID == "" {
		return "_"
	}
	s := strings.ReplaceAll(repoID, "/", "__")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "_"
	}
	return b.String()
}

// snapshotJob returns a deep-enough copy of the job suitable for JSON
// encoding outside the lock.  Files are copied by value so concurrent
// progress updates don't race with the encoder.
func snapshotJob(j *DownloadJob) *DownloadJob {
	out := *j
	out.ctx = nil
	out.cancel = nil
	files := make([]*DownloadFile, len(j.Files))
	for i, f := range j.Files {
		fc := *f
		files[i] = &fc
	}
	out.Files = files
	return &out
}

// newJobID returns a short timestamp-based id.  It is not cryptographically
// significant — only used as a stable handle for the lifetime of one job.
func newJobID() string {
	return fmt.Sprintf("hfjob-%d", time.Now().UnixNano())
}
