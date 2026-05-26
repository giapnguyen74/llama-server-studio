# Comprehensive Benchmark Suite — Design

Status: design accepted, not yet implemented
Scope: replaces the simple "average tokens/sec on one prompt" benchmark in `internal/bench/runner.go` with a structured suite that answers real-world questions about llama-server settings on a given hardware/model combination.

## 1. The question this exists to answer

The single question we're really trying to answer: **what flags should I pass to `llama-server` to get the best behaviour for my model on my hardware?**

A useful benchmark must therefore let the user:

- Vary one or more flags (`-ngl`, `-b`, `--cont-batching`, `--parallel`, `--flash-attn`, `-c`, quantisation choice).
- Hold everything else constant.
- Run the same workload across each variant.
- See a side-by-side comparison with statistical confidence.
- Walk away with a recommendation: "use these flags".

Today's runner does none of that. It averages tokens/sec across a few repetitions of one prompt, on one server, with whatever flags happen to be on that profile. That tells you the absolute number but answers no comparative question.

## 2. The two camps: inference vs. serving

Benchmarks for LLM inference split naturally into two camps. We need both:

**Inference benchmark** — "how fast does this model run with these flags on this hardware?"
- Measures raw model performance: prompt processing speed (pp/s) and token generation speed (tg/s) at varying batch sizes and context positions.
- The HTTP layer is noise; we want signal from the inference engine.
- Best tool for this: llama.cpp's own `llama-bench` binary. It bypasses the server, runs in a tight loop, prints repeatable numbers.

**Serving benchmark** — "how does this configuration behave under realistic load?"
- Measures HTTP-level metrics: time to first token (TTFT), inter-token latency (ITL), end-to-end latency, throughput under concurrent requests, slot scheduling fairness, error rate under burst.
- Requires the real HTTP path, the real reverse proxy, the real `-cb` / `-np` interaction.
- Best tool for this: our own runner, generating concurrent traffic against the running server.

The suite ships both. Users pick which camp matches their question.

## 3. Metrics — what we measure, and what each one tells you

| Metric | Unit | Tells you | Source |
| --- | --- | --- | --- |
| **pp/s** (prompt processing speed) | tokens / second | How fast you can ingest a long prompt. Compute-bound at low `-b`, memory-bound at high `-b`. | `timings.prompt_per_second` from `/completion` |
| **tg/s** (token generation speed) | tokens / second | How fast you stream output once the prompt is processed. Almost purely memory-bandwidth-bound. | `timings.predicted_per_second` |
| **TTFT** (time to first token) | milliseconds | Chat UX latency — how long the user waits before seeing anything. | SSE stream timestamp of first `data:` event |
| **ITL** (inter-token latency) | milliseconds | Steady-state perceived speed. p50 and p95 are both interesting (p95 catches stalls). | Diff between consecutive SSE events |
| **e2e latency** | milliseconds | Full request time, including queueing and prompt processing. | `time.Since(reqStart)` |
| **Throughput** | aggregate tg/s | Real serving capacity under concurrency = (tokens generated across all in-flight requests) / wall time. | Aggregate of all requests in a window |
| **RPS sustained** | requests / second | How many simultaneous chats the config can handle without latency falling off a cliff. | Same workload, varying concurrency |
| **Error rate** | % | Fraction of requests that 5xx, time out, or get queued past a deadline. | HTTP status + timeout config |
| **Peak VRAM** | bytes | Whether the config fits the hardware at all. | nvidia-smi sample at the request peak |
| **Idle CPU %** | % of one core | Does the config burn cycles when no work is happening? (Catches the `-cb` idle-loop issue documented in chat.) | `ps -p PID -o %cpu` between bursts |

Always report **p50, p90, p99, mean, std dev** — not just mean. High variance is itself a finding; "27 tok/s ± 12" tells you something is unstable.

## 4. The four benchmark types

### 4.1 Single-shot

The current behaviour. One profile, one prompt, N warmups + M repetitions, sequential. Keep it — it's the fastest way to sanity-check that a server is alive and producing sensible numbers. Use it as the warmup gate before any heavier suite.

### 4.2 Sweep (one variable)

The bread and butter. Pick **one flag**, give the user a list of values, run the same workload against each value, plot the curve.

Built-in sweep templates:

- **`-ngl` sweep**: 0 → max layers, in 5 steps. Answers: "how many layers should I put on GPU?". Often hits a knee at "all layers on GPU" or just below if VRAM is tight.
- **`-b` sweep**: 64, 128, 256, 512, 1024, 2048. Answers: "what batch size maximises pp/s for my prompt length?". Larger isn't always faster — past a point the kernel saturates and you just waste latency.
- **`-np` sweep**: 1, 2, 4, 8. Answers: "what parallel slot count maximises my serving throughput before per-request latency dies?".
- **Quant sweep**: same model in Q4_K_M, Q5_K_M, Q6_K, Q8_0. Answers: "what's the speed/quality knee for my hardware?". Quality side is out of scope here, but speed/VRAM is dead easy to measure.
- **Flash-attn on/off**: a 2-point sweep. Often a 10–30% tg/s improvement on capable GPUs.
- **Context-size sweep**: run the same generation but with prompt lengths 256, 1k, 4k, 16k, 64k. Answers: "how does my config degrade as context fills?". The curve is rarely flat.

Output: a table + plot of metric vs. flag value, with a highlighted "best" row using a user-selectable objective (max tg/s, max throughput, min TTFT, etc.).

### 4.3 Grid (two or three variables)

Cartesian product of two or three sweeps. Useful when variables interact — e.g. `-b` × `-np` (batch size and parallel slots are not independent).

Hard cap on grid size (say 24 cells) so the user doesn't kick off a 6-hour run by accident; UI warns before launch with an ETA based on a quick calibration probe.

Output: a heatmap of the chosen metric across the grid, plus the same "best cell highlighted" recommendation.

### 4.4 Concurrency / load test

Closer to vLLM's `benchmark_serving.py`. Pick a profile, a concurrency level (or a sequence — e.g. 1, 2, 4, 8 concurrent users), and a workload. The runner generates Poisson-distributed traffic at the chosen concurrency and records per-request TTFT, ITL, e2e, error rate. Report aggregate throughput too.

This is the only benchmark that exercises the slot scheduler, continuous batching, and the proxy under realistic conditions. It's expensive — runs for 30–120 s — so it shouldn't be the default, but it's the one that answers "can this profile serve N users?".

### 4.5 A/B comparison

Two profiles, same workload, side-by-side. Statistical test (paired t-test or Mann–Whitney depending on distribution) over the per-request metrics to decide whether the difference is meaningful or noise. Reports the effect size in plain English: "Profile B's tg/s is 18% higher (p < 0.01, n = 30)".

Profiles can target different models — useful for comparing quantisations of the same family — or the same model with different flags.

## 5. Workload corpus

Repeatable workload matters more than people think. We ship a built-in corpus stratified by request shape:

| Bucket | Prompt size | Output size | Example purpose |
| --- | --- | --- | --- |
| `short_chat` | ~64 tokens | 64 tokens | Realistic Q&A turn |
| `medium_chat` | ~512 tokens | 256 tokens | Multi-turn with some history |
| `long_context` | ~8k tokens | 256 tokens | RAG / document summarisation |
| `huge_context` | ~32k tokens | 64 tokens | Pushing the KV cache |
| `code_completion` | ~256 tokens | 512 tokens | Code with structure |
| `creative_long` | ~64 tokens | 2048 tokens | Long generation, no prefill cost |

Workloads ship as fixed JSON files in `internal/bench/corpus/`, with deterministic prompts (so the same run on the same hardware is reproducible). Users can add their own corpus entries — drop a JSON file in `cfg.DataDir/bench/corpus/` and it appears in the dropdown.

A benchmark run picks **one** corpus entry and varies settings around it. Mixing workload shapes across a sweep would confound the result — keep workload constant inside a single run.

## 6. Statistical rigor

Don't average two numbers and call it a day. Each measurement run does:

1. **Cold-start guard.** Discard the first request entirely (loads CUDA kernels, pages in the model, warms caches). Doesn't count toward warmup or measurement.
2. **Warmup phase.** Run `warmup_count` (default 3) requests, time them, discard the data. Confirms the server is steady before we measure.
3. **Measurement phase.** Run `measurement_count` (default 10 for sweep cells, 100+ for concurrency tests) requests. Record every metric per request.
4. **Aggregate.** Compute mean, std dev, p50, p90, p99 over the measurement window.
5. **Variance gate.** If the std dev of tg/s is more than 20% of the mean, flag the cell as "noisy" in the output table — usually means thermal throttling, another process competing for the GPU, or an `--idle-timeout` issue.
6. **Repeat the whole run** if requested (default 1). Useful for comparing two times of day, or before/after a driver update.

For concurrency tests specifically: use a fixed wall-clock duration (e.g. 60 s) rather than a fixed request count, so the comparison is "throughput at this load" not "how long does N requests take".

## 7. Storage schema extension

Today's `storage.BenchmarkRun` carries a free-form `Result map[string]interface{}`. That worked for one number; it doesn't work for a sweep across 12 cells with 8 metrics each.

New schema (additive — old runs still parse):

```go
type BenchmarkRun struct {
    ID               string
    ProfileID        string
    ServerID         string                // primary profile (sweep base)
    Kind             string                // "single_shot" | "sweep" | "grid" | "concurrency" | "ab"
    SweepFlag        string                // e.g. "-b" — empty for single_shot/ab
    SweepValues      []string              // values that were swept
    WorkloadID       string                // corpus entry key
    ConcurrencyPlan  []int                 // for concurrency tests: [1, 2, 4, 8]
    StartedAt        string
    CompletedAt      string
    Status           string                // running | completed | failed | partial | cancelled
    Cells            []BenchmarkCell       // one per sweep value or per concurrency step
    Recommendation   string                // human-readable "best cell" pointer + brief why
}

type BenchmarkCell struct {
    Label            string                // "-b 512" or "concurrency=4"
    FlagOverrides    map[string]string     // applied on top of the profile for this cell
    StartedAt        string
    CompletedAt      string
    Status           string
    Samples          []BenchmarkSample     // one per measurement request
    Aggregates       BenchmarkAggregates   // mean/p50/p90/p99/std dev across Samples
    PeakVRAMBytes    int64
    IdleCPUPercent   float64               // sample taken between bursts
    Noisy            bool                  // variance gate fired
}

type BenchmarkSample struct {
    RequestIndex     int
    PromptTokens     int
    OutputTokens     int
    TTFTMS           float64
    EndToEndMS       float64
    ITLMS            []float64             // diffs between SSE events
    PromptPerSec     float64               // from /completion timings
    PredictedPerSec  float64               // from /completion timings
    HTTPStatus       int
    Error            string
}

type BenchmarkAggregates struct {
    TGSpeedMean      float64
    TGSpeedP50       float64
    TGSpeedP90       float64
    TGSpeedP99       float64
    TGSpeedStdDev    float64
    PPSpeedMean      float64
    PPSpeedP50       float64
    TTFTP50          float64
    TTFTP95          float64
    ITLP50           float64
    ITLP95           float64
    E2EP50           float64
    E2EP95           float64
    ThroughputTGS    float64               // aggregate tok/s across concurrent requests
    ErrorRatePercent float64
}
```

Two notes:

- `BenchmarkSample.ITLMS` is per-event, so a 100-token generation yields ~99 ITL values. Store them; histograms are easier than recomputing later.
- `BenchmarkCell.FlagOverrides` records exactly what was changed for this cell relative to the profile. Reproducible: the cell knows the full delta needed to recreate it.

Storage size concern: a concurrency run with 60s × 4 concurrency levels × 5 RPS easily hits 1200 samples × ~hundreds of ITL values each. Cap `ITLMS` to the first N + last N tokens per sample (default 50 + 50) so the file stays bounded, and store an opaque digest of the discarded middle.

## 8. UI / UX

A new "Benchmark" tab (peer of Catalog, Profiles, etc. — extending the existing one). Three sub-views:

**Configure** — pick the bench type, profile(s), workload, sweep/grid/concurrency parameters. Show a calibration probe ("≈3 minutes estimated") before launching anything that takes longer than 30 s.

**Live** — once running, a table of cells with their current sample count + a tiny sparkline of tg/s. The user can cancel at any time; partial results are kept.

**Results** — for completed runs:
- A sortable cell table with all the aggregates.
- Per-metric charts: bar (sweep), heatmap (grid), latency CDF (concurrency).
- A "Recommendation" box with the chosen-best cell highlighted: *"Best for tg/s: `-b 512 -np 2`, 41.3 tok/s ± 1.1 (n = 10). 14% faster than your profile's current settings."*
- A "Apply to profile" button that writes the chosen flag overrides into the profile, with a one-click rollback if the user changes their mind within 24 h (we keep the prior `p.Args` in a `BenchmarkRecommendationLog`).

**History tab** — list of past runs, filter by model, profile, kind. Two checkboxes select two runs for a side-by-side comparison view.

## 9. Choosing the "best" cell

There isn't one universal best. The Recommendation box defaults to a balanced objective but lets the user pick:

| Objective | Score | When to use |
| --- | --- | --- |
| Max tg/s | `mean(tg/s)` | Single-user latency-sensitive chat |
| Max aggregate throughput | `throughput_tgs` from concurrency runs | Many-user serving |
| Min p95 e2e latency | `-p95(e2e)` | Latency SLO matters more than peak speed |
| Best tg/s per GB VRAM | `mean(tg/s) / peak_vram_gb` | Capacity-constrained hardware |
| Stability | `mean(tg/s) / std_dev(tg/s)` | Production where predictability matters |

Score ties (within 2%) are reported as ties — we don't fake precision.

## 10. Integration with `llama-bench`

llama.cpp ships a tool called `llama-bench` that runs the inference engine without the HTTP layer. It's the reference for "raw model speed on this hardware" and is much less noisy than our HTTP-driven measurements.

For the **inference benchmark camp**, the implementation looks like:

1. Detect `llama-bench` next to the configured `llama-server` binary (`filepath.Dir(cfg.LlamaServerBin)`).
2. If present, run `llama-bench -m <model_path> -ngl X -b Y -p P -n N` with the sweep dimensions, capture stdout, parse the table.
3. If not present, fall back to HTTP-based measurement against a fresh `llama-server` child started with the same flags. Document the noise penalty in the UI ("results may vary ±5% vs. native llama-bench").

The two paths report into the same `BenchmarkCell` shape so downstream UI doesn't care which source produced the numbers.

## 11. Implementation order

Each step leaves the binary in a working state:

1. **Schema extension** — add `Kind`, `Cells`, `BenchmarkCell`, `BenchmarkSample`, `BenchmarkAggregates` to `storage.BenchmarkRun`. Migrate the existing `Result` field to a single-cell `Cells[0]` for backwards display.
2. **Workload corpus** — ship six built-in JSON files in `internal/bench/corpus/`. Add `GET /api/bench/corpus` to list them.
3. **Single-shot rewrite** — port today's runner to emit the new schema as a single-cell run. Add ITL and TTFT to the per-sample record by switching to streaming `/completion` and recording timestamps. No UI change yet.
4. **Sweep runner** — accepts a flag name + values list, spawns a fresh server (or restarts with new flags) per cell, runs the workload, records cells. Sequential — one cell at a time so resource contention doesn't bias results.
5. **Grid runner** — Cartesian sweep with the 24-cell cap and pre-run ETA probe.
6. **Concurrency runner** — Poisson traffic generator over goroutines, with per-request streaming parse for TTFT/ITL. Wall-clock-bounded.
7. **A/B runner** — two profiles, same workload, paired statistical test on TG speed.
8. **Recommendation engine** — score each cell against the chosen objective, pick the winner, write a one-paragraph "why" string.
9. **UI** — Configure, Live, Results, History sub-views. The "Apply to profile" round-trip with the recommendation log.
10. **`llama-bench` integration** — detect, parse, prefer for inference-only sweeps. Behind a feature flag at first.
11. **End-to-end smoke**: a small built-in CI workload (10 short_chat requests) the user can run to validate their setup in under 30 s.

## 12. Out of scope (deferred)

- **Quality benchmarks** (perplexity, MMLU, HumanEval). The studio measures speed, not correctness. Quality eval is a separate domain — we'd point users at lm-evaluation-harness rather than re-implementing it.
- **Cross-machine comparison**. Useful but needs a result-export format and a shared schema; defer until single-machine works well.
- **Auto-tune**. Closed-loop "keep adjusting flags until the metric stops improving" is tempting but expensive and prone to local-minima. Document a workflow ("sweep, look, apply, sweep again") instead.
- **GPU utilisation / SM occupancy / kernel breakdown**. nsys / nvprof territory. Out of scope for the dashboard; we surface VRAM peak and that's enough for tuning.
- **Power and thermal**. Important on laptops; harder to measure portably. Optional later.

## 13. Best-way summary — what to tell a new user

> *Hold the model and hardware constant. Pick one flag at a time and sweep it across a short list of values. Run the same workload for every cell. Discard the first request, warm up with three more, then measure ten. Look at p50 and p99, not just the mean. If two cells are within 2% of each other, treat them as a tie. When you find a flag's best value, lock it in and move to the next flag — don't try to tune two at once unless you have a specific reason. For serving questions ("how many users can I handle?"), run the concurrency test instead — single-prompt benchmarks lie about that.*

Everything else in this document is mechanics for making that workflow easy.
