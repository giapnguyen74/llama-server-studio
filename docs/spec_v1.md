# llama-server-studio `spec_v1.md`

## 1. Project Summary

**llama-server-studio** is a simple local Web UI and process manager for `llama-server` from `llama.cpp`.

The application helps a user:

1. Point the studio at a local `llama.cpp` binary directory.
2. Discover local `.gguf` model files from configured folders and Hugging Face cache folders.
3. Display useful model metadata in a model catalog.
4. Build reusable `llama-server` argument sets called **profiles**.
5. Start, stop, restart, and observe `llama-server` child processes created from profiles.
6. Collect runtime and benchmark statistics for each managed server.
7. Optionally expose a stable inference route by **profile id** so clients do not need to know the child server port.

The project is intentionally small. It is not a replacement for `llama.cpp`, Ollama, LM Studio, vLLM, Kubernetes, or a full production gateway. It is a local operations panel for designing, testing, benchmarking, and exporting `llama-server` profiles.

---

## 2. Product Name

Working name:

```text
Llama Server Studio
```

Repository name suggestion:

```text
llama-server-studio
```

Binary name suggestion:

```text
llama-server-studio
```

---

## 3. Goals

### 3.1 Primary Goals

- Provide a single local Web UI for managing `llama-server` instances.
- Make local GGUF model discovery visible and searchable.
- Help users construct valid `llama-server` command-line arguments without memorizing every flag.
- Allow repeatable server launches through saved profiles.
- Collect enough statistics to compare profiles, models, and hardware behavior.
- Export profiles for later production use.

### 3.2 Secondary Goals

- Provide a quick prompt test box for each running server.
- Provide a simple benchmark runner.
- Provide optional reverse proxy routing by profile id.
- Keep the implementation understandable for one developer or a small team.

### 3.3 Non-Goals for v1

- No model training or fine-tuning.
- No model download manager required in v1.
- No distributed multi-node scheduling.
- No multi-user authentication system required in v1.
- No cloud deployment automation.
- No automatic GPU memory overcommit solver.
- No requirement to support non-`llama.cpp` backends.
- No requirement to expose every possible `llama-server` flag as a first-class UI field.

---

## 4. Target Users

- Developers running GGUF models locally.
- Engineers tuning `llama-server` arguments.
- Users comparing quantizations, context sizes, GPU layer settings, batch sizes, and parallelism.
- Teams that want to export a known-good local serving profile into a deployment script.

---

## 5. Core Concepts

### 5.1 Studio Server

The Go web application created by this project.

Responsibilities:

- Serve the Web UI.
- Store configuration.
- Scan model directories.
- Manage profile definitions.
- Spawn and supervise `llama-server` child processes.
- Collect process, request, health, and benchmark statistics.
- Optionally proxy inference requests to child `llama-server` instances.

### 5.2 llama.cpp Path

The user must provide a path to the local `llama.cpp` binaries before managed servers can be started.

Accepted configuration examples:

```bash
llama-server-studio --llama-bin-dir /opt/llama.cpp/build/bin
llama-server-studio --llama-server-bin /opt/llama.cpp/build/bin/llama-server
LLAMA_SERVER_BIN=/opt/llama.cpp/build/bin/llama-server llama-server-studio
```

v1 should require a valid `llama-server` executable. Other binaries are optional.

Validation:

- File exists.
- File is executable.
- Running `llama-server --help` succeeds.
- Running `llama-server --version` is attempted, but failure should not block startup if `--help` works.

### 5.3 Model

A local `.gguf` file discovered by the studio.

A model may come from:

- A user-configured local model directory.
- A Hugging Face cache directory.
- A manually added direct file path.

### 5.4 Model Catalog

The scanned inventory of discovered GGUF models.

The catalog is read-only in v1. It does not move, rename, delete, or download models.

### 5.5 Profile

A saved `llama-server` configuration.

A profile contains:

- A selected GGUF model.
- A generated command-line argument list.
- Studio-side metadata such as name, tags, notes, and default port policy.
- Optional benchmark history.
- Optional routing settings.

### 5.6 Managed Server

A child `llama-server` process launched by the studio from a profile.

A profile can have zero or one active managed server in v1.

---

## 6. System Overview

```text
┌──────────────────────────────┐
│ Browser Web UI               │
└──────────────┬───────────────┘
               │ HTTP / WebSocket
┌──────────────▼───────────────┐
│ llama-server-studio           │
│ Go Web Server                 │
│                              │
│ - Config manager              │
│ - Model scanner               │
│ - Profile builder             │
│ - Process supervisor          │
│ - Stats collector             │
│ - Optional inference router   │
└───────┬──────────────┬───────┘
        │              │
        │ spawn        │ scan
        │              │
┌───────▼────────┐   ┌─▼────────────────────────┐
│ llama-server   │   │ Local model directories   │
│ child process  │   │ Hugging Face cache         │
└───────┬────────┘   └──────────────────────────┘
        │
        │ local HTTP
┌───────▼────────┐
│ llama.cpp API  │
└────────────────┘
```

---

## 7. Runtime Modes

### 7.1 Local Studio Mode

Default v1 mode.

- Studio binds to `127.0.0.1`.
- Managed `llama-server` children bind to `127.0.0.1`.
- Intended for local usage on a developer workstation.

Example:

```bash
llama-server-studio \
  --listen 127.0.0.1:3100 \
  --llama-server-bin /opt/llama.cpp/build/bin/llama-server \
  --models-dir ~/models
```

### 7.2 LAN Mode

Optional.

- Studio can bind to `0.0.0.0`.
- Should show a warning because local model paths and process controls are exposed.
- v1 may protect LAN mode with a simple static admin token.

Example:

```bash
llama-server-studio \
  --listen 0.0.0.0:3100 \
  --admin-token "$TOKEN"
```

---

## 8. Configuration

### 8.1 Config File Location

Default:

```text
~/.llama-server-studio/config.json
```

Override:

```bash
llama-server-studio --config /path/to/config.json
```

### 8.2 Data Directory

Default:

```text
~/.llama-server-studio
```

Override:

```bash
llama-server-studio --data-dir /path/to/data
```

### 8.3 Suggested Directory Layout

```text
~/.llama-server-studio/
  studio.db
  logs/
    server-{server_id}.log
  exports/
    profile-{profile_id}.json
    profile-{profile_id}.sh
```

### 8.4 Example Config

```yaml
listen: "127.0.0.1:3100"

llama:
  server_bin: "/opt/llama.cpp/build/bin/llama-server"
  bin_dir: "/opt/llama.cpp/build/bin"

models:
  scan_dirs:
    - "/home/user/models"
  scan_huggingface_cache: true
  huggingface_cache_dirs:
    - "/home/user/.cache/huggingface/hub"
  follow_symlinks: true
  max_depth: 8

runtime:
  managed_host: "127.0.0.1"
  port_range_start: 41000
  port_range_end: 41999
  shutdown_timeout_seconds: 10

routing:
  enabled: false
  base_path: "/profiles"

security:
  admin_token: ""
```

---

## 9. Model Discovery

### 9.1 Scan Sources

v1 scans:

1. User-configured directories.
2. Hugging Face cache directories when enabled.
3. Any direct model files manually added through the UI.

Default Hugging Face cache candidates:

```text
$HF_HOME/hub
$HUGGINGFACE_HUB_CACHE
$XDG_CACHE_HOME/huggingface/hub
~/.cache/huggingface/hub
```

On Windows, support common equivalent cache paths where practical.

### 9.2 File Matching

A file is considered a candidate model if:

- It has a `.gguf` extension.
- It is a regular file or a valid symlink to a regular file.
- Its size is greater than zero.
- It is readable by the studio process.

Ignore:

- Partial downloads.
- Lock files.
- Temporary files.
- Non-GGUF model formats.

### 9.3 Hugging Face Cache Handling

Hugging Face cache layouts can include snapshots, refs, blobs, and symlinks.

Scanner behavior:

- Prefer stable resolved snapshot file paths when available.
- Preserve the original display path.
- Resolve symlinks for deduplication.
- Infer `repo_id` from cache path when possible.
- Do not mutate the cache.

### 9.4 Deduplication

Models are deduplicated by:

1. Resolved absolute path.
2. File size + optional fast hash.
3. Optional full SHA-256 hash when the user requests verification.

v1 should not compute full SHA-256 for all large files by default because model files can be very large.

### 9.5 Metadata Extraction

The studio should extract GGUF metadata directly from the GGUF key-value header using an internal reader.

Required metadata fields:

| Field | Description |
| --- | --- |
| `id` | Stable studio id |
| `path` | Absolute model path |
| `display_name` | File name or friendly name |
| `size_bytes` | File size |
| `modified_at` | File modification timestamp |
| `source` | `local_dir`, `huggingface_cache`, or `manual` |
| `repo_id` | Inferred Hugging Face repo id when available |
| `architecture` | GGUF architecture metadata when available |
| `quantization` | Inferred from file name and/or metadata |
| `context_length` | Training context length when available |
| `embedding_length` | Embedding dimension when available |
| `block_count` | Transformer layer/block count when available |
| `chat_template` | Chat template string when available |
| `tokenizer_model` | Tokenizer metadata when available |
| `capabilities` | Inferred capabilities list |
| `metadata_raw` | Optional map of GGUF metadata keys |

Capability inference examples:

- `chat`: chat template exists or model name indicates instruct/chat.
- `completion`: default for text generation models.
- `embedding`: model metadata or user override says embeddings.
- `rerank`: user override or model naming convention.
- `vision`: multimodal metadata or user override.

Capability inference must be labeled as inferred unless it comes from reliable metadata.

### 9.6 Model Catalog UI

The catalog page should show:

- Model file name.
- Source.
- Quantization.
- Size.
- Architecture.
- Train context/window.
- Capabilities.
- Chat template availability.
- Path.
- Last modified time.

Table actions:

- View metadata.
- Create profile from model.
- Rescan file.
- Copy path.
- Hide from catalog.

Filters:

- Search by name/path/repo.
- Source.
- Architecture.
- Quantization.
- Capability.
- Minimum context length.

---

## 10. Profile Builder

### 10.1 Purpose

The profile builder converts user choices into a repeatable `llama-server` command.

The UI should have two layers:

1. **Simple mode** for common settings.
2. **Advanced mode** for raw arguments and less common flags.

### 10.2 Required Profile Fields

| Field | Description |
| --- | --- |
| `id` | Stable profile id |
| `name` | Human-readable name |
| `description` | Optional notes |
| `model_id` | Selected catalog model |
| `args` | Ordered `llama-server` args excluding executable path |
| `env` | Optional environment variables |
| `working_dir` | Optional process working directory |
| `default_host` | Usually `127.0.0.1` |
| `default_port_policy` | `auto`, `fixed`, or `manual` |
| `fixed_port` | Required when port policy is `fixed` |
| `tags` | Optional tags |
| `created_at` | Created timestamp |
| `updated_at` | Updated timestamp |

### 10.3 Simple Mode Fields

Minimum simple fields:

- Model.
- Display name.
- Context size.
- GPU layers.
- Threads.
- Batch size.
- Parallel sequences / slots.
- Host.
- Port policy.
- Chat template override.
- Reasonable presets for CPU, GPU, low memory, high throughput.

The exact `llama-server` flags should be generated by a version-aware argument registry.

### 10.4 Advanced Mode

Advanced mode provides:

- Raw argument editor.
- Parsed argument preview.
- Command preview.
- Validation warnings.
- Unknown-flag passthrough.

Important rule:

> v1 must not hard-code the assumption that the studio knows every current and future `llama-server` flag.

Instead:

- Store user args as an ordered list.
- Keep a small known-flag registry for UI convenience.
- Allow raw flags not known to the registry.
- Validate known conflicts where possible.
- Provide `llama-server --help` output in the UI for reference.

### 10.5 Command Preview

Every profile should show the exact generated command:

```bash
/path/to/llama-server \
  -m /models/example.Q4_K_M.gguf \
  --host 127.0.0.1 \
  --port 41001 \
  -c 8192 \
  -ngl 99
```

The preview must distinguish between:

- Stored args.
- Runtime-injected args such as auto-selected port.
- Studio-only settings.

### 10.6 Profile Export

Supported v1 export formats:

#### JSON

```json
{
  "schema_version": 1,
  "name": "mistral-q4-local",
  "llama_server_bin": "/opt/llama.cpp/build/bin/llama-server",
  "model_path": "/models/mistral.Q4_K_M.gguf",
  "args": ["-m", "/models/mistral.Q4_K_M.gguf", "--host", "127.0.0.1", "--port", "8080"],
  "env": {},
  "notes": "Known-good local profile"
}
```

#### Shell Script

```bash
#!/usr/bin/env bash
set -euo pipefail

LLAMA_SERVER_BIN="${LLAMA_SERVER_BIN:-/opt/llama.cpp/build/bin/llama-server}"

exec "$LLAMA_SERVER_BIN" \
  -m "/models/mistral.Q4_K_M.gguf" \
  --host "127.0.0.1" \
  --port "8080"
```

#### Docker Compose Snippet

Optional v1.1.

---

## 11. Managed Server Lifecycle

### 11.1 State Machine

Managed server states:

```text
stopped
starting
healthy
unhealthy
stopping
crashed
unknown
```

### 11.2 Start Flow

1. User clicks **Start** on a profile.
2. Studio validates profile.
3. Studio selects a port if profile uses `auto` port policy.
4. Studio creates a server record.
5. Studio starts `llama-server` as a child process.
6. Studio captures stdout and stderr.
7. Studio polls health endpoint or root endpoint.
8. Studio marks server `healthy` when responsive.

### 11.3 Stop Flow

1. User clicks **Stop**.
2. Studio sends interrupt/terminate signal.
3. Studio waits for graceful shutdown.
4. If timeout expires, Studio kills process.
5. Studio marks server `stopped`.
6. Logs and historical stats are retained.

### 11.4 Restart Flow

Restart is stop followed by start with the same profile and current profile args.

### 11.5 Crash Handling

When a child exits unexpectedly:

- Mark server as `crashed`.
- Record exit code.
- Record last log lines.
- Preserve benchmark and request stats.
- Do not auto-restart in v1 unless the profile explicitly enables it.

### 11.6 Process Isolation

v1 uses child processes only.

No containers are required.

### 11.7 Logs

Log requirements:

- Stream stdout/stderr to the UI.
- Store logs on disk per server instance.
- Provide last 100, 500, and 5000 line views.
- Allow log download.
- Redact obvious secrets from environment variables.

---

## 12. Stats Collection

### 12.1 Stats Sources

v1 can collect statistics from:

1. Child process metadata.
2. Process resource usage.
3. Studio proxy request timing when routing is enabled.
4. Periodic health checks.
5. `llama-server` monitoring endpoints when available.
6. Benchmark runner results.
7. Parsed server logs where useful.

### 12.2 Required Server Stats

Per managed server:

| Metric | Description |
| --- | --- |
| `uptime_seconds` | Time since process start |
| `status` | Current lifecycle state |
| `pid` | OS process id |
| `port` | Bound port |
| `profile_id` | Source profile |
| `model_id` | Source model |
| `cpu_percent` | Process CPU usage if available |
| `memory_rss_bytes` | Resident memory |
| `request_count` | Requests observed by Studio proxy |
| `error_count` | Failed proxied requests |
| `avg_latency_ms` | Average proxied request latency |
| `p50_latency_ms` | Median latency |
| `p95_latency_ms` | 95th percentile latency |
| `tokens_per_second` | From benchmark/proxy metadata when available |
| `last_health_check_at` | Timestamp |
| `last_error` | Last process or health error |

### 12.3 Optional Hardware Stats

Optional when platform support is available:

- GPU utilization.
- GPU memory used.
- GPU memory total.
- GPU temperature.
- System memory.
- System CPU load.

Implementation should be modular. Do not block v1 on GPU stats.

### 12.4 Stats Retention

Default retention:

- Live samples: keep every 2 seconds for last 10 minutes.
- Historical samples: downsample to 1 minute.
- Benchmark results: keep forever unless deleted.

---

## 13. Benchmarking

### 13.1 Benchmark Purpose

Benchmarking helps compare:

- Models.
- Quantizations.
- Context sizes.
- GPU layer settings.
- Batch/parallel settings.
- Prompt templates.

### 13.2 Benchmark Types

v1 should support at least one simple benchmark:

#### Prompt Completion Benchmark

Inputs:

- Prompt text.
- Max tokens.
- Temperature.
- Repeat count.
- Warmup count.
- Target profile/server.

Outputs:

- Time to first token if available.
- Total latency.
- Output tokens.
- Tokens per second.
- Error rate.
- Raw response preview.

### 13.3 Benchmark Runner Behavior

- Can run against an already running managed server.
- Can optionally start the server if stopped.
- Should prevent multiple benchmarks on the same server by default.
- Stores results linked to profile id and server id.

### 13.4 Benchmark Result UI

Show:

- Profile.
- Model.
- Args snapshot.
- Prompt.
- Date/time.
- Tokens per second.
- Latency.
- Errors.
- Notes.

---

## 14. Quick Test UI

Every profile and running server should provide a simple test box.

Minimum test controls:

- Prompt text.
- System prompt optional.
- Max tokens.
- Temperature.
- Stream on/off.
- Send button.
- Response panel.
- Request JSON preview.
- Copy response.
- Copy curl.

This quick test is not a full chat product. It exists to verify the server works.

---

## 15. Optional Inference Routing by Profile ID

### 15.1 Purpose

When enabled, clients can call Studio using a stable profile id instead of directly calling a child server port.

Example:

```text
POST /profiles/{profile_id}/v1/chat/completions
POST /profiles/{profile_id}/v1/completions
POST /profiles/{profile_id}/v1/embeddings
GET  /profiles/{profile_id}/health
```

Studio proxies the request to the currently running managed server for that profile.

### 15.2 Routing Behavior

If profile server is running and healthy:

- Forward request to child `llama-server`.
- Preserve request body.
- Preserve streaming responses.
- Collect request metrics.
- Return child response to caller.

If profile server is stopped:

- Return `503 Service Unavailable`.

If profile does not exist:

- Return `404 Not Found`.

If profile has multiple active servers:

- v1 should not allow this.
- Future versions may add load balancing.

### 15.3 Optional Auto-Start

Optional setting:

```yaml
routing:
  auto_start_profiles: false
```

When enabled, a request to a stopped profile may start the child server.

For v1, default should be `false` because model loading can be slow and resource-heavy.

### 15.4 Routing Security

When routing is enabled outside localhost:

- Require admin token or route token.
- Do not expose arbitrary local file paths in API responses.
- Support CORS only when explicitly configured.

---

## 16. Web UI Pages

### 16.1 Dashboard

Shows:

- Number of discovered models.
- Number of profiles.
- Running servers.
- Crashed servers.
- Recent benchmark results.
- Resource summary.
- Warnings such as missing `llama-server` binary.

### 16.2 Setup Page

Shows:

- `llama-server` binary path.
- Validation status.
- Version/help detection.
- Model scan directories.
- Hugging Face cache detection.
- Port range settings.
- Rescan button.

### 16.3 Model Catalog Page

Shows the model table described in section 9.

### 16.4 Profile List Page

Shows:

- Profile name.
- Model.
- Key args summary.
- Last run status.
- Last benchmark tokens/sec.
- Actions: Start, Stop, Restart, Edit, Clone, Export, Delete.

### 16.5 Profile Editor Page

Shows:

- Simple mode form.
- Advanced args editor.
- Command preview.
- Validation messages.
- Save and Run.
- Save as Clone.

### 16.6 Server Detail Page

Shows:

- Status.
- PID.
- Port.
- Start time.
- Profile snapshot.
- Logs.
- Stats charts.
- Quick test box.
- Benchmark controls.

### 16.7 Benchmark History Page

Shows:

- Runs by model/profile/date.
- Comparison table.
- Filters.
- Export benchmark JSON/CSV.

---

## 17. HTTP API

The Web UI should use a JSON HTTP API so the frontend stays simple and testable.

### 17.1 Health

```http
GET /api/health
```

Response:

```json
{
  "ok": true,
  "version": "0.1.0"
}
```

### 17.2 Settings

```http
GET /api/settings
PUT /api/settings
POST /api/settings/validate-llama
```

### 17.3 Models

```http
GET /api/models
POST /api/models/rescan
GET /api/models/{model_id}
POST /api/models/{model_id}/hide
```

### 17.4 Profiles

```http
GET /api/profiles
POST /api/profiles
GET /api/profiles/{profile_id}
PUT /api/profiles/{profile_id}
DELETE /api/profiles/{profile_id}
POST /api/profiles/{profile_id}/clone
GET /api/profiles/{profile_id}/export.json
GET /api/profiles/{profile_id}/export.sh
```

### 17.5 Managed Servers

```http
GET /api/servers
POST /api/profiles/{profile_id}/start
POST /api/servers/{server_id}/stop
POST /api/servers/{server_id}/restart
GET /api/servers/{server_id}
GET /api/servers/{server_id}/logs
GET /api/servers/{server_id}/stats
POST /api/servers/{server_id}/test
```

### 17.6 Benchmarks

```http
GET /api/benchmarks
POST /api/benchmarks
GET /api/benchmarks/{run_id}
DELETE /api/benchmarks/{run_id}
```

### 17.7 Optional Routing

```http
POST /profiles/{profile_id}/v1/chat/completions
POST /profiles/{profile_id}/v1/completions
POST /profiles/{profile_id}/v1/embeddings
GET  /profiles/{profile_id}/health
```

---

## 18. Storage Schema

SQLite is recommended for v1.

### 18.1 `models`

```sql
CREATE TABLE models (
  id TEXT PRIMARY KEY,
  path TEXT NOT NULL UNIQUE,
  resolved_path TEXT,
  display_name TEXT NOT NULL,
  source TEXT NOT NULL,
  repo_id TEXT,
  size_bytes INTEGER NOT NULL,
  modified_at TEXT,
  architecture TEXT,
  quantization TEXT,
  context_length INTEGER,
  embedding_length INTEGER,
  block_count INTEGER,
  tokenizer_model TEXT,
  chat_template TEXT,
  capabilities_json TEXT,
  metadata_json TEXT,
  hidden INTEGER NOT NULL DEFAULT 0,
  scanned_at TEXT NOT NULL
);
```

### 18.2 `profiles`

```sql
CREATE TABLE profiles (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT,
  model_id TEXT NOT NULL,
  args_json TEXT NOT NULL,
  env_json TEXT,
  working_dir TEXT,
  default_host TEXT NOT NULL DEFAULT '127.0.0.1',
  port_policy TEXT NOT NULL DEFAULT 'auto',
  fixed_port INTEGER,
  tags_json TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(model_id) REFERENCES models(id)
);
```

### 18.3 `servers`

```sql
CREATE TABLE servers (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL,
  model_id TEXT NOT NULL,
  pid INTEGER,
  host TEXT NOT NULL,
  port INTEGER NOT NULL,
  status TEXT NOT NULL,
  started_at TEXT,
  stopped_at TEXT,
  exit_code INTEGER,
  last_error TEXT,
  profile_snapshot_json TEXT NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(profile_id) REFERENCES profiles(id),
  FOREIGN KEY(model_id) REFERENCES models(id)
);
```

### 18.4 `stats_samples`

```sql
CREATE TABLE stats_samples (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  server_id TEXT NOT NULL,
  sampled_at TEXT NOT NULL,
  cpu_percent REAL,
  memory_rss_bytes INTEGER,
  request_count INTEGER,
  error_count INTEGER,
  avg_latency_ms REAL,
  p50_latency_ms REAL,
  p95_latency_ms REAL,
  tokens_per_second REAL,
  raw_json TEXT,
  FOREIGN KEY(server_id) REFERENCES servers(id)
);
```

### 18.5 `benchmark_runs`

```sql
CREATE TABLE benchmark_runs (
  id TEXT PRIMARY KEY,
  profile_id TEXT NOT NULL,
  server_id TEXT,
  model_id TEXT NOT NULL,
  prompt TEXT NOT NULL,
  params_json TEXT NOT NULL,
  result_json TEXT NOT NULL,
  profile_snapshot_json TEXT NOT NULL,
  started_at TEXT NOT NULL,
  completed_at TEXT,
  status TEXT NOT NULL,
  error TEXT,
  FOREIGN KEY(profile_id) REFERENCES profiles(id),
  FOREIGN KEY(server_id) REFERENCES servers(id),
  FOREIGN KEY(model_id) REFERENCES models(id)
);
```

---

## 19. Backend Implementation Plan

### 19.1 Language

Use Go.

### 19.2 Suggested Packages

Possible package layout:

```text
cmd/llama-server-studio/
internal/config/
internal/models/
internal/gguf/
internal/profiles/
internal/process/
internal/stats/
internal/bench/
internal/router/
internal/httpapi/
internal/storage/
web/
```

### 19.3 Process Supervisor

Use `os/exec`.

Requirements:

- Build command from profile.
- Inject host/port.
- Set working directory if configured.
- Set environment variables.
- Capture stdout/stderr.
- Track PID.
- Handle graceful shutdown.
- Handle process exit.
- Avoid zombie processes.

### 19.4 Port Allocation

For `auto` port policy:

- Use configured port range.
- Check against currently managed servers.
- Check OS port availability before start.
- Store selected port in server record.
- Do not mutate profile just because an auto port was selected.

### 19.5 Argument Builder

The argument builder should produce:

```go
type BuiltCommand struct {
    Executable string
    Args       []string
    Env        []string
    WorkDir    string
    Host       string
    Port       int
    Warnings   []string
}
```

Validation examples:

- Model path exists.
- Required model argument exists or can be generated.
- Fixed port is valid.
- Port is available.
- Duplicate host/port args are detected.
- Known conflicting args produce warnings.

### 19.6 GGUF Reader

Implement a minimal GGUF metadata reader.

Requirements:

- Read magic/version.
- Read tensor count and metadata key-value count.
- Read metadata key-value pairs.
- Stop before reading tensor data.
- Handle common scalar and array metadata types.
- Fail safely with clear error messages.

The scanner must keep the file visible even if metadata extraction partially fails.

### 19.7 Frontend

Keep frontend simple.

Acceptable options:

- Server-rendered HTML with HTMX.
- React/Vite single-page app.
- Plain Go templates and small JavaScript.

Recommended for v1:

```text
Go HTTP API + Vite/React frontend
```

But the project should avoid unnecessary state-management complexity.

---

## 20. Safety and Security

### 20.1 Localhost Default

Default binding must be `127.0.0.1`.

### 20.2 Path Safety

The application deals with local files and process execution.

Rules:

- Never accept arbitrary executable paths from unauthenticated remote clients.
- Do not expose full paths in optional public routing responses.
- Do not delete model files in v1.
- Do not allow profile args to execute shell commands.
- Use `exec.Command` with arg arrays, not shell string execution.

### 20.3 Tokens

When `--listen` is not localhost, require one of:

- `--admin-token`.
- Explicit `--allow-insecure-lan` flag.

### 20.4 Redaction

Redact likely secrets from logs and API responses:

- Environment variables containing `TOKEN`, `KEY`, `SECRET`, `PASSWORD`.
- Admin token.
- Authorization headers.

---

## 21. Error Handling

Errors should be readable and actionable.

Examples:

| Error | User-facing message |
| --- | --- |
| Missing llama-server binary | `llama-server executable was not found. Check Settings > llama.cpp path.` |
| Port unavailable | `Port 41001 is already in use. Choose another port or use auto port mode.` |
| Model file missing | `The selected GGUF file no longer exists.` |
| Server crashed | `llama-server exited with code 1. See logs for details.` |
| Metadata parse failed | `Model was found, but GGUF metadata could not be fully parsed.` |

---

## 22. MVP Requirements

The v1 MVP is complete when the user can:

1. Start `llama-server-studio` with a path to `llama-server`.
2. Add at least one model directory.
3. Scan and view `.gguf` files.
4. View basic GGUF metadata.
5. Create a profile from a model.
6. Edit simple and raw args.
7. Start a managed `llama-server` child process.
8. See status, PID, port, and logs.
9. Run a quick prompt test.
10. Stop the server.
11. Run a simple benchmark.
12. Export the profile to JSON and shell script.

---

## 23. v1.1 Candidates

After MVP:

- Profile import.
- Docker Compose export.
- More complete GPU telemetry.
- Compare benchmark charts.
- Auto-start routing.
- Multiple servers per profile.
- Profile templates for common hardware.
- Model notes and manual capability overrides.
- Read-only Prometheus endpoint from Studio.
- OpenAPI spec for Studio API.
- Authentication improvements.

---

## 24. Acceptance Tests

### 24.1 Startup

- Studio fails clearly when `llama-server` path is invalid.
- Studio starts when `llama-server --help` succeeds.
- Studio creates config and data directories if missing.

### 24.2 Model Scan

- Scanner finds `.gguf` files in a configured directory.
- Scanner finds `.gguf` files in a Hugging Face cache snapshot path.
- Scanner does not crash on broken symlinks.
- Scanner deduplicates the same resolved file path.
- Scanner shows a model even when metadata extraction partially fails.

### 24.3 Profile Builder

- User can create a profile from a model.
- Command preview includes the selected model path.
- Raw unknown args are preserved.
- Exported shell script runs with the expected command.

### 24.4 Process Control

- Start creates a child process.
- Stop terminates the child process.
- Crash is detected and shown.
- Logs are streamed and persisted.

### 24.5 Quick Test

- Quick test sends a request to the running child server.
- Response or error is shown clearly.
- Streaming response does not block the UI forever.

### 24.6 Benchmark

- Benchmark records tokens/sec or a clear reason why token rate was unavailable.
- Benchmark result is linked to profile and model.
- Benchmark history survives server restart.

### 24.7 Optional Routing

When enabled:

- `POST /profiles/{profile_id}/v1/chat/completions` proxies to the correct child server.
- Stopped profile returns 503.
- Missing profile returns 404.
- Streaming responses are preserved.

---

## 25. CLI Reference for Studio

Required/important flags:

```text
--listen string
    Studio bind address. Default: 127.0.0.1:3100

--config string
    Path to config file.

--data-dir string
    Path to data directory.

--llama-server-bin string
    Path to llama-server executable.

--llama-bin-dir string
    Path to llama.cpp binary directory.

--models-dir string
    Add a model scan directory. Can be repeated.

--scan-hf-cache bool
    Scan Hugging Face cache directories. Default: true.

--admin-token string
    Token required for remote admin access.

--allow-insecure-lan bool
    Allow non-localhost bind without token. Default: false.
```

---

## 26. Design Principles

- **Do not hide the command.** Always show the generated `llama-server` command.
- **Do not lock users into known flags.** Preserve raw args.
- **Do not mutate model files.** The catalog is observational in v1.
- **Prefer local safety.** Bind locally by default.
- **Make failures visible.** Logs and crash states are first-class.
- **Benchmark with snapshots.** Benchmark results must store the exact profile args used.
- **Keep it simple.** One Go server, local SQLite, child processes, and a clear UI.

---

## 27. Open Questions

These can be decided during implementation:

1. Should the setup UI be available when no valid `llama-server` path exists, or should the path be mandatory at process startup?
2. Should v1 use React/Vite or server-rendered templates?
3. Should benchmark prompts be stored as reusable presets?
4. Should Studio expose its own Prometheus endpoint in v1 or v1.1?
5. Should routing require a separate route token from the admin token?
6. Should model metadata overrides be stored in v1?
7. Should the studio support multiple active servers per profile in a future version?

---

## 28. Recommended First Milestone

Build the smallest vertical slice:

1. CLI config with `--llama-server-bin`.
2. SQLite setup.
3. Model directory scanner.
4. Model catalog table.
5. Create profile from model.
6. Start/stop child `llama-server`.
7. Log viewer.
8. Quick test prompt.

After this works, add benchmarking and optional profile-id routing.
