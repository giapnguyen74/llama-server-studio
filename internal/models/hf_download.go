package models

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama-server-studio/internal/storage"
)

// downloadClient is used for the actual GET that streams bytes.  No timeout —
// large LFS files routinely take longer than any reasonable global timeout.
// The job context still bounds the request lifetime.
var downloadClient = &http.Client{}

// progressFlushInterval is the minimum gap between two mutex-guarded writes
// to a DownloadFile's progress fields, matching today's behaviour in
// hf_downloader.go.  Keeps lock contention low even at multi-GB/s read rates.
const progressFlushInterval = 250 * time.Millisecond

// persistInterval is how often we re-serialise the active job to .job.json so
// a hard restart can still tell what was running.  Slower than the in-memory
// flush since disk writes are expensive and bounded loss of 5 s of progress
// is fine.
const persistInterval = 5 * time.Second

// runJob is the worker goroutine spawned by StartJob.  It walks files in
// order, downloads each one, then transitions the job to its terminal state.
func runJob(job *DownloadJob, db *storage.DB) {
	defer func() {
		activeJobMu.Lock()
		if job.Status == StatusRunning || job.Status == StatusCancelling {
			job.Status = computeTerminalStatus(job)
		}
		job.FinishedAt = time.Now().Format(time.RFC3339)
		
		var totalBytes int64
		for _, f := range job.Files {
			totalBytes += f.SizeBytes
		}
		filesCount := len(job.Files)
		
		activeJobMu.Unlock()
		_ = persistJob(job)

		_ = db.AddDownloadHistory(storage.DownloadJobHistory{
			ID:         job.ID,
			RepoID:     job.RepoID,
			Status:     job.Status,
			StartedAt:  job.StartedAt,
			FinishedAt: job.FinishedAt,
			FilesCount: filesCount,
			TotalBytes: totalBytes,
		})

		// Trigger Dynamic Monitor Worker if the job failed or is partially completed
		if job.Status == StatusFailed || job.Status == StatusPartial {
			go startMonitorWorker(job, db)
		}
	}()


	for _, file := range job.Files {
		// Cancellation checkpoint — observed between files as well as during
		// the per-file read loop.  Avoids dragging the user through one more
		// huge file after they hit Cancel.
		if err := job.ctx.Err(); err != nil {
			activeJobMu.Lock()
			file.Status = StatusCancelled
			activeJobMu.Unlock()
			continue
		}

		activeJobMu.Lock()
		file.Status = StatusRunning
		activeJobMu.Unlock()

		err := downloadOne(job.ctx, file, job.TargetDir, db)

		activeJobMu.Lock()
		if errors.Is(err, context.Canceled) {
			file.Status = StatusCancelled
		} else if err != nil {
			file.Status = StatusFailed
			file.Error = err.Error()
		} else if file.Status != StatusSkipped {
			file.Status = StatusCompleted
			file.Progress = 100
			// Snap BytesLoaded to SizeBytes so the final UI tick agrees
			// with the progress bar — the throttled 250ms flush usually
			// leaves them a few KB apart.
			if file.SizeBytes > 0 {
				file.BytesLoaded = file.SizeBytes
			}
		}
		activeJobMu.Unlock()
	}
}

// computeTerminalStatus inspects the per-file outcomes to decide whether the
// job as a whole completed, partially completed, was cancelled, or failed.
// Caller must hold activeJobMu.
func computeTerminalStatus(job *DownloadJob) string {
	if job.Status == StatusCancelling {
		return StatusCancelled
	}
	any, allOK, anyFail, anyCancel := false, true, false, false
	for _, f := range job.Files {
		any = true
		switch f.Status {
		case StatusCompleted, StatusSkipped:
			// ok
		case StatusFailed:
			allOK = false
			anyFail = true
		case StatusCancelled:
			allOK = false
			anyCancel = true
		default:
			allOK = false
		}
	}
	if !any {
		return StatusFailed
	}
	if allOK {
		return StatusCompleted
	}
	if anyCancel && !anyFail {
		return StatusCancelled
	}
	return StatusPartial
}

// downloadOne handles a single file: HEAD probe to pin the URL, optional
// Range resume off any existing .part, streamed read with progress, SHA
// verify (when known), then atomic rename to the final filename.
//
// The function deliberately does NOT delete the .part file on cancel or on
// non-fatal error — that's what makes resume possible.
func downloadOne(ctx context.Context, file *DownloadFile, dir string, db *storage.DB) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	// HF allows subdir filenames like "gguf/model-Q4.gguf"; honour them.
	absDir, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return fmt.Errorf("resolve target dir: %w", err)
	}
	finalPath := filepath.Join(absDir, file.Filename)
	absFinalPath, err := filepath.Abs(filepath.Clean(finalPath))
	if err != nil {
		return fmt.Errorf("resolve final path: %w", err)
	}
	rel, err := filepath.Rel(absDir, absFinalPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path traversal detected: path escapes target directory")
	}

	if err := os.MkdirAll(filepath.Dir(absFinalPath), 0755); err != nil {
		return fmt.Errorf("create file subdir: %w", err)
	}
	finalPath = absFinalPath
	partPath := finalPath + ".part"

	// Fast path: a previous run may have already finished this file.  If the
	// final path exists with the right size (and SHA matches when known),
	// mark skipped and avoid re-downloading.
	if info, err := os.Stat(finalPath); err == nil && !info.IsDir() {
		sameSize := file.SizeBytes == 0 || info.Size() == file.SizeBytes
		shaOK, _ := verifyFinalSHA(finalPath, file.SHA256)
		if sameSize && shaOK {
			activeJobMu.Lock()
			file.BytesLoaded = info.Size()
			file.Progress = 100
			file.Status = StatusSkipped
			activeJobMu.Unlock()
			_, _ = AddManualModel(db, finalPath)
			return nil
		}
	}

	// HEAD-first to pin the CDN URL where possible (see docs §7.1).  Failure
	// here is non-fatal — many HF CDN edges do support HEAD on resolve URLs,
	// but the GET path also works on its own.
	pinnedURL := file.URL
	if u, totalFromHead, ok := headProbe(ctx, file.URL); ok {
		pinnedURL = u
		if file.SizeBytes == 0 && totalFromHead > 0 {
			activeJobMu.Lock()
			file.SizeBytes = totalFromHead
			activeJobMu.Unlock()
		}
	}

	// Check for partial download on disk.
	var localSize int64
	if stat, err := os.Stat(partPath); err == nil {
		localSize = stat.Size()
	}

	// Build the GET, with a Range header if we have something to resume from.
	req, err := http.NewRequestWithContext(ctx, "GET", pinnedURL, nil)
	if err != nil {
		return fmt.Errorf("build download request: %w", err)
	}
	applyHFAuth(req)
	if localSize > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", localSize))
	}

	resp, err := downloadClient.Do(req)
	if err != nil {
		return fmt.Errorf("start download: %w", err)
	}
	// Close whichever body is currently bound even after we re-issue on 416.
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()

	// 416 Range Not Satisfiable — the upstream file has changed (or our
	// .part is somehow larger than the upstream).  Throw away the partial
	// and start over from byte 0.  Record the reason so the user knows.
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		_ = os.Remove(partPath)
		localSize = 0

		activeJobMu.Lock()
		file.Error = "upstream file changed since last attempt; restarted from 0"
		file.BytesLoaded = 0
		file.Progress = 0
		activeJobMu.Unlock()

		req, err = http.NewRequestWithContext(ctx, "GET", pinnedURL, nil)
		if err != nil {
			return fmt.Errorf("retry build: %w", err)
		}
		applyHFAuth(req)
		resp, err = downloadClient.Do(req)
		if err != nil {
			return fmt.Errorf("retry start: %w", err)
		}
	}

	var (
		out       *os.File
		loaded    int64
		totalSize int64
	)
	switch resp.StatusCode {
	case http.StatusPartialContent:
		out, err = os.OpenFile(partPath, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0644)
		if err != nil {
			return fmt.Errorf("open part for append: %w", err)
		}
		loaded = localSize
		totalSize = localSize + resp.ContentLength
	case http.StatusOK:
		out, err = os.Create(partPath)
		if err != nil {
			return fmt.Errorf("create part: %w", err)
		}
		loaded = 0
		totalSize = resp.ContentLength
	default:
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("download status %d: %s", resp.StatusCode, strings.TrimSpace(string(preview)))
	}
	defer out.Close()

	// Seed progress (also handles the resume-from-N% case).
	activeJobMu.Lock()
	if totalSize > 0 {
		file.SizeBytes = totalSize
	}
	file.BytesLoaded = loaded
	file.Progress = pct(loaded, totalSize)
	activeJobMu.Unlock()

	// Stream loop — mirrors today's 32 KiB buffer + 250ms progress flush.
	buf := make([]byte, 32*1024)
	lastUpdate := time.Now()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fmt.Errorf("write part: %w", werr)
			}
			loaded += int64(n)
			if time.Since(lastUpdate) >= progressFlushInterval || (totalSize > 0 && loaded == totalSize) {
				lastUpdate = time.Now()
				activeJobMu.Lock()
				file.BytesLoaded = loaded
				file.Progress = pct(loaded, totalSize)
				activeJobMu.Unlock()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("read body: %w", rerr)
		}
	}
	out.Close()

	// SHA verification before the atomic rename (docs §7.3).  Only enforced
	// when HF gave us an LFS sha256 to compare against; otherwise we trust
	// the size and move on.
	if file.SHA256 != "" {
		got, err := fileSHA256(partPath)
		if err != nil {
			return fmt.Errorf("hash part: %w", err)
		}
		if !strings.EqualFold(got, file.SHA256) {
			return fmt.Errorf("sha256 mismatch (expected %s, got %s) — .part left on disk for inspection", file.SHA256, got)
		}
	}

	if err := os.Rename(partPath, finalPath); err != nil {
		return fmt.Errorf("finalise part: %w", err)
	}

	// Make the freshly-downloaded GGUF visible in the catalog immediately.
	if strings.HasSuffix(strings.ToLower(finalPath), ".gguf") {
		_, _ = AddManualModel(db, finalPath)
	}
	return nil
}

// headProbe issues a HEAD against the resolve URL, follows redirects, and
// returns the final URL plus the Content-Length (if known).  Failure is
// silent — callers fall back to the plain GET path.
func headProbe(ctx context.Context, url string) (string, int64, bool) {
	req, err := http.NewRequestWithContext(ctx, "HEAD", url, nil)
	if err != nil {
		return "", 0, false
	}
	applyHFAuth(req)
	resp, err := hfClient.Do(req)
	if err != nil {
		return "", 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", 0, false
	}
	finalURL := url
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL.String()
	}
	return finalURL, resp.ContentLength, true
}

// fileSHA256 hashes the bytes at path.  Used during completion verification.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyFinalSHA checks the SHA of an already-existing finalised file.  When
// expected is empty we treat the file as OK (callers fall back to size).
func verifyFinalSHA(path, expected string) (bool, error) {
	if expected == "" {
		return true, nil
	}
	got, err := fileSHA256(path)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(got, expected), nil
}

// pct returns 0..100, defaulting to 0 when total is not yet known.
func pct(loaded, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(loaded) / float64(total) * 100
}

// persistJobLoop writes the .job.json snapshot every persistInterval until
// the job leaves the running/cancelling states.
func persistJobLoop(job *DownloadJob) {
	t := time.NewTicker(persistInterval)
	defer t.Stop()
	for range t.C {
		activeJobMu.Lock()
		done := job.Status != StatusRunning && job.Status != StatusCancelling
		activeJobMu.Unlock()
		_ = persistJob(job)
		if done {
			return
		}
	}
}

// persistJob writes the job to dir/.job.json atomically via a sibling temp
// file.  Safe to call from any goroutine; takes the active job lock so the
// serialised snapshot is internally consistent.
func persistJob(job *DownloadJob) error {
	activeJobMu.Lock()
	snap := snapshotJob(job)
	path := job.persistPath
	activeJobMu.Unlock()

	if path == "" {
		return nil
	}
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readPersistedJob loads a .job.json from a repo subdir, demoting any job
// that was still marked running at the time of the last snapshot — that
// means the studio process died mid-flight (docs §10).
func readPersistedJob(dir string) (*DownloadJob, error) {
	data, err := os.ReadFile(filepath.Join(dir, ".job.json"))
	if err != nil {
		return nil, err
	}
	var job DownloadJob
	if err := json.Unmarshal(data, &job); err != nil {
		return nil, err
	}
	if job.Status == StatusRunning || job.Status == StatusCancelling {
		job.Status = StatusCancelled
	}
	job.TargetDir = dir
	job.persistPath = filepath.Join(dir, ".job.json")
	return &job, nil
}

// startMonitorWorker is the Dynamic Monitor Worker goroutine.
// It waits for 10 minutes, checks if the job is still in a failed/partial state
// and has not been manually resumed, and then wiggles the resume execution loop.
func startMonitorWorker(job *DownloadJob, db *storage.DB) {
	timer := time.NewTimer(10 * time.Minute)
	defer timer.Stop()

	select {
	case <-timer.C:
		// Timer elapsed, proceed to evaluate state
	}

	activeJobMu.Lock()
	defer activeJobMu.Unlock()

	// If the active job is currently running or cancelling, do not touch it
	if activeJob != nil && (activeJob.Status == StatusRunning || activeJob.Status == StatusCancelling) {
		return
	}

	// Verify the active job ID matches the one we want to resume
	if activeJob != nil && activeJob.ID == job.ID {
		if activeJob.Status == StatusFailed || activeJob.Status == StatusPartial {
			// Reconstruct the pending selection list
			var pending []*DownloadFile
			for _, f := range activeJob.Files {
				if f.Status != StatusCompleted && f.Status != StatusSkipped {
					pending = append(pending, f)
				}
			}

			if len(pending) > 0 {
				ctx, cancel := context.WithCancel(context.Background())
				activeJob.Status = StatusRunning
				activeJob.Error = ""
				activeJob.ctx = ctx
				activeJob.cancel = cancel

				for _, f := range activeJob.Files {
					if f.Status != StatusCompleted && f.Status != StatusSkipped {
						f.Status = StatusPending
						f.Error = ""
					}
				}

				// Re-run the job sequential executor
				go runJob(activeJob, db)
				go persistJobLoop(activeJob)
			}
		}
	}
}
