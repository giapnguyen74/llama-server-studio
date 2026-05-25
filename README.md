# 🦙 Llama Server Studio

**Llama Server Studio** is a self-contained, local-first operational dashboard, benchmark engine, and process supervisor for `llama-server` from `llama.cpp`.

Designed for developers working with local GGUF models, this tool allows you to catalog local models, construct valid `llama-server` command-line configurations with real-time syntax previews, supervise child runtime instances, record performance benchmarks, and expose stable inference routing gateways.

> [!NOTE]  
> The entire application is built **strictly using the Go Standard Library**. It has **zero third-party dependencies** (no node_modules, no external database runtimes, no compilation CGO bindings). The vibrant frontend Web UI is embedded directly inside the Go executable using `go:embed`.

---

## ⚡ Key Features

* **⚡ Fast Binary GGUF Metadata Parser**: Reads GGUF v1, v2, and v3 headers directly. Extracts context windows, model architectures, and default chat templates instantly without loading massive tensor data into memory.
* **📂 Automated Model Discovery**: Recursively scans configured folders and Hugging Face local snapshot caches. Resolves symlinks, deduplicates path mappings, and identifies model capabilities.
* **🛠️ Profile Builder & Shell Exporter**: Supports a *Simple Mode* slider-form for common flags (threads, batch size, VRAM layer counts, parallel seqs) and *Advanced Mode* for raw JSON arrays. Shows a real-time terminal CLI command preview. Exports profiles to standardized JSON and runnable `.sh` bash launchers.
* **⚡ Child Process Supervisor**: Spawns and supervises `llama-server` child runtimes, automatically searching and allocating vacant OS ports. Saves stdout/stderr logs on disk, handles graceful SIGINT interruptions, and scrapes real-time CPU/memory usage metrics via native macOS/Linux system pipelines.
* **📈 Automated Benchmark Runner**: Executes prompt throughput benchmarks directly against serving ports. Performs pre-heating warmups and active repetitions, parsing Timing JSON tokens-per-second values directly from llama-server completion timelines.
* **🔗 Stable Profile Reverse Proxy**: Rewrites URL paths and proxies incoming completions traffic. Supports SSE (Server-Sent Events) chunk flushing with a dynamic `50ms` delay interval for real-time prompt test playgrounds.
* **🛡️ Zero Dependency, Local Security**: Binds locally to `127.0.0.1:3100` by default. Can bind outside localhost securely using custom `--admin-token` request authorization blocks.

---

## 🚀 Getting Started

### 1. Build the Binary
Ensure you have Go installed (Go 1.22+ is recommended). Compile the completely self-contained binary:

```bash
go build -o llama-server-studio main.go
```

### 2. Launch the Application
Run the executable pointing to your GGUF models folders:

```bash
./llama-server-studio --models-dir ~/models
```

On launch, the Studio will print a terminal dashboard:
```text
  🦙 L L A M A   S E R V E R   S T U D I O
  =========================================
  Listen Endpoint : http://127.0.0.1:3100
  Data Directory  : /Users/username/.local/share/llama-server-studio
  Binary Location : /opt/homebrew/bin/llama-server
  Scan Folders    : /Users/username/models
  =========================================
```

### 3. Open the UI Dashboard
Open your browser and navigate to **`http://127.0.0.1:3100`** to begin!

---

## ⚙️ CLI Reference Parameters

You can customize the Studio directly using CLI options on startup:

| Option | Description | Default |
| --- | --- | --- |
| `--listen` | Studio bind address and port | `127.0.0.1:3100` |
| `--config` | Custom path to config.json file | `~/.config/llama-server-studio/config.json` |
| `--data-dir` | Folder for saving logs, exports, database | `~/.local/share/llama-server-studio/` |
| `--llama-server-bin` | Direct absolute path to llama-server binary | *Auto-detected* |
| `--models-dir` | Model scan directory path (can repeat or separate by comma) | `~/models` |
| `--scan-hf-cache` | Scan local Hugging Face Hub cache directories | `true` |
| `--admin-token` | Token required for remote LAN administrative access | *Disabled* |
| `--allow-insecure-lan` | Allow non-localhost bindings without admin token | `false` |

---

## 📁 Storage Schema & Files

The Studio stores persistent records and log files cleanly in your shares folder:

* **`studio.db`**: An atomic JSON database caching scanned GGUF catalog models, saved serving profiles, running telemetry metrics, and benchmark histories.
* **`logs/`**: Keeps individual file logs streamed directly from `llama-server` stdout and stderr (e.g. `logs/server-srv_1716499.log`).
* **`exports/`**: Holds exported configuration templates.

---

## 📝 Design Principles

1. **Do not hide the command**: The exact generated command-line execution parameters are always visible and copyable.
2. **Do not lock users into known flags**: Full raw argument array customization is supported.
3. **Do not mutate model files**: The Model Catalog is read-only and strictly observational in v1.
4. **Prefer local safety**: Binds locally by default, protecting active pipelines and file systems securely.
