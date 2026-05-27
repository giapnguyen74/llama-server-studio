<div align="center">
  <img src="assets/llama_studio_logo_1779876976860.png" alt="Llama Server Studio" width="120">
  <h1>Llama Server Studio</h1>
  <p><strong>Ollama is too simple. <code>llama.cpp</code> is too complex. This is the middle ground.</strong></p>

  [![License: GPL-3.0](https://img.shields.io/badge/License-GPL%203.0-blue.svg)](./LICENSE)
  [![Go 1.22+](https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go)](https://go.dev/)
  [![Zero Dependencies](https://img.shields.io/badge/Dependencies-Zero-brightgreen)](./go.mod)
</div>

---

Running local LLMs shouldn't mean choosing between a locked-down black box and a wall of command-line flags. **Llama Server Studio** gives you the full control of `llama.cpp`'s `llama-server` without the friction — a practical workbench for people who actually want to tune, benchmark, and ship local inference.

## The Problem

**Ollama** is great for getting started. Pull a model, chat, done. But the moment you want to push past defaults — adjust layer offloading for your exact VRAM budget, tune batch sizes, run multiple configurations side-by-side, or understand *why* one setup is faster than another — you hit a wall. Ollama abstracts away the very knobs you need.

**`llama.cpp` / `llama-server`** gives you everything. Every flag, every option, raw power. But building a valid server command, tracking what each profile does, correlating benchmark results with configuration changes, and wiring it up as a stable OpenAI-compatible endpoint? That's a spreadsheet, a pile of shell scripts, and a lot of `--help` calls.

**There's a gap.** A practical gap for the developers, researchers, and hobbyists who know what they're doing but don't want to babysit processes and reinvent tooling every time.

---

## ✨ Features at a Glance

### 🛠️ Profile Builder — Simple Sliders to Full Raw Flags

Configure `llama-server` the way that suits you. Start with **Simple Mode** — a clean form for context window, GPU layers, thread count, batch size, and parallel slots, with built-in **hardware estimation** that calculates how many GPU layers fit your VRAM budget. Ready to go deeper? Flip to **Advanced Mode** and edit the raw flag array directly. Either way, the exact `llama-server` command is always shown, always copyable — no magic, no surprises.

> **Simple → Advanced**: Move from a beginner-friendly slider UI to a raw JSON flag array in one click. Nothing is hidden.

![Profile Builder — configure llama-server with a visual UI or raw flags](assets/profile-builder.png)

---

### 🖥️ Server Lifecycle & Real-Time Monitoring

Start, stop, and restart `llama-server` child processes from the UI. Watch live telemetry: prefill speed, generation throughput, KV-cache utilisation, in-flight requests, and uptime — all updated in real time.

Includes a **Quick Test Console** to fire off a single prompt (with optional image or audio attachment) directly at the running server instance — a fast sanity check without wiring up a full client.

![Server Lifecycle — real-time metrics, logs, and Quick Test console](assets/server.png)

---

### 📊 Benchmark Suite — Pick the Best Setup for Your Lab

Stop guessing which configuration is fastest. Run **repeatable throughput benchmarks** directly against your profiles and let the numbers decide. The suite handles pre-warming, repetition averaging, and tokens-per-second parsing — then surfaces a single **Optimal Recommendation** so you know exactly which settings to ship.

> **Why it matters**: A Q4_K_M at `-ngl 35` might outperform a Q5_K_S at full offload on your hardware. Benchmark first, configure with confidence.

![Benchmark Comparison — measure throughput and latency across profiles](assets/benchmark.png)

---

### 📥 Hugging Face Hub — Pull GGUF Models Like Ollama, but You Own the Files

Type a repo ID, browse the files, pick your quantization, and click download — as easy as `ollama pull`, except **the GGUF lands on your disk in a folder you control**. No opaque blob store, no registry lock-in. Filter to `.gguf`-only, inspect sizes before committing, and batch-select multiple quants in one go. The model is immediately visible in the catalog.

> **Painless**: `Llama-3-8B-Q4_K_M.gguf` in three clicks. No CLI, no `wget`, no figuring out which shard to download.

![Hugging Face Hub — browse repos and download GGUF models](assets/huggingface.png)

---

### 🔗 Proxy Gateway — OpenAI-Compatible Model Router

The gateway is a **stable, named endpoint** that sits in front of your running profiles. Point Open WebUI, the OpenAI Python SDK, LangChain, or any `curl` script at `http://localhost:3101/v1/chat/completions` — it just works, exactly like talking to `ollama serve` or the real OpenAI API. No reconfiguration when you swap models; the gateway routes to whichever profile is active.

| | Llama Server Studio Gateway | Ollama |
|---|---|---|
| OpenAI-compatible API | ✅ | ✅ |
| SSE streaming | ✅ | ✅ |
| Model routing by name | ✅ | ✅ |
| You control the llama-server flags | ✅ | ❌ |
| Benchmarks & profiling | ✅ | ❌ |

![Gateway — OpenAI-compatible proxy; works with Open WebUI and any SDK](assets/gateway.png)

**Plugs into Open WebUI in seconds:**

![Open WebUI connected to Llama Server Studio via the gateway](assets/openwebui-conn.png)

---

## ⚡ Key Technical Notes

- **Zero dependencies** — pure Go standard library, single self-contained binary. No Node, no Docker, no external databases.
- **Embedded Web UI** — the dashboard ships inside the binary via `go:embed`.
- **Fast GGUF parser** — reads v1/v2/v3 headers directly without loading tensor data. Extracts context length, architecture, and chat template in milliseconds.
- **Process supervisor** — spawns and watches `llama-server` child processes, auto-allocates ports, streams logs to disk, scrapes CPU/memory in real time.
- **Multimodal-aware** — profiles built with `--mmproj` are detected automatically; the Quick Test console unlocks image/audio attachment only when the server supports it.
- **Local-first, local-safe** — binds to `127.0.0.1:3100` by default. LAN exposure requires explicit opt-in.

---

## 🚀 Getting Started

### Build

Requires Go 1.22+. No other toolchain needed.

```bash
git clone https://github.com/youruser/llama-server-studio
cd llama-server-studio
go build -o llama-server-studio main.go
```

### Run

```bash
./llama-server-studio --models-dir ~/models
```

Open **`http://127.0.0.1:3100`** in your browser.

```
  🦙 L L A M A   S E R V E R   S T U D I O
  =========================================
  Listen Endpoint : http://127.0.0.1:3100
  Data Directory  : ~/.llama-server-studio
  Binary Location : /opt/homebrew/bin/llama-server
  Scan Folders    : ~/models
  =========================================
```

---

## ⚙️ CLI Options

| Option | Description | Default |
|---|---|---|
| `--listen` | Studio bind address and port | `127.0.0.1:3100` |
| `--data-dir` | Folder for logs, database, config | `~/.llama-server-studio` |
| `--config` | Path to config.json | `<data-dir>/config.json` |
| `--llama-server-bin` | Path to llama-server binary | *auto-detected* |
| `--models-dir` | GGUF model scan directory | `<data-dir>/models` |
| `--scan-hf-cache` | Scan local Hugging Face cache | `true` |
| `--allow-insecure-lan` | Allow non-localhost without password | `false` |
| `--password` | Set admin password and exit | *none* |

---

## 📁 Data Layout

```
<data-dir>/
├── studio.db       # JSON database: model catalog, profiles, benchmarks, metrics
├── logs/           # Per-server stdout/stderr logs
└── exports/        # Exported profile configs and shell scripts
```

---

## 📝 Design Principles

1. **Never hide the command.** The exact `llama-server` invocation is always visible and copyable.
2. **Never lock you in.** Raw argument arrays are always an option alongside the form UI.
3. **Never touch your models.** The catalog is read-only. Files are never modified.
4. **Local by default.** No cloud, no telemetry, no surprises.

---

## License

[GPL-3.0](./LICENSE)
