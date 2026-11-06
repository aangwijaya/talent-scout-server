# Talent Scout — REST API Contract (v1)

> **This is a contract.** The file is identical in `talent-scout-server/docs/` and `talent-scout-client/docs/`. On Day 1 the backend agent derives `openapi.yaml` (OpenAPI 3.1) from this document; the client generates TS types from `openapi.yaml`. A contract change = change this file first in both repos.

## Conventions

- Base URL: `{API_BASE_URL}/api/v1`. UTF-8 JSON, `snake_case` fields.
- `id` = UUID string (v4, from Postgres `gen_random_uuid()`). Times = ISO-8601 UTC (`2026-11-04T06:30:00Z`).
- Public URLs are built from the server env `PUBLIC_APP_URL` (default `https://talentscout.vercel.app`): `apply_url` = `${PUBLIC_APP_URL}/apply/{jobId}`, session link in email = `${PUBLIC_APP_URL}/s/{token}`. The API path (`/sessions/{token}`) differs from the page path (`/s/{token}`).
- **Callers.** The client is a server-side SvelteKit app (BFF, see client `docs/SERVER-SIDE.md`). All REST calls come from the SvelteKit server, never from the browser. The only browser → API calls are the two SSE streams.
- Recruiter auth: header `Authorization: Bearer <access_token>`, sent only by the SvelteKit server. The browser never sees `access_token`. For the run SSE stream the browser uses a short-lived `sse_token` in the query instead (see `POST /auth/sse-token`).
- Client IP forwarding: the SvelteKit server sends `X-Forwarded-For: <client ip>` (from `event.getClientAddress()`) plus `X-BFF-Secret: <BFF_SHARED_SECRET>`. The API trusts `X-Forwarded-For` **only** when `X-BFF-Secret` matches (constant-time); otherwise it uses the TCP remote address. The trusted IP is what the apply rate limit (5/IP/hour) counts.
- Applicant auth: session `token` in the path (no header).
- List: `?limit=` (default 50, max 100) and `?cursor=`; response `{ "items": [...], "next_cursor": "..." | null }`.

### Error Format (all endpoints)

```json
{ "error": { "code": "validation_failed", "message": "email is invalid", "details": { "field": "email" } } }
```

| HTTP | `code` |
|---|---|
| 400 | `validation_failed`, `invalid_file` |
| 401 | `unauthorized` |
| 404 | `not_found`, `session_not_found` |
| 409 | `already_applied`, `session_already_started`, `invalid_state` |
| 410 | `session_expired`, `session_completed` |
| 413 | `file_too_large` |
| 429 | `rate_limited` |
| 500 / 502 | `internal_error`, `llm_unavailable` |
| 503 | `db_unavailable` |

## Endpoint Summary

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/health` | — | Health check (`?deep=1` checks the DB) |
| POST | `/auth/login` | — | Recruiter login |
| POST | `/auth/sse-token` | recruiter | Mint a short-lived token for one run SSE stream |
| GET | `/jobs` | recruiter | List jobs (MVP: contains 1 seeded job) |
| GET | `/jobs/{jobId}` | recruiter | Job detail |
| GET | `/jobs/{jobId}/public` | — | Job info for the apply page |
| POST | `/jobs/{jobId}/applications` | — | Apply (multipart) |
| GET | `/jobs/{jobId}/applications` | recruiter | List applicants per job |
| GET | `/applications/{applicationId}` | recruiter | Applicant detail + session + transcript + scores |
| GET | `/applications/{applicationId}/cv` | recruiter | Download CV (PDF) |
| POST | `/applications/{applicationId}/decision` | recruiter | Shortlist / reject |
| POST | `/applications/{applicationId}/resend-invite` | recruiter | New invite link (new token; `invited` sessions only); in `EMAIL_MODE=log` the link is returned for display in the dashboard |
| GET | `/sessions/{token}` | token | Session info for the applicant |
| POST | `/sessions/{token}/start` | token | Start the session (once) |
| GET | `/sessions/{token}/messages` | token | Chat history (resume) |
| POST | `/sessions/{token}/messages` | token | Send an answer |
| GET | `/sessions/{token}/events` | token | Chat SSE (see EVENTS.md) |
| GET | `/graph-runs/{runId}` | recruiter | Graph run snapshot |
| GET | `/graph-runs/{runId}/events` | `sse_token` (query) | Live graph SSE + replay |

**Post-MVP (not implemented in the 1-week build):** `POST /jobs` (create job) and `PATCH /jobs/{jobId}` (edit job). The MVP has only **one job**, created by the seed (`cmd/seed`, see "Job Seed" below).

## Shared Shapes

```json
// RubricCriterion
{ "key": "frontend_depth", "label": "Frontend depth", "description": "Svelte/React, state, performance", "weight": 2 }

// Job
{ "id": "7c9e6679-…", "title": "Senior Frontend Engineer", "description": "...", "rubric": [RubricCriterion],
  "status": "open", "apply_url": "https://talentscout.vercel.app/apply/7c9e6679-…",
  "application_count": 3, "created_at": "...", "updated_at": "..." }

// ApplicationSummary
{ "id": "7c9e6679-…", "job_id": "7c9e6679-…", "name": "Budi", "email": "budi@example.com",
  "status": "review", "session_status": "completed", "overall_score": 3.8, "created_at": "..." }

// Message
{ "id": "7c9e6679-…", "role": "assistant", "kind": "question", "topic_id": "7c9e6679-…", "content": "...", "created_at": "..." }

// Topic
{ "id": "7c9e6679-…", "position": 1, "title": "State management", "goal": "...", "rubric_keys": ["frontend_depth"],
  "status": "done", "follow_up_count": 1 }

// Score
{ "rubric_key": "frontend_depth", "score": 4, "rationale": "...", "status": "valid", "attempts": 2,
  "evidence": [ { "message_id": "7c9e6679-…", "quote": "I moved the state into a separate store" } ] }
```

Enum: `Application.status` = `applied|invited|in_session|scoring|review|shortlisted|rejected|failed`; `Session.status` = `invited|active|completed|expired|revoked`; `finish_reason` = `completed|time_limit|question_limit|abandoned`; `Score.status` = `valid|needs_manual` (when `needs_manual`, `score` = `null`).

## Endpoint Details

### `GET /health?deep=1` → 200
```json
{ "status": "ok", "version": "0.1.0", "llm_mode": "openrouter", "email_mode": "log", "db": "ok" }
```
`deep=1` runs a light query against the DB (the `db` field exists only with `deep=1`; `"error"` + HTTP 503 when the DB is unreachable, e.g. Supabase paused). Without `deep` the DB is not touched.

### `POST /auth/login`
Req `{ "password": "..." }` → 200 `{ "access_token": "...", "expires_at": "..." }` (valid for 12 hours) · 401 `unauthorized`.
The single password is compared with env `RECRUITER_PASSWORD` (constant-time). `access_token` = a stateless HMAC-signed token (`AUTH_TOKEN_SECRET`) containing `expires_at`; no refresh, logout, or token table. The SvelteKit server stores the token in an httpOnly cookie on its own domain (`ts_session`, `maxAge` from `expires_at`) and never returns it to browser JS; any 401 → the SvelteKit server clears the cookie and redirects to login. `access_token` is accepted **only** in the `Authorization` header (not in a query string).

### `POST /auth/sse-token`
Auth: recruiter Bearer. Req `{ "run_id": "7c9e6679-…" }` → 200 `{ "sse_token": "...", "expires_at": "..." }` · 401 `unauthorized` · 404 `not_found` (unknown run).
`sse_token` = stateless HMAC token (`AUTH_TOKEN_SECRET`) with claims `aud=sse`, `run_id`, `expires_at` (now + 60 s). It is accepted only as `?sse_token=` on `GET /graph-runs/{run_id}/events` for the same `run_id`, and only checked when the stream connects (an open stream is not cut when the token expires). To reconnect, mint a new token. The SvelteKit server mints it in a server load (and a small cookie-authenticated `+server.ts` endpoint for reconnects) and passes it to the browser.

### `GET /jobs` → 200 `{ "items": [Job], "next_cursor": null }`

### Job Seed (not an endpoint)
One demo job is created by `go run ./cmd/seed` from `config/seed-job.yaml` (idempotent: if the `id` already exists, it is not recreated). Validation run by the seed: title 3–120 chars; description 50–8000 chars; rubric 3–6 criteria, unique `key` `^[a-z][a-z0-9_]{1,39}$`, `weight` integer 1–3. `status` = `open`.

### `GET /jobs/{jobId}` → 200 `Job` · 404

### `GET /jobs/{jobId}/public` → 200
```json
{ "id": "7c9e6679-…", "title": "...", "description": "...", "status": "open" }
```
404 if not found; a `closed` job still returns 200 with `status: "closed"` (the UI refuses to apply).

### `POST /jobs/{jobId}/applications` (multipart/form-data)
Field: `name` (2–100), `email`, `cv` (`application/pdf` file, ≤ 2 MB).
→ 201
```json
{ "application_id": "7c9e6679-…", "status": "invited", "message": "Thanks! Your screening link will be sent to you shortly." }
```
Error: 400 `validation_failed`/`invalid_file` (not a PDF / text cannot be extracted), 413 `file_too_large`, 409 `already_applied`, 409 `invalid_state` (job closed), 429 `rate_limited` (max 5 applies/IP/hour).

### `GET /jobs/{jobId}/applications?status=` → 200 `{ "items": [ApplicationSummary], "next_cursor": null }`

### `GET /applications/{applicationId}` → 200
```json
{
  "application": ApplicationSummary,
  "job": { "id": "7c9e6679-…", "title": "...", "rubric": [RubricCriterion] },
  "cv_text": "...",
  "decision": { "value": "shortlist", "note": "...", "decided_at": "..." } ,
  "session": { "id": "7c9e6679-…", "status": "completed", "expires_at": "...", "started_at": "...", "ended_at": "...",
               "finish_reason": "completed", "question_count": 9 },
  "graph_run": { "id": "7c9e6679-…", "status": "succeeded", "cost_usd": 0.0041 },
  "topics": [Topic],
  "messages": [Message],
  "scores": [Score]
}
```
`decision` = `null` if not decided yet. `graph_run` = `null` if the session has not started. The raw token is never returned here (the DB stores only the hash).

### `GET /applications/{applicationId}/cv` → 200 `application/pdf`
The server fetches the file from Supabase Storage (private bucket) and streams it to the recruiter. No Storage URL/signed URL is sent to the client.

### `POST /applications/{applicationId}/decision`
Req `{ "decision": "shortlist" | "reject", "note": "optional, ≤ 1000" }` → 200 `ApplicationSummary` (status `shortlisted`/`rejected`). 409 `invalid_state` if the status is not `review` yet (may be changed again between `shortlisted`↔`rejected`).

### `POST /applications/{applicationId}/resend-invite`
→ 200 `{ "session_id": "7c9e6679-…", "expires_at": "...", "invite_url": "..." | null }`. The old `invited` session is revoked (`revoked`) and a new token is created. `invite_url` (`${PUBLIC_APP_URL}/s/{token}`) is filled when `EMAIL_MODE=log` (**MVP default**): the dashboard shows it via the "Get invite link" button for the recruiter to copy. With `EMAIL_MODE=resend` (optional) the link is sent by email and `invite_url` is always `null`. 409 `invalid_state` if the session is already `active`/`completed`. Because the DB stores only the hash, an old link cannot be shown again; every click creates a new link.

### `GET /sessions/{token}` → 200
```json
{ "status": "invited", "job_title": "Senior Frontend Engineer", "applicant_name": "Budi",
  "expires_at": "...", "started_at": null, "deadline_at": null,
  "max_minutes": 15, "topic_count": null, "current_topic": null }
```
`deadline_at` = `started_at + 15 minutes` (when `active`). `current_topic` = `{ "position": 2, "title": "..." }` when `active`.
Error: 404 `session_not_found`, 410 `session_expired`, 410 `session_completed`.

### `POST /sessions/{token}/start` → 200 (same shape as `GET /sessions/{token}`, status `active`)
The AI opening message is sent via SSE (`message.delta` → `message.completed`). 409 `session_already_started`.

### `GET /sessions/{token}/messages` → 200 `{ "items": [Message] }`
Only `assistant` and `applicant` messages (no `system`). For resuming after a refresh.

### `POST /sessions/{token}/messages`
Req `{ "content": "..." }` (1–2000 char) → 202
```json
{ "message": Message, "awaiting": "assistant" }
```
The AI reply arrives via SSE. 409 `invalid_state` if the AI has not finished replying (the applicant must wait for `message.completed`), 410 if the session is finished/time is up.

### `GET /sessions/{token}/events` → `text/event-stream` (see EVENTS.md §3)

### `GET /graph-runs/{runId}` → 200
```json
{
  "id": "7c9e6679-…", "session_id": "7c9e6679-…", "status": "running",
  "nodes": [ { "id": "planner", "type": "splitter", "status": "succeeded", "attempts": 1, "model": "deepseek/deepseek-v4.1-flash" } ],
  "edges": [ { "from": "planner", "to": "plan_check" } ],
  "last_seq": 42, "total_tokens_in": 12000, "total_tokens_out": 1800, "cost_usd": 0.0021
}
```
`nodes[].id` ∈ `planner|plan_check|interviewer|judge|guard|scorer|evidence_check|recruiter_gate`; `type` ∈ `splitter|worker|gate|code|human_gate`.

### `GET /graph-runs/{runId}/events?sse_token=...&since_seq=0` → `text/event-stream` (see EVENTS.md §4)
Missing/invalid/expired `sse_token`, wrong `aud`, or a `run_id` mismatch → 401 `unauthorized`. `?access_token=` is no longer accepted.

## CORS

- Only the SSE routes are called from the browser: `GET /sessions/{token}/events` and `GET /graph-runs/{runId}/events`. CORS headers are sent only on those routes, only for `GET`, only for origins listed in env `FRONTEND_ORIGIN` (comma-separated, no wildcard; e.g. production URL, `http://localhost:5173`). No `Access-Control-Allow-Credentials` (no cookies cross-origin).
- All other routes return no CORS headers (they are called server-to-server by SvelteKit).
- The API never logs query strings of SSE requests (`sse_token`) or the `X-BFF-Secret` header.
