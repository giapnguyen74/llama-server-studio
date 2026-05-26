# 🦙 Llama Server Studio

> **Ollama is too simple. `llama.cpp` is too complex. This is the middle ground.**

Running local LLMs shouldn't mean choosing between a locked-down black box and a wall of command-line flags. **Llama Server Studio** gives you the control of `llama.cpp`'s `llama-server` without the friction — a practical workbench for people who actually want to tune, benchmark, and ship local inference.

---

## The Problem

**Ollama** is great for getting started. Pull a model, chat, done. But the moment you want to push past defaults — adjust layer offloading for your exact VRAM budget, tune batch sizes, run multiple configurations side-by-side, or understand *why* one setup is faster than another — you hit a wall. Ollama abstracts away the very knobs you need.

**`llama.cpp` / `llama-server`** gives you everything. Every flag, every option, raw power. But building a valid server command, tracking what each profile does, correlating benchmark results with configuration changes, and wiring it up as a stable OpenAI-compatible endpoint? That's a spreadsheet, a pile of shell scripts, and a lot of `--help` calls.

**There's a gap.** A practical gap for the developers, researchers, and hobbyists who know what they're doing but don't want to babysit processes and reinvent tooling every time.

---

## What Llama Server Studio Does

### 📥 Simple Model Downloader
Pull GGUF models by name — similar to `ollama pull`, but you land the actual file and you own it. No opaque blob storage. No registry lock-in. Just a GGUF on disk, with metadata extracted and catalogued automatically.

### 🛠️ Profile Builder with Hardware Estimation
Stop guessing `-ngl` values. Build named *serving profiles* with a visual form: select a model, set your thread count, batch size, parallel sequences, and context window. The studio estimates GPU layer counts that fit your VRAM budget and shows you the exact `llama-server` command it will run — always visible, always copyable.

Switch between a **Simple Mode** slider UI and a raw **Advanced Mode** JSON array for complete flag control. Save profiles, export them as `.sh` scripts, share them with your team.

### 📊 Benchmark & Meter Suite
Run repeatable prompt-throughput benchmarks directly against your running servers. Pre-warming, repetition averaging, tokens-per-second parsing — all built in. Compare profiles side-by-side. Understand the real cost of a configuration change before you ship it.

### 🔗 Proxy Gateway (OpenAI-compatible)
Point your applications at the Studio's stable proxy address instead of ephemeral server ports. The gateway routes to whichever profile is active, handles SSE streaming, and speaks standard OpenAI API — so any tool that works with `ollama serve` or the OpenAI SDK works here too, without reconfiguration.

---

## ⚡ Key Technical Notes

- **Zero dependencies** — pure Go standard library, single self-contained binary. No Node, no Docker, no external databases.
- **Embedded Web UI** — the dashboard ships inside the binary via `go:embed`.
- **Fast GGUF parser** — reads v1/v2/v3 headers directly without loading tensor data. Extracts context length, architecture, and chat template in milliseconds.
- **Process supervisor** — spawns and watches `llama-server` child processes, auto-allocates ports, streams logs to disk, scrapes CPU/memory in real time.
- **Local-first, local-safe** — binds to `127.0.0.1:3100` by default. LAN exposure requires explicit opt-in.

---

## 🚀 Getting Started

### Build

Requires Go 1.22+. No other toolchain needed.

```bash
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
