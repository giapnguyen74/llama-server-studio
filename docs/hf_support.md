# Hugging Face Hub Download Support — Design

Status: design accepted, not yet implemented
Author: design discussion, May 2026
Scope: replaces the current ad-hoc `repo_id:quant` downloader in `internal/models/hf_downloader.go`

## 1. Problem

The current downloader takes a single string like `unsloth/Qwen3.6-27B-GGUF:Q4_K_M`, picks one GGUF by substring match, writes to `cfg.ModelsDirs[0]`, supports HTTP-Range resume, and enforces "one active download at a time". This works as a smoke test but is too narrow for real use:

- No way to see what files a repo actually contains before committing to a download.
- No way to grab multiple files from the same repo (common for split GGUFs, multi-quant repos, or accompanying tokenizer/config files).
- No per-file progress reporting, no cancel button, no recovery path for a half-finished file after the studio restarts.
- The UI doesn't reflect the "one job at a time" invariant — nothing stops a user from typing a second repo string and clobbering the in-flight state.
- Files land flat in `models_dirs[0]`, which scrambles provenance and makes the eventual catalog scan ambiguous.

## 2. Target behaviour

A dedicated "Hugging Face Hub" tab in the Web UI. The user pastes a repo ID (e.g. `unsloth/Qwen3.6-27B-GGUF`). The server fetches repo metadata and returns the file list. The UI displays the files, filtered to `*.gguf` by default with a checkbox to show all files. The user ticks one or more files and starts the download. Files are saved to `<data-dir>/models/<safe_repo>/<filename>`. While the job is active the tab is locked to that job — the input is replaced by the progress panel — until the user cancels or it completes. Cancelled or interrupted downloads leave `.part` files behind so the user can resume via HTTP Range without losing bytes.

## 3. High-level model

A **DownloadJob** is `(repo_id, []FileSelection)`. The system runs **at most one** DownloadJob at any time — this is the same invariant the current code already enforces, extended from "one file" to "one repo's batch".

Files within a job download **sequentially**. Reasons: simpler bookkeeping, kinder to HF's LFS CDN (avoids parallel range bursts that can trip throttling), and clearer progress UX. Parallelism inside a single repo can be added later if it proves useful; it is not part of v1.

The job owns a `context.Context`. Cancelling the job calls its `CancelFunc`, which tears down the active HTTP read loop and stops the worker. The in-flight `.part` file is flushed and left on disk so a subsequent run can resume it.

Job lifecycle: `pending → running → (cancelling) → cancelled | failed | completed | partial`.
Per-file status: same enum plus `skipped` (file already exists on disk at the expected size and, if available, SHA).

## 4. Filesystem layout

```
<data-dir>/models/<safe_repo>/<filename>
<data-dir>/models/<safe_repo>/<filename>.part   (in-flight)
<data-dir>/models/<safe_repo>/.job.json         (persisted job state)
```

`safe_repo` is the repo ID with `/` replaced by `__`, and any character outside `[A-Za-z0-9._-]` dropped or replaced with `_`. Example: `unsloth/Qwen3.6-27B-GGUF` → `unsloth__Qwen3.6-27B-GGUF` (replace all dangerous characters with `_`).

The `.part` suffix replaces the current `.download` suffix and is the canonical signal "this file is partial, eligible for HTTP-Range resume". On startup the studio scans `models/*/*.part` and surfaces those as resumable jobs in the UI.

If a repo's filename contains a forward slash (HF allows e.g. `gguf/model-Q4.gguf`), the subdirectory is created under `<safe_repo>/`. The `.part` file lives next to its final path.

## 5. Backend package layout

Split today's single `internal/models/hf_downloader.go` into three files inside the same package (keeps the import path stable):

**`hf_api.go`** — pure metadata fetch.

```
FetchRepoFiles(repoID string) ([]RepoFile, error)
```

Calls `GET https://huggingface.co/api/models/<repo>?blobs=true`. The `blobs=true` parameter is what makes the response include `lfs.size` and the OID hash for LFS files (which is what GGUFs almost always are). For non-LFS files we use the plain `size` field from the siblings entry. Returns `[]RepoFile{Filename, SizeBytes, IsLFS, SHA256}`. The existing `HF_TOKEN` / `HF_API_TOKEN` env-var auth logic moves here unchanged.

**`hf_job.go`** — the job state machine.

Defines `DownloadJob`, `DownloadFile`, the package-level `activeJob *DownloadJob` pointer guarded by a `sync.Mutex`, and the public functions `StartJob`, `CancelJob`, `GetActiveJob`, `ListResumableJobs`. `StartJob` rejects with a sentinel `ErrJobInProgress` if `activeJob != nil && activeJob.Status == running`. The HTTP layer translates that to a 409.

**`hf_download.go`** — the per-file worker.

```
downloadOne(ctx context.Context, file *DownloadFile, dir string) error
```

This is the existing `performDownload` logic, plus three changes: `ctx` is plumbed through via `http.NewRequestWithContext`; progress updates flush to the file struct under the job mutex (still throttled to every 250ms as today); on `ctx.Err() != nil` the function returns cleanly without deleting the `.part` file.

## 6. HTTP API

Replace the current two routes with five new ones, all under the existing auth middleware:

```
GET    /api/hf/repo?repo=<repo_id>          metadata + file list
POST   /api/hf/jobs                          start new job  (body: {repo, files:[...]})
GET    /api/hf/jobs/current                  active or last-finished job
POST   /api/hf/jobs/current/cancel           cancel
GET    /api/hf/jobs/resumable                repos with .part files on disk
```

`GET /api/hf/repo` returns:

```json
{
  "repo_id": "unsloth/Qwen3.6-27B-GGUF",
  "files": [
    {"filename": "Qwen3.6-27B-Q4_K_M.gguf", "size": 17234567890, "is_lfs": true, "sha256": "..."}
  ]
}
```

The frontend does GGUF filtering client-side off this list. Errors map cleanly: 404 from HF → 404 with a "repository not found" message; 401/403 from HF → 401 with an "authentication required, set HF_TOKEN" message; network or 5xx → 502.

`POST /api/hf/jobs` returns 201 on success with the freshly-created job document. On conflict it returns `409 Conflict` with `{error, active_job}` so the client can re-sync to the actual running job (e.g. two browser tabs racing).

Keep the legacy `POST /api/models/download` route as a thin shim that constructs a single-file job, so any external scripts continue to work.

## 7. HTTP-Range resume — three details that matter

1. **HEAD before GET on the first request.** HF often 302s LFS downloads to CloudFront. Do a `HEAD` first to learn the resolved URL and canonical `Content-Length`, save the resolved URL on the job, and reuse it on subsequent range requests so retries hit the same CDN edge.
2. **416 means the upstream file changed.** The current code already handles this by restarting from zero. Keep that behaviour, but additionally record a line in the job's error log so the user knows their old `.part` was discarded.
3. **Verify on completion.** When HF metadata included an LFS SHA256, hash the finished file and compare *before* the atomic rename `.part → final`. On mismatch, mark the file `failed`, leave the `.part` on disk for inspection, and continue to the next file in the job. This is on by default for LFS files; non-LFS files fall back to a size check only.

## 8. Concurrency invariant

The "one job at a time" rule is enforced in two layers:

- **Backend** is the ground truth: `POST /api/hf/jobs` returns 409 with the current job when one is active.
- **Frontend** mirrors this by disabling the input and showing the progress panel while `GET /api/hf/jobs/current` reports `running`. If two tabs race and one gets a 409, it re-syncs by reading `current` and rendering the active job.

This matches the existing pattern in the catalog (single global state, polled at 1Hz).

## 9. Frontend — "Hugging Face Hub" tab

A new top-level navigation entry, peer of Catalog / Profiles / Servers / Benchmarks. Three sub-views, only one visible at a time, driven by the current job status:

**View A — Browse.** No active job. A single text input ("Paste a repo ID, e.g. `unsloth/Qwen3.6-27B-GGUF`") and a "Fetch files" button. Below the input, a "Resume previous downloads" section lists any `.part` files found on disk, with a per-entry "Resume" button.

**View B — File picker.** Shown after a successful metadata fetch, before starting. A table of files with columns: select / filename / size / type. Above the table:

- A checked-by-default checkbox "Only show `.gguf` files" — this is the `*.gguf` filter; unchecking it reveals all files.
- A "Select all visible" link.
- A running "Estimated total: X.XX GB" summary that updates as the user toggles checkboxes.

Below the table: a "Start download" button (disabled when zero files are selected) and a "Back" link.

**View C — Active job.** Shown whenever a job is `running`. The repo input is hidden. The other top-level tabs (Catalog, Profiles, etc.) remain navigable; only navigation *within* the HF tab is restricted — there is nothing else to do here besides watching progress or cancelling. Layout: repo header, then one row per file with a progress bar, `bytes_loaded / bytes_total`, and a status pill. A single "Cancel job" button sits at the bottom. Cancelling opens a confirmation modal, then transitions the job to `cancelled` and returns to View A — with the now-partial files appearing in the resumable list.

Polling: while View C is visible, poll `/api/hf/jobs/current` at 1Hz, same cadence as today's `pollHfDownloads`.

## 10. State persistence

In-memory progress updates already happen on a 250ms throttle. Add a second, slower tick at 5s that writes `.job.json` atomically (write to a sibling temp file, then `os.Rename`). On studio startup:

1. Scan `models/*/.job.json`.
2. Any job whose status was `running` at the time of the last persisted snapshot is demoted to `cancelled` (the process died mid-flight; we can't know progress past the last 5s tick).
3. For each file in each surviving job that has a matching `.part` on disk, expose it via `GET /api/hf/jobs/resumable`.

This is enough to give the user a clear "you have an interrupted download, want to resume?" prompt after a restart, without needing a real database.

## 11. Edge cases — explicit decisions

- **Disk full mid-download** (ENOSPC from `out.Write`): file status `failed` with the underlying error, job continues to the next file, job ends with status `partial`.
- **Gated repo (HF returns 401/403)**: surface a specific error in the metadata fetch path mentioning `HF_TOKEN`. Do not create a job.
- **Repo not found (HF returns 404)**: surface 404 from `/api/hf/repo`, do not create a job.
- **Same file already complete**: idempotent. If the final file exists with matching size (and SHA when known), mark `skipped` and move on. This is the recovery path for "I closed the browser tab while a job was finishing".
- **Filenames with `/`** (HF subdir layout): create the subdirectory under `<safe_repo>/`; the `.part` lives next to the final path.
- **HEAD request not supported by CDN**: fall back to the existing GET-with-Range flow; we lose the URL-pinning optimisation but not correctness.

## 12. Implementation order

Each step leaves the binary in a working state:

1. `hf_api.go` and `GET /api/hf/repo` — pure read, lowest risk, no behavioural change.
2. Job struct, `StartJob`, `CancelJob`, `context.Context` plumbing — keep the body of the existing `performDownload`, just add the context.
3. New job routes (`POST /api/hf/jobs`, `GET /api/hf/jobs/current`, cancel). Keep `POST /api/models/download` as a single-file shim.
4. Frontend tab — View A → View B → View C flow.
5. `.job.json` persistence and `GET /api/hf/jobs/resumable`, plus the resume entry-point in View A.
6. SHA256 verification gate before the atomic rename.

## 13. Out of scope (deferred)

- Parallel downloads within a single job. Useful for fast networks, but adds range-bookkeeping complexity not worth v1.
- Cross-repo job queue. Today's invariant is a single job, period; queueing across repos can come later if needed.
- Mirroring full HF snapshot layouts (`.gitattributes`, blob/ref dirs). The studio's storage layout is intentionally simpler than the HF cache and is meant to be human-readable.
- Bandwidth throttling, scheduled downloads, and pause-without-cancel. Cancel + Resume covers the pause use case adequately.
