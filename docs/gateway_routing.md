# OpenAI-Compatible Gateway Routing — Design

Status: design accepted, not yet implemented
Scope: adds model-field routing on top of the existing per-profile reverse proxy in `internal/router/proxy.go`, exposes OpenAI-standard URLs on the gateway port, and keeps the legacy `/profiles/{id}/v1/...` paths working as a backwards-compat shim.

## 1. Problem

Today, calling a profile through the gateway requires a non-standard URL:

```
POST /profiles/{profile_id}/v1/chat/completions
```

That works, but no off-the-shelf OpenAI client speaks it. Users have to either curl by hand or wrap each profile in a custom client. We want to mimic the standard:

```
GET  /models                              → list profiles
POST /v1/chat/completions                 → body has {"model": "<profile_id>"}
POST /v1/completions                      → same
POST /v1/embeddings                       → same
```

Drop-in for the OpenAI Python SDK, LangChain, LlamaIndex, llama.cpp's own `llama-cli --api`, anything else that sends a `model:` field.

## 2. Target behaviour

A single gateway server (the one already running on `port+1`, default `127.0.0.1:3101`) operates in two modes:

**Intercepted endpoints.** A small number of paths are served by the studio itself, not proxied to any backend:

- `GET /models` and `GET /v1/models` — list every profile in the catalog, regardless of whether its child server is currently running. Each entry includes a `studio_status` extension field (`"ready"` | `"stopped"`) so a smart client can filter.
- `GET /v1/models/{model_id}` — single-model retrieve, OpenAI-standard. 404 if no profile by that id.
- `GET /health` — gateway liveness (does **not** probe any child).

**Transparent passthrough for everything else.** Any other request path is treated as a model-routed proxy call: extract a `model` identifier from the request, look up the matching profile, and forward the request **byte-for-byte** (method, headers, query string, body, including multipart and binary payloads) to that profile's running llama-server child. The response (including `text/event-stream`, `audio/*`, `image/*`, `application/octet-stream`, anything) streams back unchanged.

This is the key design decision: the gateway is a thin router, not an endpoint enumerator. Whatever the backend supports — chat completions, vision (image_url in chat content), embeddings, rerank, tokenize/detokenize, the native `/completion` and `/infill`, llama-server's `/props` and `/slots`, future OpenAI-spec endpoints that land in upstream llama.cpp — works through the gateway with zero per-endpoint code. If a backend doesn't implement a path, its native 404 propagates back to the client.

### What llama-server actually supports today

Documentation that the gateway's surface inherits whatever llama-server exposes — which is currently a text-focused subset of the OpenAI API:

| Capability | OpenAI path | llama-server today |
| --- | --- | --- |
| Chat completions (text) | `POST /v1/chat/completions` | yes |
| Vision (images in chat) | same, `content[].image_url` | yes — requires the profile to be launched with `--mmproj` and a multimodal model |
| Legacy completions | `POST /v1/completions` | yes |
| Embeddings | `POST /v1/embeddings` | yes |
| Rerank | `POST /v1/rerank` and `POST /rerank` | yes |
| Tokenize / detokenize | `POST /v1/tokenize`, `/v1/detokenize` | yes (also exposed without `/v1/` prefix) |
| Native completion | `POST /completion`, `POST /infill` | yes (not OpenAI-standard but commonly used) |
| Slots / metrics / props | `GET /slots`, `/metrics`, `/props` | yes |
| Audio (Whisper) | `/v1/audio/transcriptions`, `/v1/audio/speech` | no — llama-server does not implement these |
| Image generation | `/v1/images/generations` | no |
| Files API | `/v1/files`, `/v1/files/{id}` | no |
| Fine-tuning API | `/v1/fine_tuning/jobs` | no |
| Assistants / Threads / Vector Stores | `/v1/threads/...` | no |
| Responses API | `/v1/responses` | not yet (upstream llama.cpp tracking it) |

When a user calls an unsupported path, they get the backend's 404 — clean failure mode, no studio-specific knowledge required. If/when llama-server adds Responses or any new OpenAI endpoint, the gateway picks it up automatically with no code change.

Rejection rules, in order:

1. `model` field missing or empty:
   - If a default model is configured on the gateway, use it.
   - Otherwise → **400** with OpenAI-style error envelope.
2. No profile with that id → **404**.
3. Profile exists but has no running server → **409** with a clear "start it from the Server Lifecycle tab" message.
4. Profile has a running server → forward.

Explicitly **not** in this iteration: auto-start of a stopped profile. The existing `/profiles/{id}/v1/...` route keeps its auto-start branch; the new `/v1/...` paths do not auto-start.

### Default model

The gateway has a single optional `default_model` setting — a profile id chosen by the operator from the Security Gateway tab. When a request hits `/v1/chat/completions` (or any of the model-routed endpoints) without a `model` field, the gateway substitutes the default and proceeds. This matches Ollama's behaviour of letting `ollama run` callers omit the model when they have a sensible default configured. The default is persisted in `config.json` as `gateway_default_model` (string profile id; empty string means "no default — require explicit model in every request").

If the default is set but the profile it points at has been deleted (e.g. the user removed the profile after configuring the default), the gateway returns a clear error rather than silently failing — see §8 below. The profile-delete handler also auto-clears the default if it matches the deleted profile id, so this state is short-lived in practice.

## 3. The `model` field is the profile id

There is no separate model-to-profile mapping table. The canonical model name in the gateway API is the slugified profile id (e.g. a profile named "Qwen 2.5 7B Q4" becomes id `qwen-25-7b-q4`, and that's the string clients send). Profile ids are visible in the Profile Builder UI and in the JSON exports — users already know them.

Two consequences worth being explicit about:

- Renaming a profile updates its display name but **not** its id. Clients keep working across renames.
- Cloning a profile creates a new id (`profile-clone`, `profile-clone-1`, etc.). The clone is addressable separately and clients that hard-code ids won't accidentally start hitting it.

## 4. Endpoint surface

The gateway has exactly four explicit handlers, plus one catch-all:

| Method | Path | Handler | Behaviour |
| --- | --- | --- | --- |
| `GET` | `/models` | intercepted | Alias for `/v1/models`. |
| `GET` | `/v1/models` | intercepted | OpenAI-style list of all profiles + `studio_status` + `studio_is_default`. |
| `GET` | `/v1/models/{model_id}` | intercepted | OpenAI-style retrieve. 404 if no such profile. |
| `GET` | `/health` | intercepted | `{"ok": true}` when the gateway token is configured. |
| **any** | **any other path** | catch-all | Model-routed transparent proxy — see §6. |

The catch-all is implemented as Go's mux fallback (`mux.HandleFunc("/", ...)`), so any future llama-server endpoint just works. The four explicit handlers take precedence over the catch-all by virtue of being more specific.

Admin-side (on the main studio API port, behind admin auth — not on the gateway port):

| Method | Path | Behaviour |
| --- | --- | --- |
| `POST` | `/api/settings/gateway-default` | Body `{"profile_id": "<id>"}` (or `""` to clear). Validates the profile exists, persists to `config.json`. |
| `GET` | `/api/settings` | Existing endpoint; gains a `gateway_default_model` field in its sanitised response. |

Legacy gateway routes (kept as-is for compatibility):

| Method | Path | Behaviour |
| --- | --- | --- |
| `POST` | `/profiles/{profile_id}/v1/chat/completions` | URL-based dispatch instead of body-based. Keeps auto-start. |
| `POST` | `/profiles/{profile_id}/v1/completions` | Same. |
| `POST` | `/profiles/{profile_id}/v1/embeddings` | Same. |
| `POST` | `/profiles/{profile_id}/rerank` | Same. |
| `GET` | `/profiles/{profile_id}/health` | Same. |

All routes — explicit, catch-all, and legacy — sit behind the existing `gatewayAuth` middleware, so the gateway token requirement is unchanged.

## 5. Listing response shape

`GET /v1/models`:

```json
{
  "object": "list",
  "data": [
    {
      "id": "qwen-25-7b-q4",
      "object": "model",
      "created": 1716499200,
      "owned_by": "llama-server-studio",
      "studio_status": "ready",
      "studio_profile_name": "Qwen 2.5 7B Q4",
      "studio_port": 41003,
      "studio_is_default": true
    },
    {
      "id": "llama-31-8b-instruct",
      "object": "model",
      "created": 1716501733,
      "owned_by": "llama-server-studio",
      "studio_status": "stopped",
      "studio_profile_name": "Llama 3.1 8B Instruct",
      "studio_port": 0
    }
  ]
}
```

- `created` is `Unix(profile.CreatedAt)`. If parse fails, fall back to 0.
- `studio_status` is `"ready"` when there's a server with `Status in {"healthy","ready"}` for that profile; `"stopped"` otherwise (covers `starting`, `crashed`, `stopped`, and "no server ever started"). Clients that care about the difference can hit the legacy `/profiles/{id}/health` for a deeper check.
- `studio_port` is `0` when no server is running. Mostly useful for debugging via `curl`.
- `studio_is_default` is present and `true` on exactly the entry that matches `cfg.GatewayDefaultModel`. Omitted on all other entries (so JSON stays small).
- All `studio_*` fields are extensions; vanilla OpenAI clients ignore them.

`GET /v1/models/{model_id}` returns a single model object (the same shape, minus the `object: "list"` wrapper), or 404 if no such profile.

## 6. Routing logic

The catch-all flow:

```
any METHOD any /path  (not one of the four intercepted)
└── gatewayAuth middleware (already exists)
    └── handleGatewayCatchAll
        ├── extractModel(r) — tries, in order:
        │     1. JSON body:        {"model": "<id>"}     (Content-Type: application/json)
        │     2. multipart form:   "model" form field    (Content-Type: multipart/form-data)
        │     3. urlencoded form:  "model" form field    (Content-Type: application/x-www-form-urlencoded)
        │     4. query string:     ?model=<id>
        │     5. URL path:         /v1/models/<id>/...   (rarely used today, reserved)
        ├── If still empty:
        │     ├── If cfg.GatewayDefaultModel != "" → use it
        │     └── Else → 400 (missing_model)
        ├── Look up profile by id  — 404 if none
        │     └── If the id came from cfg.GatewayDefaultModel,
        │         return default_profile_missing (see §8).
        ├── Find a running server for the profile  — 409 if none
        ├── Restore r.Body to a buffered reader (see "Body handling" below)
        └── proxyRouter.ProxyByModel(w, r, profile, server)
            └── Reuses the reverse-proxy core. URL path passes through
                unchanged (we don't strip a prefix — the request URL is
                already what the backend expects).
```

### Body handling — three regimes

The body extraction logic adapts to the request's `Content-Type` to avoid eating large uploads into memory needlessly:

1. **JSON requests** (`application/json`): buffer the full body up to `cfg.GatewayMaxJSONBytes` (default 16 MiB), parse with a tolerant struct probe (`{"model": string}` plus an empty interface for everything else), restore as `io.NopCloser(bytes.NewReader(buf))`. This covers chat completions, completions, embeddings, rerank, tokenize, native `/completion`/`/infill`.

2. **Multipart and form-encoded requests** (`multipart/form-data`, `application/x-www-form-urlencoded`): buffer up to `cfg.GatewayMaxUploadBytes` (default 64 MiB), parse the form with `mime/multipart` (or `r.ParseForm()` for urlencoded), extract `model`, then restore the body so the proxy can replay it byte-for-byte to the backend. We **do not** re-encode the multipart — that risks subtle boundary issues. Instead, we re-read from the buffered byte slice. This covers any future audio upload (`/v1/audio/transcriptions`), file upload (`/v1/files`), etc. The 64 MiB default is generous; configurable for users who actually need large uploads.

3. **No-body or query-only requests** (`GET`, `DELETE`, `HEAD` with no body, or any method with `Content-Length: 0`): skip body parsing entirely, look for `?model=` in the query string only.

If `Content-Length` exceeds the applicable cap, return 413 immediately without reading the body.

### Streaming responses

Already handled. `httputil.NewSingleHostReverseProxy` with `FlushInterval = 50 * time.Millisecond` flushes chunks as they arrive, which covers:

- `text/event-stream` (chat completions streaming)
- `audio/*`, `image/*`, `application/octet-stream` (TTS, image gen, binary downloads if the backend ever supports them)
- Long-lived connections with periodic keepalive bytes

No new code path needed for binary responses — the proxy is content-type-agnostic.

## 7. Picking which server to route to

When multiple servers exist for the same profile (e.g. a restart that left an orphan), pick the same way the existing `ProxyRequest` does today: **latest `StartedAt` among `Status in {"healthy","ready","starting"}`**. If only `starting` candidates exist (no healthy/ready), 409 — we don't proxy to a server that hasn't passed its first health check, because that's how clients get long hung requests on cold start.

## 8. Error envelope — OpenAI-compatible

All error responses use:

```json
{
  "error": {
    "message": "Model 'qwen-25-7b-q4' has no running server. Start it from the Server Lifecycle tab.",
    "type": "studio_server_not_running",
    "code": "server_not_running",
    "param": "model"
  }
}
```

`type` is studio-namespaced so external observability tools can't confuse our errors for OpenAI-upstream ones. `param` names the offending JSON field where it makes sense (`"model"` for missing/unknown/stopped, omitted for everything else).

Defined error types in this iteration:

| HTTP | `type` | `code` | When |
| --- | --- | --- | --- |
| 400 | `invalid_request_error` | `missing_model` | `model` field missing or empty, and no `default_model` is configured |
| 400 | `invalid_request_error` | `invalid_body` | body is not valid JSON, or > 16 MiB |
| 404 | `invalid_request_error` | `model_not_found` | no profile with that id |
| 404 | `studio_default_stale` | `default_profile_missing` | request used the configured default, but that profile has been deleted — operator needs to pick a new default on the Security Gateway tab |
| 409 | `studio_server_not_running` | `server_not_running` | profile exists but no running server |
| 502 | `studio_upstream_error` | `child_unreachable` | child server returned a connect error |
| 503 | `studio_gateway_disabled` | `gateway_disabled` | no gateway token configured |

Add a tiny helper `writeOpenAIError(w, httpCode, message, errType, code, param)` next to `writeJSONError` in `api.go`.

## 9. Refactoring `internal/router/proxy.go`

The current `ProxyRequest(w, r, profileID)` does too many things at once:

1. Validates the gateway token (with a plaintext-only compare — broken in bcrypt mode).
2. Looks up the profile, finds a server, optionally auto-starts.
3. Builds/caches a reverse proxy and serves.

Split it into:

- `ProxyByProfile(w, r, profileID, opts)` — existing URL-based dispatch. URL rewriter strips the `/profiles/{id}` prefix. `opts.AutoStart = true` by default (matches today's behaviour).
- `ProxyByModel(w, r, model)` — new body-based dispatch. URL rewriter is identity (the request URL is already `/v1/chat/completions`). `opts.AutoStart = false`.
- Shared `serveProfile(w, r, profile, opts, rewriteURL func(*http.Request))` — the core that does server-lookup, optional auto-start, proxy-cache lookup, and `proxy.ServeHTTP`.

**Drop the redundant gateway-token check at lines 71–87.** The `gatewayAuth` middleware in `api.go` already checks the token correctly (using `HasGatewayToken` + `VerifyGatewayToken`, which handle bcrypt). The duplicate check in `proxy.go` only ever sees the plaintext field, so if a user has configured their token via the secure UI (bcrypt-only), the proxy returns `503 disabled` on every request. Removing the duplicate fixes the bcrypt path as a side effect.

## 10. Frontend tweak — Security Gateway tab

The "Client Integration Example" `<pre>` block currently shows the legacy URL. Update it to the new short form:

```
curl http://127.0.0.1:3101/v1/chat/completions \
  -H "Authorization: Bearer sk-xyz" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen-25-7b-q4",
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

Plus a small note: "The legacy URL `/profiles/{id}/v1/chat/completions` continues to work." Add a copy button (we already have a `Copy curl` pattern elsewhere) so users can drop it into their client config in one click.

### Default model picker

A new section on the Security Gateway tab — below the existing token UI, above the Client Integration Example — lets the operator pick a default model:

```
┌─ Default Model ────────────────────────────────────────┐
│  Clients that don't send a `model` field will be       │
│  routed to this profile.                               │
│                                                        │
│  [ Profile dropdown ▾ ]   [ Save ]   [ Clear default ] │
│                                                        │
│  Currently: Qwen 2.5 7B Q4 (qwen-25-7b-q4)             │
│  Status: ✓ ready                                       │
└────────────────────────────────────────────────────────┘
```

UI behaviour:

- The dropdown lists every profile id (the same set the user sees in the Profiles tab), labelled `<profile_name>  (<profile_id>)` so they pick by friendly name but commit by id. The first option is a placeholder "— No default —" that clears the setting.
- "Save" calls `POST /api/settings/gateway-default` with the chosen id; "Clear default" sends an empty id.
- After save, the "Currently:" line refreshes from `GET /api/settings`, and the status badge mirrors the `studio_status` field from `GET /v1/models` for that profile (ready / stopped). If the configured default no longer exists (the profile was deleted), the status badge shows a red "missing" warning with a one-click "Clear default" button.
- The "Currently:" block is empty when no default is set, with a hint: "No default configured. Requests must include a `model` field."

A second example panel for the OpenAI Python SDK is a nice-to-have:

```python
from openai import OpenAI
client = OpenAI(
    base_url="http://127.0.0.1:3101/v1",
    api_key="sk-xyz",
)
client.chat.completions.create(
    model="qwen-25-7b-q4",
    messages=[{"role": "user", "content": "Hello!"}],
)
```

## 11. Edge cases — explicit decisions

- **Streaming responses (`stream: true`)**: handled by `FlushInterval = 50 * time.Millisecond` in the existing reverse proxy. Works for SSE chat completion streams, and for any future binary stream (audio, image bytes).
- **Vision / multimodal in chat**: zero code change. The chat-completions body with `content: [{type: "image_url", image_url: {url: "data:image/png;base64,..."}}]` is forwarded byte-for-byte. The backend handles the image; the gateway is transparent. Caveat: the matched profile must have been launched with `--mmproj` and a multimodal GGUF, otherwise llama-server returns its own error which we propagate.
- **Audio responses (TTS) and binary downloads**: also zero code change. The proxy streams arbitrary `Content-Type`s through with the same flush interval. If llama-server (or a future companion service behind a profile) emits `audio/wav`, the client receives it intact.
- **Large multipart uploads** (audio transcription, file API, fine-tuning datasets): buffered up to `cfg.GatewayMaxUploadBytes` (default 64 MiB) before forwarding. Beyond that → 413. Configurable for users who legitimately need huge uploads; we recommend pairing a larger cap with a separate file-storage backend rather than streaming through the gateway.
- **Trailing slashes**: `/v1/chat/completions/` differs from `/v1/chat/completions`. The catch-all handles both, but the *backend* may treat them differently. We pass through as-is.
- **Body without a `model` field on a non-OpenAI endpoint** (e.g. `/tokenize` or `/v1/audio/speech` where some clients omit `model`): substitute `cfg.GatewayDefaultModel` if set, else 400. Same fallback logic as JSON endpoints.
- **Default model points at a deleted profile**: 404 `default_profile_missing` (rather than the generic `model_not_found`) so the operator can distinguish "client sent a typo" from "your gateway default is stale". The profile-delete handler auto-clears the default in `config.json` if it matches the deleted id, so this state should be transient.
- **Embeddings with batch input**: `input` can be a string or an array; the model probe ignores everything except `model`. Same parsing path works regardless of payload shape.
- **Reranking**: llama-server exposes `/rerank` (no `/v1/` prefix). Both `POST /v1/rerank` and `POST /rerank` work — the catch-all forwards the URL path unchanged, and the backend honours either.
- **Native llama-server paths** (`/completion`, `/infill`, `/tokenize`, `/detokenize`, `/props`, `/slots`, `/metrics`): forwarded unchanged. Useful for users coming from llama.cpp's native client. For paths where `model` isn't part of the payload (e.g. `/tokenize`), the default must be configured.
- **CORS**: existing `gatewayAuth` already sets `Access-Control-Allow-Origin: *` for the gateway port. The catch-all inherits it. Multipart preflight (`OPTIONS`) is short-circuited in the middleware before our handlers see it.
- **HEAD on `/v1/models`**: Go's mux handles this for GET routes automatically (responds without body). Fine.
- **Unsupported endpoints**: when the user calls e.g. `POST /v1/audio/transcriptions` while llama-server doesn't implement it, the backend's native 404 propagates. The gateway doesn't pre-filter — that would be brittle as the backend evolves.

## 12. Implementation order

Each step leaves the binary in a working state:

1. **Refactor `proxy.go`** — extract `serveProfile`, rename existing entry point to `ProxyByProfile`, add `ProxyByModel`. `ProxyByModel` passes the URL path through unchanged (no prefix stripping). Delete the redundant gateway-token check. No behavioural change for existing callers.
2. **Add `writeOpenAIError` helper** in `api.go`.
3. **Add `GatewayDefaultModel`, `GatewayMaxJSONBytes` (16 MiB), `GatewayMaxUploadBytes` (64 MiB)** to `config.Config`. Surface `gateway_default_model` via the existing `GET /api/settings` response and add a `POST /api/settings/gateway-default` handler that validates against `db.GetProfile` and persists via `config.SaveConfig`. Auto-clear the default in `handleDeleteProfile` when the deleted profile matches.
4. **Add intercepted handlers in `RegisterGatewayRoutes`**: `GET /models`, `GET /v1/models`, `GET /v1/models/{model_id}`, `GET /health`. Include `studio_is_default` and `studio_status` extensions in the listing.
5. **Add `extractModel(r)` helper** that tries JSON body → multipart form → urlencoded form → query → URL path, with Content-Type awareness so we don't read large multipart streams when we don't need to.
6. **Wire the catch-all** with `mux.HandleFunc("/", gatewayAuth(s.handleGatewayCatchAll))`. The handler runs `extractModel`, applies the default fallback, looks up the profile, restores the body, and dispatches via `ProxyByModel`. Size-capped per §6.
7. **Frontend update** — Security Gateway tab: integration examples + Default Model picker + a small "Endpoints" panel that lists what's available and what's intercepted, so the user can see at a glance "yes, vision works; no, audio transcription is not yet in llama-server".
8. **Build, `go vet`, smoke test**: list models, hit `/v1/chat/completions` with missing/unknown/stopped model both with and without a default configured, exercise `/v1/embeddings`, `/v1/rerank`, `/tokenize`, the native `/completion`. Send a multipart request to `/v1/audio/transcriptions` (expecting the backend's 404 to propagate cleanly). Delete a profile that is the configured default and confirm it auto-clears. Verify legacy URLs still work.

## 13. Out of scope (deferred)

- Auto-start of a stopped profile on first request. Possible later as a per-profile opt-in (mirrors the existing `Routing.AutoStart` flag on the legacy path).
- llama.cpp router-mode adoption (one `llama-server --models-preset` child handling all routing). That's a much larger redesign — see the prior discussion in chat. This document is the smaller, additive step that gets us OpenAI-compatible URLs without changing the supervisor model.
- Per-model rate limiting and per-token quotas.
- Streaming body forwarding for very large multipart uploads (we buffer up to `GatewayMaxUploadBytes`). Streaming pass-through is possible but requires us to peek at the multipart prefix without consuming it — non-trivial, deferred until someone actually needs >64 MiB uploads.
- Bridging non-llama-server backends (whisper.cpp for audio, stable-diffusion.cpp for images) under a unified gateway. The catch-all design makes this *possible* — point a profile at a whisper.cpp server and `/v1/audio/transcriptions` will work — but the studio's Profile Builder is currently llama.cpp-specific. Generalising it is a separate workstream.
