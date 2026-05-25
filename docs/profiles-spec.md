# Llama Server Studio — Profiles Specification v1

Status: Draft v1  
Target: `llama-server` profile builder, launcher, validator, process manager, benchmark runner, and optional traffic router.

## 1. Purpose

A **profile** is a reusable, named configuration for starting one `llama-server` process or one router-mode `llama-server` process.

The profile builder must make `llama-server` usable without exposing every command-line option at once. It should support two levels:

1. **Simple Args**: common, safe, high-impact options that most users need.
2. **Advanced Args**: hardware-specific, model-specific, security-sensitive, or experimental options.

The profile builder should produce:

- a structured profile JSON document;
- a generated `llama-server` command preview;
- launch-time validation warnings/errors;
- runtime metadata for monitoring and benchmark comparison;
- optional routing metadata so Studio can forward inference traffic by profile id.

## 2. Core design principle

Do **not** store profiles only as shell command strings.

Store the profile as structured data and generate CLI args from that structure.

Why:

- the UI can explain each option;
- validation is easier;
- profiles can be migrated when `llama-server` flags change;
- request defaults can be separated from process args;
- advanced raw args remain possible without making the entire UI a raw text box.

## 3. Key distinction: process args vs request defaults

The profile builder must separate these concepts:

### Process Args

These control how `llama-server` starts.

Examples:

- model path;
- host and port;
- context size;
- GPU layers;
- parallel slots;
- metrics endpoint;
- Web UI on/off.

### Request Defaults

These control how Studio's built-in test chat, benchmark runner, or local proxy sends inference requests.

Examples:

- temperature;
- top-p;
- min-p;
- max tokens;
- stream on/off;
- seed.

Important: external OpenAI-compatible clients may send their own request parameters. Those client parameters can override Studio's request defaults. For that reason, the UI must not imply that request defaults are guaranteed for all downstream clients.

## 4. Profile modes

A profile can run in one of two mutually exclusive modes.

### 4.1 Single Model Mode

Studio starts one `llama-server` process with one model.

Use this mode for:

- normal local chat;
- testing one GGUF model;
- benchmarking one model/profile combination;
- running one embedding model;
- running one rerank model;
- simple production-like API serving.

Single Model Mode uses one of these model sources:

- local GGUF file: `--model` / `-m`;
- Hugging Face repo: `--hf-repo` / `-hf`;
- Hugging Face file override: `--hf-file` / `-hff`;
- direct model URL: `--model-url` / `-mu`.

### 4.2 Router Mode

Studio starts `llama-server` without a single model. The server becomes a router that can load and unload models dynamically.

Use this mode for:

- one HTTP server serving multiple models;
- experimenting with cache-based model loading;
- model catalog browsing through server endpoints;
- future multi-model routing.

Router Mode uses:

- `--models-dir`;
- `--models-preset`;
- `--models-max`;
- `--models-autoload` / `--no-models-autoload`.

Rule: do not combine Single Model Mode fields such as `model.path` with Router Mode fields unless the user intentionally uses raw extra args. The UI should block this by default.

## 5. Simple vs Advanced strategy

### 5.1 Simple Args definition

Simple Args are shown in the normal profile builder.

A field belongs in Simple Args when:

- most users understand the decision;
- it is needed to launch a working server;
- changing it has predictable effects;
- it is useful for comparing profiles;
- it does not require deep hardware or model-template knowledge.

### 5.2 Advanced Args definition

Advanced Args are hidden under an expandable Advanced section.

A field belongs in Advanced Args when:

- wrong values can break model quality;
- wrong values can exhaust VRAM/RAM;
- it is hardware-topology-specific;
- it is security-sensitive;
- it is experimental;
- it is only useful for a narrow model class;
- it is better handled by raw args until Studio has a good UI for it.

### 5.3 Raw args escape hatch

Always provide a raw extra args field:

```txt
advanced.extraArgs: string[]
```

Rules:

- raw extra args are appended last;
- the command preview must clearly mark them;
- validation should warn when raw args conflict with structured fields;
- raw args should be searchable in profile list;
- raw args must not be silently removed.

## 6. Recommended profile JSON shape

```json
{
  "schemaVersion": 1,
  "id": "qwen3-8b-local-fast",
  "name": "Qwen3 8B Local Fast",
  "description": "Fast local chat profile for daily testing.",
  "tags": ["chat", "local", "gpu"],
  "enabled": true,
  "mode": "single",

  "launcher": {
    "llamaServerPath": "/opt/llama.cpp/build/bin/llama-server",
    "workingDirectory": "/opt/llama.cpp",
    "environment": {},
    "inheritSystemEnvironment": true
  },

  "model": {
    "source": "local",
    "path": "/models/qwen3-8b-q4_k_m.gguf",
    "modelUrl": null,
    "hfRepo": null,
    "hfFile": null,
    "hfTokenRef": null,
    "alias": "qwen3-8b",
    "modelTags": ["chat", "q4_k_m"]
  },

  "server": {
    "host": "127.0.0.1",
    "port": 8081,
    "apiPrefix": "",
    "enableUi": false,
    "timeoutSeconds": 600,
    "httpThreads": null
  },

  "runtime": {
    "ctxSize": 8192,
    "nPredict": null,
    "threads": -1,
    "threadsBatch": null,
    "batchSize": null,
    "ubatchSize": null,
    "gpuLayers": "auto",
    "fit": "on",
    "flashAttention": "auto"
  },

  "concurrency": {
    "parallelSlots": -1,
    "continuousBatching": true
  },

  "capabilities": {
    "apiUseCase": "chat",
    "embeddingsOnly": false,
    "rerank": false
  },

  "monitoring": {
    "metrics": true,
    "slots": true,
    "props": false
  },

  "requestDefaults": {
    "temperature": 0.8,
    "topK": 40,
    "topP": 0.95,
    "minP": 0.05,
    "maxTokens": 1024,
    "seed": -1,
    "stream": true
  },

  "advanced": {
    "hardware": {},
    "memory": {},
    "rope": {},
    "template": {},
    "multimodal": {},
    "lora": {},
    "security": {},
    "router": {},
    "speculative": {},
    "logging": {},
    "extraArgs": []
  }
}
```

## 7. UI layout

Recommended profile builder layout:

```txt
Profile Builder
├── Overview
│   ├── Profile name
│   ├── Description
│   ├── Tags
│   └── Mode: Single Model / Router
│
├── Model
│   ├── Source: Local / Hugging Face / URL
│   ├── Model path / HF repo / HF file / URL
│   ├── Alias
│   └── Model tags
│
├── Server
│   ├── Host
│   ├── Port
│   ├── API prefix
│   ├── Enable llama-server UI
│   └── Timeout
│
├── Runtime
│   ├── Context size
│   ├── GPU layers
│   ├── Fit to memory
│   ├── Threads
│   ├── Batch settings
│   └── Flash Attention
│
├── Concurrency
│   ├── Parallel slots
│   └── Continuous batching
│
├── Capabilities
│   ├── Chat / Completion
│   ├── Embeddings
│   └── Rerank
│
├── Monitoring
│   ├── Metrics
│   ├── Slots endpoint
│   └── Props endpoint
│
├── Request Defaults
│   ├── Temperature
│   ├── Top-k
│   ├── Top-p
│   ├── Min-p
│   ├── Max tokens
│   ├── Seed
│   └── Streaming
│
├── Advanced
│   ├── Hardware
│   ├── Memory / KV cache
│   ├── RoPE / long context
│   ├── Template / reasoning / Jinja
│   ├── Multimodal
│   ├── LoRA / control vectors
│   ├── Security
│   ├── Router mode
│   ├── Speculative decoding
│   ├── Logging
│   └── Raw extra args
│
└── Preview & Launch
    ├── Generated command
    ├── Validation results
    ├── Save profile
    ├── Start server
    └── Quick test
```

## 8. Simple Args implementation table

### 8.1 Profile metadata

These are Studio fields, not `llama-server` CLI flags.

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `id` | string | yes | none | Stable internal profile id. Use slug format. | Always. | Never. Generate automatically from name if user does not provide one. |
| `name` | string | yes | none | Human-readable profile name. | Always. | Never. |
| `description` | string | no | none | Notes explaining purpose and tradeoff. | Useful when many profiles use the same model with different settings. | Skip for quick experiments. |
| `tags` | string[] | no | none | Studio-level tags for filtering profiles. | Use for `chat`, `embedding`, `gpu`, `cpu`, `benchmark`, `production`. | Skip if profile list is small. |
| `enabled` | bool | yes | none | Whether profile can be launched or routed. | Usually true. | Set false for archived/broken profiles. |

### 8.2 Launcher

These are Studio process-runner fields, not `llama-server` CLI flags.

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `launcher.llamaServerPath` | path | yes | executable | Path to `llama-server` binary. | Always before launching. | Never. Profile cannot start without it unless Studio has a global default. |
| `launcher.workingDirectory` | path | no | process cwd | Working directory used when spawning the child process. | Set when relative paths, logs, or assets depend on cwd. | Leave empty to use Studio's default cwd. |
| `launcher.environment` | map | no | env vars | Extra environment variables for this process. | Use for CUDA/Metal/ROCm tuning, `LLAMA_CACHE`, or controlled secrets references. | Leave empty for normal use. |
| `launcher.inheritSystemEnvironment` | bool | yes | env behavior | Whether child process inherits parent env. | True for normal local dev. | False for stricter reproducible launches. |

### 8.3 Model source

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `mode` | enum `single/router` | yes | generated behavior | Selects single model or router server. | Use `single` for one model per process. Use `router` for multi-model loading. | Do not mix both in one profile. |
| `model.source` | enum `local/hf/url` | yes in single mode | generated behavior | Determines which model source fields are active. | Always in single mode. | Not used in router mode. |
| `model.path` | path | conditional | `-m`, `--model` | Local GGUF model path. | Use for manually downloaded GGUF files and stable production paths. | Leave empty if using Hugging Face repo, model URL, or router mode. |
| `model.modelUrl` | URL | no | `-mu`, `--model-url` | Remote HTTP URL to download/load model. | Use for controlled internal artifact URLs. | Avoid for normal use; local path or HF repo is usually more reproducible. |
| `model.hfRepo` | string | conditional | `-hf`, `--hf-repo` | Hugging Face model repository, optionally with quant tag. | Use when the model should be pulled from HF/cache. | Leave empty for local GGUF path. |
| `model.hfFile` | string | no | `-hff`, `--hf-file` | Specific model file inside HF repo. | Use when a repo has many GGUF quant files and you need exact file selection. | Leave empty if repo quant tag is enough or auto selection is acceptable. |
| `model.hfTokenRef` | secret ref | no | `-hft`, `--hf-token` or env `HF_TOKEN` | Reference to stored HF token. Do not store the token plaintext in profile JSON. | Use for private/gated HF models. | Leave empty for public models or when token is already available in environment. |
| `model.alias` | string | recommended | `-a`, `--alias` | API model id returned by `/v1/models` and used by clients. | Set for stable client-facing model names like `qwen3-8b` or `local-chat`. | Leave empty only for quick tests where raw file path as id is acceptable. |
| `model.modelTags` | string[] | no | `--tags` | Informational model tags reported by server. | Use for metadata and UI display. | Leave empty if Studio tags are enough. |

### 8.4 Server

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `server.host` | string | yes | `--host` | IP address or UNIX socket to listen on. Default should be `127.0.0.1`. | Use `127.0.0.1` for local-only. Use `0.0.0.0` only when serving LAN/container traffic. | Never expose `0.0.0.0` without thinking about API key/firewall. |
| `server.port` | int | yes | `--port` | HTTP port. | Set unique port per running profile. | Do not reuse ports unless explicitly using advanced `--reuse-port`. |
| `server.apiPrefix` | string | no | `--api-prefix` | Prefix path without trailing slash. | Use behind reverse proxies, subpath deployments, or Studio routing. Example: `/llama`. | Leave empty for direct local use. |
| `server.enableUi` | bool | yes | `--ui` / `--no-ui` | Enables llama-server's built-in Web UI. | Enable for direct manual testing in llama-server UI. | Disable when Studio provides its own UI or in production-like serving. |
| `server.timeoutSeconds` | int | no | `-to`, `--timeout` | Server read/write timeout. | Increase for very long generations, slow clients, or large prefill requests. | Leave default for normal chat. |
| `server.httpThreads` | int | no | `--threads-http` | Number of threads used for HTTP request handling. | Set when many clients connect concurrently and HTTP handling becomes a bottleneck. | Leave auto/default for normal local usage. |

### 8.5 Runtime

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `runtime.ctxSize` | int | recommended | `-c`, `--ctx-size` | Prompt context size in tokens. `0` means load from model. | Set when you know your target context: `4096`, `8192`, `16384`, etc. | Use `0` or empty to trust model metadata and reduce bad long-context settings. |
| `runtime.nPredict` | int | no | `-n`, `--predict`, `--n-predict` | Default number of tokens to predict. `-1` means unlimited. | Set for CLI/server default caps or benchmark reproducibility. | Prefer request-level `maxTokens` for Studio test chat. |
| `runtime.threads` | int | no | `-t`, `--threads` | CPU threads used during generation. `-1` means auto. | Set for CPU-only profiles or when auto uses too many/few threads. | Leave `-1` for normal GPU or unknown systems. |
| `runtime.threadsBatch` | int | no | `-tb`, `--threads-batch` | CPU threads for prompt/batch processing. | Tune when prompt processing is CPU-heavy. | Leave empty to use same behavior as `threads`. |
| `runtime.batchSize` | int | no | `-b`, `--batch-size` | Logical maximum batch size for prompt processing. | Tune for throughput benchmarks or high-concurrency workloads. | Leave default for simple chat. |
| `runtime.ubatchSize` | int | no | `-ub`, `--ubatch-size` | Physical micro-batch size. | Reduce if VRAM is tight; increase only after measuring. | Leave default for normal use. |
| `runtime.gpuLayers` | int/string | recommended | `-ngl`, `--n-gpu-layers`, `--gpu-layers` | Number of layers to store in VRAM. Accept `auto`, `all`, or exact number. | Use `auto` for most GPU setups. Use exact number when avoiding OOM. Use `all` for full offload if VRAM allows. | Use `0` or device `none` for CPU-only. |
| `runtime.fit` | enum `on/off` | recommended | `-fit`, `--fit` | Adjusts unset arguments to fit in device memory. | Keep `on` for most users; it helps avoid bad memory choices. | Turn `off` only for controlled benchmarking where exact args must not change. |
| `runtime.flashAttention` | enum `auto/on/off` | recommended | `-fa`, `--flash-attn` | Flash Attention mode. | Use `auto` normally. Try `on` for supported GPUs/models when optimizing. Try `off` if output, compatibility, or performance is suspicious. | Leave `auto` unless benchmarking. |

### 8.6 Concurrency

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `concurrency.parallelSlots` | int | recommended | `-np`, `--parallel` | Number of server slots for processing requests. `-1` means auto. | Set `1` for single-user lowest memory. Set `2+` for multi-user or parallel benchmark. | Leave `-1` if you trust auto. |
| `concurrency.continuousBatching` | bool | yes | `-cb`, `--cont-batching` / `-nocb`, `--no-cont-batching` | Enables dynamic/continuous batching. | Keep enabled for server workloads and multiple requests. | Disable only for debugging, latency experiments, or reproducing old behavior. |

### 8.7 Capabilities

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `capabilities.apiUseCase` | enum `chat/completion/embeddings/rerank` | yes | generated behavior | Studio-level intent for UI, testing, and validation. | Always choose the main use case. | Do not treat this alone as a server flag. It controls which flags are emitted. |
| `capabilities.embeddingsOnly` | bool | conditional | `--embedding`, `--embeddings` | Restricts server to embedding use case. | Use only with dedicated embedding models. | Do not enable for normal chat models. |
| `capabilities.rerank` | bool | conditional | `--rerank`, `--reranking` | Enables reranking endpoint. | Use only with rerank-capable models. | Do not enable for normal chat unless you know model supports reranking. |

Generation rule:

- If `apiUseCase = embeddings`, set `embeddingsOnly = true` and emit `--embeddings`.
- If `apiUseCase = rerank`, set `rerank = true` and emit `--rerank`.
- If `apiUseCase = chat` or `completion`, emit neither by default.

### 8.8 Monitoring

| Field | Type | Required | CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `monitoring.metrics` | bool | recommended | `--metrics` | Enables Prometheus-compatible `/metrics` endpoint. | Enable for Studio stats, benchmark history, dashboards, and production-like monitoring. | Disable only if you want smaller exposed surface. |
| `monitoring.slots` | bool | recommended | `--slots` / `--no-slots` | Exposes `/slots` monitoring endpoint. Enabled by default in llama-server. | Keep enabled for Studio live slot view and per-request debugging. | Disable for tighter API surface or when not using Studio monitoring. |
| `monitoring.props` | bool | no | `--props` | Enables changing global properties via `POST /props`. | Use only when Studio intentionally supports live mutation. | Leave disabled for predictable profiles. |

### 8.9 Request defaults

These values should be used by Studio test chat and benchmark requests. They may also be emitted as CLI sampling defaults only if Studio has a compatibility mode that intentionally sets launch-time defaults.

| Field | Type | Required | Request field / CLI flag | Description | Set when | Leave empty/default when |
|---|---:|---:|---|---|---|---|
| `requestDefaults.temperature` | float | no | `temperature` / `--temp` | Randomness. Lower = more deterministic. Higher = more creative. | Use `0.1–0.3` for deterministic tasks. Use `0.7–1.0` for chat/creative. | Leave default if the client controls sampling. |
| `requestDefaults.topK` | int | no | `top_k` / `--top-k` | Limits sampling to top K tokens. | Tune for benchmark reproducibility or if output feels too random. | Leave default for most chat. |
| `requestDefaults.topP` | float | no | `top_p` / `--top-p` | Nucleus sampling cutoff. | Lower for focused output, higher for more diversity. | Leave default unless tuning generation style. |
| `requestDefaults.minP` | float | no | `min_p` / `--min-p` | Relative probability floor. | Useful for modern local LLM sampling with good balance. | Set `0` to disable only if comparing old sampling behavior. |
| `requestDefaults.maxTokens` | int | no | `max_tokens` / `n_predict` | Maximum generated tokens per Studio request. | Always set for quick tests and benchmarks to avoid runaway generation. | Leave empty only when caller must decide. |
| `requestDefaults.seed` | int | no | `seed` / `--seed` | RNG seed. `-1` means random. | Set a fixed seed for reproducible comparisons. | Use `-1` for normal chat. |
| `requestDefaults.stream` | bool | no | `stream` | Enables streaming response in Studio test chat. | Enable for chat UX and latency feel. | Disable for exact benchmark timing or simpler response handling. |

## 9. Advanced Args implementation table

Advanced fields should be grouped and searchable. Each group can begin collapsed.

### 9.1 Advanced: CPU placement and scheduling

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.hardware.cpuMask` | `-C`, `--cpu-mask` | CPU affinity mask. | You need fixed CPU core placement for benchmarking or NUMA tuning. | Normal users should not touch this. |
| `advanced.hardware.cpuRange` | `-Cr`, `--cpu-range` | CPU core range for affinity. | Easier than mask when pinning to a range. | Avoid if you do not know CPU topology. |
| `advanced.hardware.cpuStrict` | `--cpu-strict` | Strict CPU placement. | Controlled benchmark or isolation. | General local use. |
| `advanced.hardware.priority` | `--prio` | Process/thread priority. | Dedicated inference machine. | Shared desktop; realtime/high can harm system responsiveness. |
| `advanced.hardware.poll` | `--poll` | Polling level for work wait. | Latency experiments. | Battery/desktop use where CPU waste matters. |
| `advanced.hardware.cpuMaskBatch` | `-Cb`, `--cpu-mask-batch` | CPU mask for batch/prompt processing. | Advanced CPU performance isolation. | Normal use. |
| `advanced.hardware.cpuRangeBatch` | `-Crb`, `--cpu-range-batch` | CPU range for batch processing. | Advanced CPU performance isolation. | Normal use. |
| `advanced.hardware.cpuStrictBatch` | `--cpu-strict-batch` | Strict batch CPU placement. | Controlled benchmark. | Normal use. |
| `advanced.hardware.priorityBatch` | `--prio-batch` | Batch processing priority. | Controlled benchmark. | Normal use. |
| `advanced.hardware.pollBatch` | `--poll-batch` | Batch polling behavior. | Latency experiment. | Normal use. |
| `advanced.hardware.numa` | `--numa` | NUMA optimization mode. | Multi-socket or NUMA systems. | Single-socket consumer machines. |

### 9.2 Advanced: GPU and multi-GPU

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.hardware.device` | `-dev`, `--device` | Comma-separated offload devices; `none` disables offload. | You need to choose specific GPU/backend devices. | Single-GPU systems where auto works. |
| `advanced.hardware.splitMode` | `-sm`, `--split-mode` | Multi-GPU split strategy: `none`, `layer`, `row`, `tensor`. | Multi-GPU tuning. | Single GPU or CPU-only. |
| `advanced.hardware.tensorSplit` | `-ts`, `--tensor-split` | Proportions of model offload per GPU. | GPUs have different VRAM sizes. | Uniform single GPU or when `fit` handles it. |
| `advanced.hardware.mainGpu` | `-mg`, `--main-gpu` | Main GPU index. | Multi-GPU setups needing explicit primary GPU. | Single-GPU auto mode. |
| `advanced.hardware.fitTarget` | `-fitt`, `--fit-target` | Target memory margin per device for `--fit`. | You want more/less VRAM headroom than default. | Normal users; default is safer. |
| `advanced.hardware.fitCtx` | `-fitc`, `--fit-ctx` | Minimum ctx size allowed by `--fit`. | You want `fit` to preserve at least a target context. | Normal use. |
| `advanced.hardware.opOffload` | `--op-offload` / `--no-op-offload` | Offload host tensor operations to device. | Debugging backend performance. | Leave default unless measuring. |
| `advanced.hardware.noHost` | `--no-host` | Bypass host buffer. | Specialized memory tuning. | Normal use. |

### 9.3 Advanced: Memory and KV cache

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.memory.kvOffload` | `--kv-offload` / `--no-kv-offload` | Enables KV cache offloading. | Keep default enabled for GPU workloads. Disable for debugging or CPU-only memory experiments. | Do not change without measuring. |
| `advanced.memory.cacheTypeK` | `-ctk`, `--cache-type-k` | KV cache K data type. | Need lower memory use for long context. | Do not change when quality/stability matters and memory is enough. |
| `advanced.memory.cacheTypeV` | `-ctv`, `--cache-type-v` | KV cache V data type. | Need lower memory use for long context. | Do not change casually; can affect quality/perf. |
| `advanced.memory.mlock` | `--mlock` | Keep model in RAM instead of swapping/compressing. | Dedicated machine with enough RAM; avoid swap stalls. | Low-RAM systems; can make system less responsive. |
| `advanced.memory.mmap` | `--mmap` / `--no-mmap` | Memory-map model file. Default enabled. | Keep enabled for fast load and efficient file-backed memory. Disable only if pageout behavior is bad. | Do not disable without a reason. |
| `advanced.memory.directIo` | `--direct-io` / `--no-direct-io` | Use DirectIO if available. | Storage benchmarking or special IO setup. | Normal local model loading. |
| `advanced.memory.cacheRamMiB` | `-cram`, `--cache-ram` | Maximum prompt/cache RAM in MiB. | You want bounded cache memory. | Normal simple chat. |
| `advanced.memory.kvUnified` | `--kv-unified` / `--no-kv-unified` | Single unified KV buffer shared across sequences. | Advanced server cache behavior. | Leave default unless troubleshooting slots/cache. |
| `advanced.memory.cacheIdleSlots` | `--cache-idle-slots` / `--no-cache-idle-slots` | Save and clear idle slots on new task. | Long-running multi-user server with prompt cache behavior. | Normal single-user chat. |
| `advanced.memory.cachePrompt` | `--cache-prompt` / `--no-cache-prompt` | Prompt caching. Default enabled. | Keep enabled for repeated prompts/system prompts. | Disable when testing cold-start prompt timing. |
| `advanced.memory.cacheReuse` | `--cache-reuse` | Minimum chunk size for cache reuse via KV shifting. | Advanced caching optimization. | Normal use. |
| `advanced.memory.slotSavePath` | `--slot-save-path` | Path for saving/restoring slot KV cache. | You need explicit slot cache persistence. | Normal use. |

### 9.4 Advanced: Long context and RoPE

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.rope.ropeScaling` | `--rope-scaling` | RoPE scaling method: `none`, `linear`, `yarn`. | You know model requires a specific scaling method. | Leave model metadata/defaults alone. |
| `advanced.rope.ropeScale` | `--rope-scale` | Context scaling factor. | Long-context experiments with known-compatible model. | Normal chat; wrong values can degrade quality. |
| `advanced.rope.ropeFreqBase` | `--rope-freq-base` | RoPE base frequency. | Model card explicitly recommends it. | Guessing. |
| `advanced.rope.ropeFreqScale` | `--rope-freq-scale` | RoPE frequency scale. | Model card explicitly recommends it. | Guessing. |
| `advanced.rope.yarnOrigCtx` | `--yarn-orig-ctx` | Original model context for YaRN. | YaRN long-context tuning. | Non-YaRN models. |
| `advanced.rope.yarnExtFactor` | `--yarn-ext-factor` | YaRN extrapolation mix. | Advanced long-context tuning. | Normal use. |
| `advanced.rope.yarnAttnFactor` | `--yarn-attn-factor` | YaRN attention magnitude factor. | Advanced long-context tuning. | Normal use. |
| `advanced.rope.yarnBetaSlow` | `--yarn-beta-slow` | YaRN correction dim alpha. | Advanced long-context tuning. | Normal use. |
| `advanced.rope.yarnBetaFast` | `--yarn-beta-fast` | YaRN correction dim beta. | Advanced long-context tuning. | Normal use. |
| `advanced.rope.swaFull` | `--swa-full` | Full-size SWA cache. | Model/benchmark requires it. | Normal use. |

### 9.5 Advanced: Prompt, template, reasoning, special behavior

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.template.jinja` | `--jinja` / related template behavior if supported by current binary | Enables Jinja chat templating when supported. | Tool calling or model chat template requires Jinja. | If built-in model template already works. |
| `advanced.template.chatTemplate` | `--chat-template` if supported by current binary | Inline chat template. | Model lacks correct template metadata or user wants custom formatting. | Normal GGUFs with correct embedded template. |
| `advanced.template.chatTemplateFile` | `--chat-template-file` if supported by current binary | Chat template file path. | Large custom template should be versioned. | Normal use. |
| `advanced.template.chatTemplateKwargs` | `--chat-template-kwargs` | JSON kwargs for template parser. | Template requires extra params. | If you do not know template contract. |
| `advanced.template.escape` | `--escape` / `--no-escape` | Process escape sequences. | Prompt formatting tests. | Normal chat. |
| `advanced.template.keep` | `--keep` | Number of initial prompt tokens to keep. | Long interactive sessions or special cache behavior. | Normal request/response server usage. |
| `advanced.template.contextShift` | `--context-shift` / `--no-context-shift` | Infinite generation context shifting. | Infinite/continuous text generation. | Normal chat/completions. |
| `advanced.template.reversePrompt` | `-r`, `--reverse-prompt` | Stop/halt generation at prompt. | Interactive CLI-like workflows. | OpenAI-compatible chat. |
| `advanced.template.specialTokens` | `-sp`, `--special` | Output special tokens. | Debug tokenizer/template behavior. | User-facing chat. |
| `advanced.template.spmInfill` | `--spm-infill` | Suffix/Prefix/Middle infill order. | Infill model expects this format. | Chat models. |

### 9.6 Advanced: Sampling internals

These can be shown under Request Defaults > Advanced Sampling. They can be sent per request and optionally emitted as CLI defaults.

| Field | CLI/request field | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.sampling.samplers` | `--samplers`, `samplers` | Full sampler order. | Reproducing exact llama.cpp behavior. | Normal users; order is easy to misconfigure. |
| `advanced.sampling.samplerSeq` | `--sampler-seq` | Simplified sampler sequence. | Comparing sampler algorithms. | Normal use. |
| `advanced.sampling.ignoreEos` | `--ignore-eos`, `ignore_eos` | Ignore EOS and keep generating. | Testing or forced continuation. | Normal chat; can cause runaway output. |
| `advanced.sampling.typicalP` | `--typical`, `typical_p` | Locally typical sampling. | Experimenting with old/alternative sampling. | Normal use. |
| `advanced.sampling.repeatLastN` | `--repeat-last-n`, `repeat_last_n` | Token window for repeat penalty. | Output repeats too much. | If no repetition problem. |
| `advanced.sampling.repeatPenalty` | `--repeat-penalty`, `repeat_penalty` | Repetition penalty strength. | Output loops or repeats. | Leave `1.0` if output is fine. |
| `advanced.sampling.presencePenalty` | `--presence-penalty`, `presence_penalty` | Penalizes repeated presence. | Need more topic diversity. | Deterministic factual tasks. |
| `advanced.sampling.frequencyPenalty` | `--frequency-penalty`, `frequency_penalty` | Penalizes frequent tokens. | Output overuses phrases. | Normal chat. |
| `advanced.sampling.dryMultiplier` | `--dry-multiplier`, `dry_multiplier` | DRY anti-repetition multiplier. | Strong repetition/looping issues. | Normal first attempt. |
| `advanced.sampling.mirostat` | `mirostat` | Mirostat sampling mode. | Advanced entropy control. | If using normal top-p/min-p tuning. |
| `advanced.sampling.grammar` | request `grammar` | Grammar-constrained generation. | Need strict output format. | Normal chat. |
| `advanced.sampling.jsonSchema` | request `json_schema` | JSON schema constrained output. | Need validated structured JSON. | Free-form chat. |

### 9.7 Advanced: Multimodal

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.multimodal.mmproj` | `-mm`, `--mmproj` | Multimodal projector file path. | Local vision/multimodal model requires separate projector. | Text-only models. |
| `advanced.multimodal.mmprojUrl` | `-mmu`, `--mmproj-url` | URL for multimodal projector. | Controlled remote projector artifact. | Text-only or HF auto-download. |
| `advanced.multimodal.mmprojAuto` | `--mmproj-auto` / `--no-mmproj` | Auto-use projector if available. | Keep enabled for HF multimodal models. | Disable if it loads wrong projector or for text-only testing. |
| `advanced.multimodal.mmprojOffload` | `--mmproj-offload` / `--no-mmproj-offload` | GPU offload for projector. | Keep default for performance. | Disable for compatibility or VRAM troubleshooting. |
| `advanced.multimodal.imageMinTokens` | `--image-min-tokens` | Minimum tokens per image for dynamic-resolution vision models. | Model card recommends it. | Normal use. |
| `advanced.multimodal.imageMaxTokens` | `--image-max-tokens` | Maximum tokens per image. | Need cap for memory/performance. | Text-only models. |
| `advanced.multimodal.mediaPath` | `--media-path` | Directory for local media files via relative `file://` URLs. | Controlled local media testing. | Untrusted environments. |

### 9.8 Advanced: Embeddings and rerank details

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.embeddings.pooling` | `--pooling` | Pooling type for embeddings: `none`, `mean`, `cls`, `last`, `rank`. | Embedding/rerank model requires a specific pooling. | Leave model default unless docs say otherwise. |
| `advanced.embeddings.embdNormalize` | `--embd-normalize` | Embedding normalization mode. | Matching vector DB expectations or benchmark reproducibility. | Normal embedding use where default works. |

### 9.9 Advanced: LoRA and control vectors

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.lora.adapters` | `--lora` | One or more LoRA adapter files. | You want to modify model behavior with adapters. | Base model only. |
| `advanced.lora.scaledAdapters` | `--lora-scaled` | LoRA files with explicit scale. | Need weighted adapter strength. | If scale `1.0` is fine. |
| `advanced.lora.initWithoutApply` | `--lora-init-without-apply` if supported | Load adapters but start at scale 0. | UI will dynamically enable adapters per request. | Static adapter profiles. |
| `advanced.controlVectors.files` | `--control-vector` | Control vector files. | You use control vectors for steering. | Normal users. |
| `advanced.controlVectors.scaled` | `--control-vector-scaled` | Control vector with explicit scale. | Need precise steering strength. | Normal users. |
| `advanced.controlVectors.layerRange` | `--control-vector-layer-range` | Layer range for control vectors. | Control vector documentation requires it. | Guessing. |

### 9.10 Advanced: Security

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.security.apiKeys` | `--api-key` | One or more API keys. Store as secret references, not plaintext in profile. | Host is not strictly local, or multiple apps/users access the server. | Local-only throwaway tests. |
| `advanced.security.apiKeyFile` | `--api-key-file` | Path to file containing API keys. | Production-like local deployment. | If Studio secret store is enough. |
| `advanced.security.sslKeyFile` | `--ssl-key-file` | SSL private key. | Direct HTTPS serving. | If using reverse proxy TLS. |
| `advanced.security.sslCertFile` | `--ssl-cert-file` | SSL certificate. | Direct HTTPS serving. | If using reverse proxy TLS. |
| `advanced.security.uiMcpProxy` | `--ui-mcp-proxy` / `--no-ui-mcp-proxy` | Experimental MCP CORS proxy. | Trusted local development only. | Untrusted networks or production. |
| `advanced.security.tools` | `--tools` | Built-in tools such as file read/search or shell command if available. | Trusted local agent experiments. | Any untrusted environment; this can expose local system capabilities. |

Validation rules:

- If `server.host = 0.0.0.0` and no API key is configured, show a high-severity warning.
- If `tools` includes shell/file-write capabilities, show a high-severity warning.
- If `uiMcpProxy = true`, show a high-severity warning.
- Do not show secret values in command preview. Show `--api-key ***` or prefer `--api-key-file`.

### 9.11 Advanced: Router mode

Only active when `mode = router`.

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.router.modelsDir` | `--models-dir` | Directory containing models for router server. | You maintain a local model directory. | Single Model Mode. |
| `advanced.router.modelsPreset` | `--models-preset` | INI file containing model presets. | You want named model presets with model-specific args. | Simple one-model profile. |
| `advanced.router.modelsMax` | `--models-max` | Maximum simultaneously loaded models. | Need memory control. | Leave default if unsure. |
| `advanced.router.modelsAutoload` | `--models-autoload` / `--no-models-autoload` | Automatically load models. | Enable for convenience. Disable for manual loading. | Single Model Mode. |

Router validation:

- Block launch when `mode = router` and `model.path`, `model.hfRepo`, or `model.modelUrl` are set, unless user confirms raw advanced behavior.
- Show that some args are router-level and some are model-instance-level.
- For traffic routing by profile id, Studio should route to the router profile itself or to model ids known by the router, not to child processes that Studio did not spawn directly.

### 9.12 Advanced: Speculative decoding

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.speculative.type` | `--spec-type` | Speculative decoding type. | Benchmarking acceleration modes. | Normal first launch. |
| `advanced.speculative.draftModel` | `--model-draft` | Draft model path. | You have a compatible smaller draft model. | If model compatibility is unknown. |
| `advanced.speculative.draftGpuLayers` | `--n-gpu-layers-draft` | GPU layers for draft model. | Draft model should use GPU. | CPU-only or if VRAM is tight. |
| `advanced.speculative.draftDevice` | `--device-draft` | Devices for draft model. | Multi-GPU draft/main split. | Single default GPU. |
| `advanced.speculative.draftCacheTypeK` | `--cache-type-k-draft` | K cache type for draft model. | Memory tuning. | Normal use. |
| `advanced.speculative.draftCacheTypeV` | `--cache-type-v-draft` | V cache type for draft model. | Memory tuning. | Normal use. |
| `advanced.speculative.nMax` | `--spec-draft-n-max` | Maximum draft tokens. | Speculative benchmark tuning. | Normal use. |
| `advanced.speculative.nMin` | `--spec-draft-n-min` | Minimum draft tokens. | Speculative benchmark tuning. | Normal use. |
| `advanced.speculative.pMin` | `--spec-draft-p-min` | Minimum speculative probability. | Speculative benchmark tuning. | Normal use. |
| `advanced.speculative.pSplit` | `--spec-draft-p-split` | Split probability. | Speculative benchmark tuning. | Normal use. |
| `advanced.speculative.ngram*` | `--spec-ngram-*` | N-gram speculative settings. | You use n-gram speculative mode. | Normal use. |

### 9.13 Advanced: Logging and diagnostics

| Field | CLI flag | Description | Set when | Do not set when |
|---|---|---|---|---|
| `advanced.logging.disable` | `--log-disable` | Disable logging. | Noise-sensitive production-like runs. | Debugging. |
| `advanced.logging.file` | `--log-file` | Write logs to file. | Long-running server or bug reports. | Quick tests. |
| `advanced.logging.colors` | `--log-colors` | Colored logs: `on`, `off`, `auto`. | Improve terminal readability. | File logs should usually be `off` or `auto`. |
| `advanced.logging.verbose` | `-v`, `--verbose`, `--log-verbose` | Maximum verbosity. | Debugging failed launches or templates. | Normal use; logs become noisy. |
| `advanced.logging.verbosity` | `-lv`, `--verbosity` | Verbosity threshold. | Controlled diagnostics. | Normal use. |
| `advanced.logging.prefix` | `--log-prefix` / `--no-log-prefix` | Prefix log messages. | Log aggregation. | Normal use. |
| `advanced.logging.timestamps` | `--log-timestamps` / `--no-log-timestamps` | Log timestamps. | Benchmark/debug timelines. | Not necessary for quick tests. |
| `advanced.diagnostics.perf` | `--perf` / `--no-perf` | Internal performance timings. | Benchmarking and profiling. | Normal serving if overhead/noise matters. |
| `advanced.diagnostics.checkTensors` | `--check-tensors` | Check model tensor data for invalid values. | Suspect corrupted model. | Normal launches; can slow startup. |
| `advanced.diagnostics.listDevices` | `--list-devices` | Print available devices and exit. | Studio hardware discovery. | Normal launch; this exits instead of serving. |

## 10. Command generation rules

### 10.1 General rules

1. Build args as an array, not one string.
2. Never shell-concatenate unescaped user input.
3. Emit only fields that are explicitly set or required by Studio.
4. Preserve user intent for booleans with tri-state values: `unset`, `true`, `false`.
5. Append `advanced.extraArgs` last.
6. Show a command preview with shell-escaped values.
7. Store the exact generated argv used for each process run.

### 10.2 Boolean generation

Use these patterns:

| Field state | Example flag pair | Emit |
|---|---|---|
| unset | `--ui` / `--no-ui` | nothing, unless Studio default requires one |
| true | `--ui` / `--no-ui` | `--ui` |
| false | `--ui` / `--no-ui` | `--no-ui` |

Do not confuse `false` with `unset`. Unset means “let llama-server default decide.”

### 10.3 Single Model Mode generation order

Recommended order:

```txt
llama-server
  [model source]
  [model alias/tags]
  [server binding]
  [runtime]
  [concurrency]
  [capability flags]
  [monitoring]
  [advanced groups]
  [raw extra args]
```

Example:

```bash
llama-server \
  --model /models/qwen3-8b-q4_k_m.gguf \
  --alias qwen3-8b \
  --host 127.0.0.1 \
  --port 8081 \
  --no-ui \
  --ctx-size 8192 \
  --n-gpu-layers auto \
  --fit on \
  --flash-attn auto \
  --parallel 2 \
  --cont-batching \
  --metrics \
  --slots
```

### 10.4 Router Mode generation order

Example:

```bash
llama-server \
  --host 127.0.0.1 \
  --port 8080 \
  --models-dir /models \
  --models-max 2 \
  --models-autoload \
  --metrics \
  --slots
```

## 11. Validation rules

Validation has three levels:

- **Error**: block launch.
- **Warning**: allow launch but require visible warning.
- **Info**: explain tradeoff.

### 11.1 Required errors

Block launch when:

- `launcher.llamaServerPath` is missing or not executable;
- `mode = single` and no model source is configured;
- more than one single-model source is configured, unless explicitly allowed;
- `mode = router` and single-model fields are set;
- port is already in use by another Studio-managed process;
- `server.port` is outside valid TCP range;
- `server.apiPrefix` has trailing slash;
- `model.path` does not exist;
- `model.path` is not a file;
- `model.path` does not look like `.gguf`, unless user explicitly allows;
- `requestDefaults.temperature` is outside accepted range;
- `requestDefaults.topP` or `minP` is outside `0..1`;
- unknown enum values are present.

### 11.2 Required warnings

Warn when:

- `server.host = 0.0.0.0` and no API key is configured;
- built-in tools are enabled;
- UI MCP proxy is enabled;
- `ctxSize` is much larger than model training context metadata;
- `ctxSize` is likely too high for detected VRAM/RAM;
- `gpuLayers = all` and VRAM is likely insufficient;
- `parallelSlots > 1` with very large context;
- embeddings mode is enabled with a model not detected as embedding-capable;
- rerank mode is enabled with a model not detected as rerank-capable;
- custom chat template is used;
- RoPE settings are manually overridden;
- raw extra args conflict with structured args;
- `--no-ui` is set and user expects to open llama-server UI;
- metrics are disabled and Studio benchmark/stat collection is enabled.

### 11.3 Info messages

Show info when:

- `gpuLayers = auto`: llama-server will choose offload behavior;
- `fit = on`: unset memory-related args may be adjusted to fit device memory;
- `ctxSize = 0`: context size is loaded from model metadata;
- request defaults affect Studio requests but not necessarily external clients;
- `parallelSlots = -1`: slot count is automatic;
- `continuousBatching = true`: improves server throughput but changes request scheduling behavior.

## 12. Presets

Presets should fill fields but remain editable.

### 12.1 Balanced Local Chat

Use when user wants a good default.

```json
{
  "runtime": {
    "ctxSize": 8192,
    "gpuLayers": "auto",
    "fit": "on",
    "flashAttention": "auto",
    "threads": -1
  },
  "concurrency": {
    "parallelSlots": -1,
    "continuousBatching": true
  },
  "monitoring": {
    "metrics": true,
    "slots": true
  },
  "requestDefaults": {
    "temperature": 0.8,
    "topP": 0.95,
    "minP": 0.05,
    "maxTokens": 1024,
    "seed": -1,
    "stream": true
  }
}
```

### 12.2 CPU Only

Use when no compatible GPU is available or user wants reproducible CPU behavior.

```json
{
  "runtime": {
    "gpuLayers": 0,
    "threads": -1,
    "fit": "off",
    "flashAttention": "off"
  },
  "advanced": {
    "hardware": {
      "device": "none"
    }
  }
}
```

### 12.3 GPU Full Offload

Use when model fits entirely in VRAM.

```json
{
  "runtime": {
    "gpuLayers": "all",
    "fit": "on",
    "flashAttention": "auto"
  }
}
```

Warn if VRAM estimate is insufficient.

### 12.4 Long Context

Use for retrieval, document analysis, long chat history.

```json
{
  "runtime": {
    "ctxSize": 32768,
    "gpuLayers": "auto",
    "fit": "on"
  },
  "concurrency": {
    "parallelSlots": 1
  },
  "requestDefaults": {
    "maxTokens": 2048
  }
}
```

Warn that long context increases KV cache memory.

### 12.5 Embedding Server

Use for vector database ingestion/search.

```json
{
  "capabilities": {
    "apiUseCase": "embeddings",
    "embeddingsOnly": true
  },
  "requestDefaults": {
    "stream": false
  }
}
```

Validate that model appears embedding-capable.

### 12.6 Rerank Server

Use for ranking candidate documents/passages.

```json
{
  "capabilities": {
    "apiUseCase": "rerank",
    "rerank": true
  },
  "requestDefaults": {
    "stream": false
  }
}
```

Validate that model appears rerank-capable.

### 12.7 Benchmark Profile

Use for consistent comparison.

```json
{
  "runtime": {
    "fit": "off"
  },
  "concurrency": {
    "parallelSlots": 1,
    "continuousBatching": false
  },
  "monitoring": {
    "metrics": true,
    "slots": true
  },
  "requestDefaults": {
    "temperature": 0.0,
    "topP": 1.0,
    "minP": 0.0,
    "seed": 42,
    "stream": false
  }
}
```

Benchmark note: if the goal is production throughput, use a separate throughput benchmark preset with continuous batching enabled and multiple slots.

## 13. Process lifecycle

Each launched profile becomes a Studio-managed server instance.

### 13.1 Instance fields

```json
{
  "instanceId": "run_01hxyz",
  "profileId": "qwen3-8b-local-fast",
  "pid": 12345,
  "status": "starting",
  "baseUrl": "http://127.0.0.1:8081",
  "startedAt": "2026-05-25T10:00:00+07:00",
  "argv": ["/path/llama-server", "--model", "/models/qwen.gguf"],
  "logPath": "/studio/logs/run_01hxyz.log",
  "health": {
    "lastCheckAt": null,
    "state": "unknown"
  }
}
```

### 13.2 Status model

```txt
created -> starting -> loading -> ready -> stopping -> stopped
                       └──────-> failed
ready -> unhealthy -> ready
```

### 13.3 Health checks

Studio should poll:

- `GET /health` until ready;
- `GET /v1/models` after ready;
- `GET /metrics` when metrics enabled;
- `GET /slots` when slots enabled.

### 13.4 Stop behavior

On stop:

1. send graceful termination signal;
2. wait configured timeout;
3. force kill if still alive;
4. record exit code;
5. preserve logs and last stats.

## 14. Stats collection

### 14.1 Sources

Collect stats from:

- `/metrics` when enabled;
- `/slots` when enabled;
- Studio request proxy logs;
- process start/stop events;
- benchmark runner results.

### 14.2 Core metrics

Store at minimum:

| Metric | Source | Description |
|---|---|---|
| `promptTokensTotal` | `/metrics` | Total prompt tokens processed. |
| `promptSecondsTotal` | `/metrics` | Total prompt processing time. |
| `promptTokensPerSecond` | `/metrics` | Prompt throughput. |
| `generationTokensTotal` | `/metrics` | Total generated tokens. |
| `generationSecondsTotal` | `/metrics` | Total generation time. |
| `generationTokensPerSecond` | `/metrics` | Decode throughput. |
| `requestsProcessing` | `/metrics` | Active processing requests. |
| `requestsDeferred` | `/metrics` | Deferred/queued requests. |
| `slotCount` | `/slots` | Total slots. |
| `busySlots` | `/slots` | Active slots. |
| `ctxSizeObserved` | `/metrics` or `/slots` | High-water context usage. |
| `processMemoryRss` | OS | Resident memory for child process. |
| `gpuMemoryUsed` | backend/system probe | VRAM use if available. |

### 14.3 Benchmark record

```json
{
  "benchmarkId": "bench_01hxyz",
  "profileId": "qwen3-8b-local-fast",
  "instanceId": "run_01hxyz",
  "startedAt": "2026-05-25T10:10:00+07:00",
  "promptName": "short-chat",
  "request": {
    "maxTokens": 256,
    "temperature": 0.0,
    "seed": 42,
    "stream": false
  },
  "result": {
    "success": true,
    "timeToFirstTokenMs": 180,
    "totalLatencyMs": 5400,
    "promptTokens": 128,
    "completionTokens": 256,
    "promptTokensPerSecond": 900,
    "generationTokensPerSecond": 47.4
  }
}
```

## 15. Optional routing by profile id

Studio can expose its own proxy endpoint:

```txt
POST /profiles/{profileId}/v1/chat/completions
POST /profiles/{profileId}/v1/completions
POST /profiles/{profileId}/v1/embeddings
POST /profiles/{profileId}/rerank
```

Routing rules:

1. If profile has a ready instance, forward to that instance.
2. If profile is stopped and `autoStart` is enabled, start it, wait for health, then forward.
3. If profile is stopped and `autoStart` is disabled, return `409 Profile not running`.
4. If multiple instances exist for a profile, route to the active primary instance.
5. Preserve streaming behavior.
6. Add Studio request logging for stats.

Profile routing metadata:

```json
{
  "routing": {
    "enabled": true,
    "autoStart": false,
    "primaryInstancePolicy": "latest-ready",
    "publicPath": "/profiles/qwen3-8b-local-fast/v1"
  }
}
```

Security warning: if Studio exposes profile routing beyond localhost, Studio itself needs authentication and rate limiting. Do not rely only on child `llama-server` settings.

## 16. Arg registry implementation

Represent every known arg using an internal registry. This keeps the UI, validation, docs, and generator aligned.

```ts
export type ArgLevel = "simple" | "advanced";
export type ArgKind = "string" | "int" | "float" | "bool" | "enum" | "path" | "stringArray" | "secretRef";

export interface ArgSpec {
  key: string;
  level: ArgLevel;
  group: string;
  label: string;
  kind: ArgKind;
  cliFlags: string[];
  defaultValue?: unknown;
  recommendedDefault?: unknown;
  allowedValues?: string[];
  description: string;
  whenToSet: string;
  whenToLeaveDefault: string;
  validation?: string[];
  conflictsWith?: string[];
  requires?: string[];
  securitySensitive?: boolean;
  experimental?: boolean;
}
```

Example:

```ts
const ctxSizeArg: ArgSpec = {
  key: "runtime.ctxSize",
  level: "simple",
  group: "Runtime",
  label: "Context size",
  kind: "int",
  cliFlags: ["-c", "--ctx-size"],
  recommendedDefault: 8192,
  description: "Prompt context size in tokens. 0 means load from model metadata.",
  whenToSet: "Set when you know the target context length for chat, RAG, or benchmark.",
  whenToLeaveDefault: "Leave 0/empty when you want the model metadata to decide or when memory is tight.",
  validation: ["must be >= 0", "warn if much larger than model metadata"]
};
```

## 17. Migration strategy

Profiles must have `schemaVersion`.

Migration examples:

- renamed fields;
- moved advanced arg from raw extra args to structured field;
- changed default UI recommendation;
- deprecated flags.

Migration rule:

- never delete unknown fields silently;
- preserve raw extra args;
- show a migration note in profile history;
- allow export of original profile JSON.

## 18. Export formats

Support exports:

1. **Studio Profile JSON** — complete structured profile.
2. **Shell script** — generated `llama-server` launch command.
3. **Docker command** — optional later.
4. **Environment file** — optional for production-like setup.

Example shell export:

```bash
#!/usr/bin/env bash
set -euo pipefail

exec /opt/llama.cpp/build/bin/llama-server \
  --model /models/qwen3-8b-q4_k_m.gguf \
  --alias qwen3-8b \
  --host 127.0.0.1 \
  --port 8081 \
  --no-ui \
  --ctx-size 8192 \
  --n-gpu-layers auto \
  --fit on \
  --flash-attn auto \
  --parallel 2 \
  --cont-batching \
  --metrics \
  --slots
```

## 19. MVP scope

### Include in v1

- profile CRUD;
- local model path selection;
- Hugging Face repo/file fields;
- simple arg builder;
- advanced raw extra args;
- command preview;
- validation warnings/errors;
- spawn/stop/restart process;
- health check;
- logs;
- quick test chat;
- metrics and slots collection;
- benchmark records;
- import/export profile JSON.

### Defer after v1

- full typed UI for every advanced arg;
- router preset editor;
- dynamic model load/unload UI;
- multi-GPU visual planner;
- automatic VRAM estimator;
- LoRA runtime controls;
- custom chat template editor with validation;
- production deployment generator.

## 20. Recommended default decisions

For the default new profile:

```txt
mode = single
host = 127.0.0.1
port = next available port starting 8080
enableUi = false
ctxSize = 0 or 8192 depending on UX preference
gpuLayers = auto
fit = on
flashAttention = auto
threads = -1
parallelSlots = -1
continuousBatching = true
metrics = true
slots = true
temperature = 0.8
topP = 0.95
minP = 0.05
maxTokens = 1024
seed = -1
stream = true
```

If the user is unsure, recommend:

- leave `gpuLayers = auto`;
- leave `fit = on`;
- leave `threads = -1`;
- keep `host = 127.0.0.1`;
- enable metrics and slots;
- do not touch RoPE, KV cache types, CPU affinity, speculative decoding, or templates.

## 21. User-facing decision copy

These short texts should appear in the UI as helper text.

### Context size

> Larger context lets the model read more prompt/history, but uses more memory, especially KV cache memory. Use model default when unsure.

### GPU layers

> Controls how much of the model is placed on GPU. `auto` is safest. Use `all` only when the model fits in VRAM. Use `0` for CPU-only.

### Fit to memory

> Allows llama-server to adjust unset memory-sensitive options to fit your device. Keep on unless you need exact benchmark reproducibility.

### Parallel slots

> More slots allow more concurrent work, but increase memory use. Use `1` for single-user long-context testing. Use auto for normal serving.

### Continuous batching

> Improves throughput when multiple requests are active. Disable only for debugging or strict latency experiments.

### Metrics

> Enables the metrics endpoint so Studio can collect throughput and request stats.

### Slots endpoint

> Shows live server slots, token progress, and per-request state. Useful for debugging and monitoring.

### Host

> `127.0.0.1` is local-only and safest. `0.0.0.0` exposes the server to the network and should use API keys/firewall.

### API key

> Add API keys when the server is reachable by anything except your own machine.

### Raw extra args

> Advanced escape hatch for llama-server flags not yet modeled by Studio. Raw args are appended after generated args and may override behavior.

## 22. References for implementers

Use the currently installed `llama-server --help` as the source of truth at runtime because llama.cpp flags evolve quickly.

Also check the official `llama.cpp/tools/server/README.md` for:

- common params;
- sampling params;
- server params;
- endpoint behavior;
- metrics and slots;
- router mode;
- multimodal support;
- built-in tools and security notes.

