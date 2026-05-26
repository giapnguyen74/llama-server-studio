# Multimodal (mmproj) Support — Design

Status: design accepted, not yet implemented
Scope: teach the scanner, catalog, and Profile Builder about projection-only GGUF files (`mmproj-*.gguf`) so that vision-capable models like Qwen2-VL, Llama-3.2-Vision, MiniCPM-V, LLaVA, and similar Just Work without users having to hand-edit args.

## 1. The problem

llama.cpp's multimodal models ship as **two files**:

- The **base model** GGUF (text/LLM weights) — the file we already catalog.
- A **projection model** GGUF (vision/audio encoder + projector layers) — by convention named `mmproj-*.gguf`.

To run such a model, llama-server takes `--mmproj <path>` in addition to `-m <path>`. Without the mmproj, the model loads as text-only and silently drops any `image_url` in the chat content.

Today's scanner has two problems:

1. It treats `mmproj-*.gguf` files as if they were standalone models, polluting the catalog with entries that *cannot* be served on their own — clicking "Build profile" on an mmproj file produces a broken profile that will fail at startup.
2. Even when the user picks the right base model, there's no surface for "this model has a sibling projector — would you like to enable multimodal?". The user has to discover the `--mmproj` flag themselves and type the path into the raw-args mode.

This design fixes both: filter mmprojs out of the catalog, and pair them with their sibling base models so the Profile Builder can offer one-click multimodal.

## 2. What counts as an mmproj

Two-tier detection, fast path first:

**Filename heuristic (fast).** Match any of:

- `^mmproj-.*\.gguf$` — the dominant convention (unsloth, ggml-org, bartowski).
- `^mmproj\.gguf$` — older / single-file convention.
- `.*[-_]mmproj([-_.].*)?\.gguf$` — defensive, catches `Qwen2-VL-mmproj-F16.gguf` and similar.

All matches are case-insensitive. If the filename matches, treat as mmproj. No header read needed.


## 3. Pairing rule

When two GGUFs live in the **same directory** (after symlink resolution), and one is an mmproj while the other isn't, they're paired.

Concrete cases:

- `model-Q4_K_M.gguf` + `mmproj-F16.gguf` → the model pairs with that mmproj.
- `model-Q4.gguf`, `model-Q5.gguf`, `model-Q8.gguf` + `mmproj-F16.gguf` → all three text models pair with the same mmproj. Each gets the same `MMProjCandidates: ["mmproj-F16.gguf"]`.
- `model-Q4.gguf` + `mmproj-F16.gguf` + `mmproj-F32.gguf` → both mmprojs are candidates; the Profile Builder dropdown shows both.
- `model.gguf` in a folder with no mmproj → no multimodal option offered, no change from today.
- `mmproj.gguf` alone in a folder → filtered out, not catalogued, no error.

Out of scope: cross-folder pairing. If someone keeps mmprojs in a separate directory from base models, they have to either move them next to the model or add `--mmproj` manually in the profile's raw args.

Hugging Face cache layout (`models--<author>--<name>/snapshots/<hash>/`) already keeps mmprojs next to the model — the same-directory rule covers that case cleanly.

## 4. Catalog changes

**Filter mmprojs out of the catalog entirely.** The scanner classifies every `.gguf` as either a normal model (added to the catalog) or an mmproj (recorded for pairing but never inserted as a standalone model). They simply do not appear in:

- `GET /api/models`
- The Catalog table in the UI
- The model dropdown in the Profile Builder

**Add a "Vision" badge to paired models.** Models that have at least one mmproj candidate get `"vision"` appended to their existing `Capabilities` array. The catalog table's Capabilities column already renders capability pills; the new pill needs no special handling, just a status colour. Suggested colour: cyan (same as `embedding` for visual coherence) with the label `vision`.

**Model detail modal — Multimodal section.** When viewing a model's details, show a small new block under "Capabilities":

```
Multimodal (vision):
  • mmproj-Qwen2-VL-7B-F16.gguf  (1.7 GB)
  • mmproj-Qwen2-VL-7B-F32.gguf  (3.4 GB)
```

Each entry shows the filename and size. No actions on the entries themselves — they're informational. The "Build profile" button on the model is where the user actually opts in.

## 5. Profile Builder — Multimodal section

When the user picks a model in the Profile Builder, the model lookup yields `MMProjCandidates`. If non-empty, render a new section below the Resource Settings:

```
┌─ Multimodal ─────────────────────────────────────────┐
│  This model includes a vision projector.             │
│                                                      │
│  ☐ Enable multimodal (--mmproj)                      │
│      └─ Projector: [ mmproj-Qwen2-VL-7B-F16 ▾ ]      │
│                                                      │
│  Adds ~0.3 GB VRAM. Required for image_url content   │
│  in chat completions. Audio support depends on the   │
│  projector and llama.cpp version.                    │
└──────────────────────────────────────────────────────┘
```

- Checkbox: when toggled on, the dropdown enables and the `--mmproj <path>` pair is added to `p.Args` (we already have similar one-flag toggle patterns for `--flash-attn`).
- Dropdown: shows the basename of each candidate, ordered alphabetically. Default selection is the first candidate (deterministic across reloads).
- Hint: a short explainer + the estimated VRAM impact.

When toggled off, the `--mmproj` arg pair is removed from `p.Args`. The toggle is the source of truth — it reads the current args on load to set its initial state.

If the model has no mmproj candidates, the entire section is hidden. No empty placeholder, no "no projector found" message — that's noise for the 95% of profiles that don't care.

The CLI command preview at the bottom of the Profile Builder shows the resolved `--mmproj <abs_path>` so the user sees exactly what gets executed.

## 6. VRAM estimator

The estimator at `web/app.js:updateVRAMEstimate()` adds a small overhead when multimodal is enabled. mmproj sizes in practice:

| Projector flavour | Typical size | VRAM at load |
| --- | --- | --- |
| Qwen2-VL F16 | ~1.6 GB | ~1.7 GB |
| Llama-3.2-Vision F16 | ~1.0 GB | ~1.1 GB |
| MiniCPM-V F16 | ~0.6 GB | ~0.7 GB |
| LLaVA / CLIP base | ~0.3 GB | ~0.35 GB |

The estimator can read the actual mmproj file size off `MMProjCandidates` (we already record size during the scan) and add `size * 1.05` to the VRAM estimate. No need to maintain a per-projector table — file size is a good proxy. The 1.05 multiplier accounts for CUDA workspace.

## 7. HF Hub downloader interaction

When the user fetches a multimodal repo (e.g. `ggml-org/Qwen2.5-VL-7B-Instruct-GGUF`), the file list includes both `model-*.gguf` and `mmproj-*.gguf`. Two small touches:

- In View B (file picker), automatically tick the matching mmproj when the user ticks a base model (and detect "matching" by repo identity — within one repo, all mmprojs match any base model).
- A small hint above the picker: "This repo contains a vision projector (mmproj). Select it together with the base model to enable multimodal."

When the download finishes and `AddManualModel` registers the new base model, the scanner pairs it with the mmproj sibling automatically because both files land in the same `~/.llama-server-studio/models/<safe_repo>/` directory. No special download-time wiring needed beyond the auto-tick hint.

## 8. Storage schema changes

Additive to `storage.Model`:

```go
type Model struct {
    // ... existing fields ...

    // MMProjCandidates lists absolute paths of mmproj-*.gguf files that live
    // in the same directory as this model.  Empty for text-only models.
    // Populated by the scanner, refreshed on every scan.
    MMProjCandidates []string `json:"mmproj_candidates,omitempty"`

    // MMProjSizesBytes is parallel to MMProjCandidates — the file size of
    // each candidate, cached so the UI doesn't need to stat() on render.
    MMProjSizesBytes []int64 `json:"mmproj_sizes_bytes,omitempty"`
}
```

Existing models in the JSON DB without these fields parse cleanly (Go's `json.Unmarshal` leaves them nil). A rescan repopulates them on first boot after the upgrade.

No changes to `storage.Profile`. Multimodal state lives entirely in `p.Args` as `--mmproj <path>`, same as every other llama-server flag. This keeps profile JSON portable — exporting a profile preserves the mmproj path, importing on another machine the user can fix up the path manually if it differs (same caveat as the base model path).

## 9. Scanner implementation sketch

The current `walkDirRecursive` calls `processFile(path)` for every `.gguf`. Three changes:

1. **Two-pass per directory.** Instead of processing files immediately during walk, collect candidates per directory: split into `models[]` and `mmprojs[]` by filename heuristic.
2. **After scanning a directory**, for each entry in `models[]`, call `processFile(path, mmprojs)` with the per-directory mmproj list.
3. `processFile` populates `Model.MMProjCandidates` and `Model.MMProjSizesBytes` from the passed list, and appends `"vision"` to `Capabilities` if the list is non-empty.

mmproj files are never passed to `db.SaveModel` — they exist only in memory during the scan and contribute to the base models' metadata.

Edge case: same physical file reachable through multiple paths (already handled by `scannedFiles` dedup at `scanner.go:104`). The dedup keys off resolved paths, so a symlink farm doesn't cause double-counting. We extend the same dedup to mmproj resolution: an mmproj at `/models/snapshot-A/mmproj.gguf` and another at `/models/snapshot-B/mmproj.gguf` that both `EvalSymlinks` to `/blobs/sha256-abc` are a single candidate.

## 10. Edge cases — explicit decisions

- **Model file *named* like an mmproj but isn't.** Vanishingly rare in practice; the filename heuristic is owned by the user's filesystem and we trust it. If someone has a file called `mmproj-experiment.gguf` that's actually a base model, they have to rename. We don't try to second-guess.
- **Multiple base models + one mmproj in the same folder.** All base models pair with the same mmproj. No ambiguity.
- **Multiple mmprojs + one base model.** All mmprojs are candidates; the dropdown shows them all. Default selection is the first alphabetically (which on a clean HF download is the highest-precision projector — `F32` before `F16` lexicographically; could swap to "by size descending" if we want the heaviest by default).
- **mmproj newer than its sibling model.** No problem — the scanner pairs on disk state at scan time. A rescan picks up the new file.
- **mmproj loaded into a profile, then the file moves on disk.** llama-server fails at startup with a clear error; the studio surfaces that error in the Server Lifecycle tab like any other start failure. The Profile Builder doesn't validate paths in real time — that would add stat() noise on every keystroke.
- **Audio mmprojs.** Some newer llama.cpp builds support audio projectors through the same `--mmproj` flag (whisper-style projectors for voice in chat). The detection and pairing rule is identical to vision. The UI label says "vision" because that's the common case; we'll relabel to "multimodal" if and when audio projectors become common.
- **Manual user override.** Users editing the profile in Advanced (raw args) mode can put any `--mmproj` path they want, even one that isn't in `MMProjCandidates`. The Multimodal section toggle in Simple mode reflects the underlying args — if it sees a `--mmproj` value not in the candidate list, the checkbox is checked but the dropdown shows the literal value as a "(custom)" entry.

## 11. Implementation order

Each step leaves the binary in a working state:

1. **Scanner filter and pairing** — `scanner.go` learns the mmproj filename heuristic, runs the per-directory two-pass collection, populates `Model.MMProjCandidates` and `Model.MMProjSizesBytes`. mmprojs no longer appear in `db.SaveModel`. Backwards-compatible: old DB entries lose nothing.
2. **Capability tagging** — append `"vision"` to capabilities when candidates exist. Existing UI renders the new pill with no code change.
3. **Model detail modal block** — small front-end addition to show the candidate list under "Capabilities" in the model detail modal.
4. **Profile Builder Multimodal section** — toggle + dropdown, wired to `p.Args` add/remove of the `--mmproj` pair. Reads candidates from the selected model's record.
5. **VRAM estimator** — pull `MMProjSizesBytes` and add `size * 1.05` to the baseline when multimodal is on.
6. **HF Hub auto-tick + hint** — small UX touch in the File Picker view.
7. **`AddManualModel` re-pair** — when a single file is added (e.g. via the HF downloader's per-file `AddManualModel` call), also re-scan its directory to find sibling mmprojs and update the just-added record. Otherwise a freshly-downloaded model misses its mmproj until the next full rescan.

## 12. Out of scope (deferred)

- **Cross-folder pairing**. Users with non-standard directory layouts have to use raw args mode. Pursuing folder-spanning pairing risks false positives (an mmproj that happens to share part of a name with an unrelated model).
- **Auto-download of missing mmprojs**. "This model says it's multimodal but you don't have an mmproj — want me to fetch one from HF?" is appealing but heuristic; defer until we have a robust catalog of model → mmproj associations.
- **In-line image upload from the Quick Test panel**. Today's quick test sends `prompt` to `/completion`; chat with images uses `/v1/chat/completions` with `content` blocks. Wiring image upload into the playground is a separate UX surface — worth doing, but not part of this design.
- **Validation that the chosen mmproj actually matches the base model architecture**. llama.cpp will tell us at startup if it doesn't. We surface that error like any other start failure; we don't pre-validate.
- **A "test multimodal" round-trip** that sends a tiny image to verify the projector loaded correctly. Nice-to-have once the rest is in place.
