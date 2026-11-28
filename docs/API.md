# Talent Scout — REST API Contract (v1)

> **This is a contract.** The file is identical in `talent-scout-server/docs/` and `talent-scout-client/docs/`. On Day 1 the backend agent derives `openapi.yaml` (OpenAPI 3.1) from this document; the client generates TS types from `openapi.yaml`. A contract change = change this file first in both repos.

## Conventions

- Base URL: `{API_BASE_URL}/api/v1`. UTF-8 JSON, `snake_case` fields.
- `id` = UUID string (v4, from Postgres `gen_random_uuid()`). Times = ISO-8601 UTC (`2026-11-04T06:30:00Z`).
- Public URLs are built from the server env `PUBLIC_APP_URL` (default `https://talentscout-id.vercel.app`): `apply_url` = `${PUBLIC_APP_URL}/apply/{jobId}`, session link in email = `${PUBLIC_APP_URL}/s/{token}`. The API path (`/sessions/{token}`) differs from the page path (`/s/{token}`).
- **Callers.** The client is a server-side SvelteKit app (BFF, see client `docs/SERVER-SIDE.md`). All REST calls come from the SvelteKit server, never from the browser. The only browser → API calls are the two SSE streams.
- Recruiter auth: header `Authorization: Bearer <access_token>`, sent only by the SvelteKit server. The browser never sees `access_token`. For the run SSE stream the browser uses a short-lived `sse_token` in the query instead (see `POST /auth/sse-token`).
- Client IP forwarding: the SvelteKit server sends `X-Forwarded-For: <client ip>` (from `event.getClientAddress()`) plus `X-BFF-Secret: <BFF_SHARED_SECRET>`. The API trusts `X-Forwarded-For` **only** when `X-BFF-Secret` matches (constant-time); otherwise it uses the TCP remote address. The trusted IP is what the apply rate limit (5/IP/hour) and the recruiter login limit (5 failed attempts/IP/15 minutes) count.
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
| 409 | `already_applied`, `session_already_started`, `invalid_state`, `audio_budget_exceeded` |
| 410 | `session_expired`, `session_completed`, `recording_deleted` |
| 413 | `file_too_large` |
| 422 | `no_speech` |
| 429 | `rate_limited` |
| 500 / 502 | `internal_error`, `llm_unavailable`, `transcription_unavailable` |
| 503 | `db_unavailable` |

## Endpoint Summary

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/health` | — | Health check (`?deep=1` checks the DB) |
| POST | `/auth/login` | — | Recruiter login |
| POST | `/auth/sse-token` | recruiter | Mint a short-lived token for one run SSE stream |
| GET | `/jobs` | recruiter | List jobs (newest first) |
| POST | `/jobs` | recruiter | Create a job |
| PATCH | `/jobs/{jobId}` | recruiter | Edit a job |
| POST | `/jobs/rubric-suggestion` | recruiter | Suggest a draft rubric |
| GET | `/jobs/{jobId}` | recruiter | Job detail |
| GET | `/jobs/{jobId}/public` | — | Job info for the apply page |
| POST | `/jobs/{jobId}/applications` | — | Apply (multipart) |
| GET | `/jobs/{jobId}/applications` | recruiter | List applicants per job |
| GET | `/applications/{applicationId}` | recruiter | Applicant detail + session + transcript + scores + authenticity + fluency |
| GET | `/applications/{applicationId}/cv` | recruiter | Download CV (PDF) |
| GET | `/applications/{applicationId}/messages/{messageId}/audio` | recruiter | Stream a spoken answer recording |
| POST | `/applications/{applicationId}/decision` | recruiter | Shortlist / reject |
| POST | `/applications/{applicationId}/resend-invite` | recruiter | New invite link (new token; `invited` sessions or `failed` applications); in `EMAIL_MODE=log` the link is returned for display in the dashboard |
| GET | `/sessions/{token}` | token | Session info for the applicant |
| POST | `/sessions/{token}/start` | token | Start the session (once) |
| POST | `/sessions/{token}/audio` | token | Upload a spoken answer recording (voice sessions) |
| GET | `/sessions/{token}/messages` | token | Chat history (resume) |
| POST | `/sessions/{token}/messages` | token | Send an answer (typed or spoken) |
| POST | `/sessions/{token}/retry` | token | Re-run the failed graph step |
| GET | `/sessions/{token}/events` | token | Chat SSE (see EVENTS.md) |
| GET | `/graph-runs/{runId}` | recruiter | Graph run snapshot |
| GET | `/graph-runs/{runId}/events` | `sse_token` (query) | Live graph SSE + replay |

**Job validation** (shared by the seed, create and edit):

- `title`: 3–120 characters.
- `description`: markdown, 50–12000 characters.
- `rubric`: 3–6 criteria, unique `key` matching `^[a-z][a-z0-9_]{1,39}$`, `label` 2–60, `description` 10–300, `weight` integer 1–3.
- `status`: `open` or `closed`.
- `spoken_english_required`: boolean, default `false`. It is a job setting, not a rubric criterion.

Validation errors use 400 `validation_failed` with `details.field`, for example `rubric[2].weight`.

## Shared Shapes

```json
// RubricCriterion
{ "key": "frontend_depth", "label": "Frontend depth", "description": "Svelte/React, state, performance", "weight": 2 }

// Job
{ "id": "7c9e6679-…", "title": "Senior Frontend Engineer", "description": "...", "rubric": [RubricCriterion],
  "status": "open", "spoken_english_required": false,
  "apply_url": "https://talentscout-id.vercel.app/apply/7c9e6679-…",
  "application_count": 3, "created_at": "...", "updated_at": "..." }

// ApplicationSummary
{ "id": "7c9e6679-…", "job_id": "7c9e6679-…", "name": "Budi", "email": "budi@example.com",
  "status": "review", "session_status": "completed", "overall_score": 3.8, "created_at": "..." }

// Message
{ "id": "7c9e6679-…", "role": "assistant", "kind": "question", "topic_id": "7c9e6679-…", "content": "...", "created_at": "..." }

// MessageAudio
{ "duration_ms": 18000, "language": "en", "status": "available" | "deleted" | "not_stored" }

// MessageEntry
{ "pasted_chars": 120, "typed_chars": 300, "duration_ms": 4500,
  "mode": "typed" | "spoken",
  "fallback_reason": "chose_text" | "declined_recording" | "microphone_unavailable" | "transcription_failed",
  "start_latency_ms": 900, "speaking_ms": 17500, "rerecord_count": 0 }

// Authenticity
{ "level": "none" | "possible" | "likely", "reasons": ["..."], "assessed_at": "..." }

// VoiceSession
{ "max_answer_seconds": 120, "max_total_seconds": 1200, "used_seconds": 0, "recording_consent": null | boolean }

// Fluency
{ "words_per_minute": 132.4, "pause_ratio": 0.18, "filler_rate": 0.03, "transcript_confidence": 0.94,
  "languages": ["en"], "summary": "...", "spoken_answers": 6, "typed_answers": 1, "assessed_at": "..." }

// Topic
{ "id": "7c9e6679-…", "position": 1, "title": "State management", "goal": "...", "rubric_keys": ["frontend_depth"],
  "status": "done", "follow_up_count": 1 }

// Score
{ "rubric_key": "frontend_depth", "score": 4, "rationale": "...", "status": "valid", "attempts": 2,
  "evidence": [ { "message_id": "7c9e6679-…", "quote": "I moved the state into a separate store" } ] }
```

Enum: `Application.status` = `applied|invited|in_session|scoring|review|shortlisted|rejected|failed`; `Session.status` = `invited|active|completed|expired|revoked`; `finish_reason` = `completed|time_limit|question_limit|abandoned|failed`; `Score.status` = `valid|needs_manual` (when `needs_manual`, `score` = `null`).

`ApplicationSummary.overall_score` is the weighted average of the criteria scores (`score × rubric weight`), counting only `Score.status = valid`; `needs_manual` criteria are excluded. It is `null` when no criterion has a valid score.

`Message.entry` holds the applicant client's signals for one answer. It is returned only in the recruiter's `GET /applications/{applicationId}`: `null` for interviewer messages and when the applicant sent no entry. `GET /sessions/{token}/messages` never returns it. `mode` and `fallback_reason` describe how an answer was given; in a voice session a typed answer always carries a `fallback_reason`. `pasted_chars`, `typed_chars`, `duration_ms`, `start_latency_ms`, `speaking_ms` and `rerecord_count` are integers 0–10,000,000. The new fields are optional, so existing clients keep working unchanged.

`Message.audio` is returned only in the recruiter's `GET /applications/{applicationId}`: `null` for typed answers and interviewer messages. `VoiceSession` is `null` for a text session. `Fluency` is advisory (ADR 0001): it is not an input to the scorer or `overall_score` and never rejects an applicant. It is `null` for a text session, when the run has not finished, or when the check failed.

## Endpoint Details

### `GET /health?deep=1` → 200
```json
{ "status": "ok", "version": "0.1.0", "llm_mode": "openrouter", "email_mode": "log", "db": "ok" }
```
`deep=1` runs a light query against the DB (the `db` field exists only with `deep=1`; `"error"` + HTTP 503 when the DB is unreachable, e.g. Supabase paused). Without `deep` the DB is not touched.

### `POST /auth/login`
Req `{ "password": "..." }` → 200 `{ "access_token": "...", "expires_at": "..." }` (valid for 12 hours) · 401 `unauthorized` · 429 `rate_limited` (max 5 failed attempts/IP/15 minutes; `Retry-After` in seconds). Successful logins do not count.
The single password is compared with env `RECRUITER_PASSWORD` (constant-time). `access_token` = a stateless HMAC-signed token (`AUTH_TOKEN_SECRET`) containing `expires_at`; no refresh, logout, or token table. The SvelteKit server stores the token in an httpOnly cookie on its own domain (`ts_session`, `maxAge` from `expires_at`) and never returns it to browser JS; any 401 → the SvelteKit server clears the cookie and redirects to login. `access_token` is accepted **only** in the `Authorization` header (not in a query string).

### `POST /auth/sse-token`
Auth: recruiter Bearer. Req `{ "run_id": "7c9e6679-…" }` → 200 `{ "sse_token": "...", "expires_at": "..." }` · 401 `unauthorized` · 404 `not_found` (unknown run).
`sse_token` = stateless HMAC token (`AUTH_TOKEN_SECRET`) with claims `aud=sse`, `run_id`, `expires_at` (now + 60 s). It is accepted only as `?sse_token=` on `GET /graph-runs/{run_id}/events` for the same `run_id`, and only checked when the stream connects (an open stream is not cut when the token expires). To reconnect, mint a new token. The SvelteKit server mints it in a server load (and a small cookie-authenticated `+server.ts` endpoint for reconnects) and passes it to the browser.

### `GET /jobs` → 200 `{ "items": [Job], "next_cursor": null }`
All jobs, newest first.

### `POST /jobs` → 201 `Job`
Auth: recruiter Bearer. Req `{ "title", "description", "rubric": [RubricCriterion], "status"?, "spoken_english_required"? }`. `status` defaults to `open`; `spoken_english_required` defaults to `false`. Validated with the job rules above. 400 `validation_failed` with `details.field`. 409 `invalid_state` when 20 jobs already exist (counting open and closed jobs and the seed).

### `PATCH /jobs/{jobId}` → 200 `Job`
Auth: recruiter Bearer. Req: any subset of `title`, `description`, `rubric`, `status`, `spoken_english_required`. 400 `validation_failed` with `details.field`; 404 `not_found`. `rubric` can't change once the job has any application: 409 `invalid_state`, because existing scores reference its keys. Title, description, status and `spoken_english_required` stay editable. Closing a job doesn't touch running sessions; new applies get the existing 409 `invalid_state` (job closed). Editing `spoken_english_required` only affects sessions created after the edit: a session's mode is fixed when it is created (on apply or `resend-invite`).

### `POST /jobs/rubric-suggestion` → 200 `{ "rubric": [RubricCriterion], "spoken_english_required": boolean, "spoken_english_reason": string }`
Auth: recruiter Bearer. Req `{ "title", "description" }` with the same title and description limits as a job. The server validates the suggestion with the rubric rules above and also suggests whether the job requires spoken English, with a short reason. This is a suggestion only: the recruiter sets the job field, and nothing is stored. It retries the model once on invalid output, then returns 502 `llm_unavailable`. 429 `rate_limited` when the 10-per-hour budget is spent (the limit is global, not per client IP, because the shared password carries no recruiter identity); `Retry-After` in seconds. A provider error (network or upstream failure) refunds the slot; invalid output after the retry does not, so it counts against the budget. The title and description go to the model as untrusted data blocks.

### Job Seed (not an endpoint)
One demo job is created by `go run ./cmd/seed` from `config/seed-job.yaml` (idempotent: if the `id` already exists, it is not recreated). The seed uses the same validation as create/edit (see "Job validation" above) and sets `status` = `open`.

### `GET /jobs/{jobId}` → 200 `Job` · 404

### `GET /jobs/{jobId}/public` → 200
```json
{ "id": "7c9e6679-…", "title": "...", "description": "...", "status": "open", "spoken_english_required": false }
```
404 if not found; a `closed` job still returns 200 with `status: "closed"` (the UI refuses to apply). The apply page uses `spoken_english_required` to say that the interview is spoken.

### `POST /jobs/{jobId}/applications` (multipart/form-data)
Field: `name` (2–100), `email`, `cv` (`application/pdf` file, ≤ 2 MB).
→ 201
```json
{ "application_id": "7c9e6679-…", "status": "invited", "message": "..." }
```
`message` is mode-aware: with `EMAIL_MODE=log` it is `"Thanks! Your application was received. The recruiter will send your screening link."`; with `EMAIL_MODE=gmail` it is `"Thanks! Your screening link will be sent to you shortly."`. A Gmail send failure is logged (redacted) and still returns 201; the recruiter can re-issue the link with `resend-invite`.
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
  "scores": [Score],
  "authenticity": Authenticity | null,
  "fluency": Fluency | null
}
```
`decision` = `null` if not decided yet. `graph_run` = `null` if the session has not started. `authenticity` is the advisory answer-authenticity assessment, or `null` when the run has not finished or the check failed. `fluency` is the advisory spoken-English assessment, or `null` for a text session, when the run has not finished, or when the check failed. Neither rejects an applicant or changes a score (ADR 0001). `graph_run.cost_usd` includes the transcriptions. The raw token is never returned here (the DB stores only the hash).

### `GET /applications/{applicationId}/cv` → 200 `application/pdf`
The server fetches the file from Supabase Storage (private bucket) and streams it to the recruiter. No Storage URL/signed URL is sent to the client.

### `GET /applications/{applicationId}/messages/{messageId}/audio` → 200 `audio/webm` | `audio/ogg` | `audio/mp4`
The server fetches the spoken answer recording from the private `audio` bucket and streams it to the recruiter. No Storage URL/signed URL is sent to the client; the response `Content-Type` is the uploaded type. A `Range` request returns `206 Partial Content` with `Content-Range` and `Accept-Ranges: bytes`, so browsers can seek. 404 `not_found` when the application or message is unknown, or the message is not a spoken answer. 410 `recording_deleted` when retention deleted the recording or it was not stored (`Message.audio.status` = `deleted` or `not_stored`).

### `POST /applications/{applicationId}/decision`
Req `{ "decision": "shortlist" | "reject", "note": "optional, ≤ 1000" }` → 200 `ApplicationSummary` (status `shortlisted`/`rejected`). 409 `invalid_state` if the status is not `review` yet (may be changed again between `shortlisted`↔`rejected`).

### `POST /applications/{applicationId}/resend-invite`
→ 200 `{ "session_id": "7c9e6679-…", "expires_at": "...", "invite_url": "..." | null }`. Allowed when the current session is `invited`, or when `application.status` is `failed`. For an `invited` session the old session is revoked (`revoked`) and a new token is created. For a `failed` application a new `invited` session is created and the application returns to `invited`; the old session keeps its terminal status. `invite_url` (`${PUBLIC_APP_URL}/s/{token}`) is filled when `EMAIL_MODE=log` (**MVP default**): the dashboard shows it via the "Get invite link" button for the recruiter to copy. With `EMAIL_MODE=gmail` (optional) the link is sent by email and `invite_url` is always `null`; a send failure is logged (redacted) and the endpoint can be called again. 409 `invalid_state` if the application is not `failed` and the current session is not `invited` (`active`/`completed`/`expired`/`revoked`). Because the DB stores only the hash, an old link cannot be shown again; every click creates a new link.

### `GET /sessions/{token}` → 200
```json
{ "status": "invited", "job_title": "Senior Frontend Engineer", "applicant_name": "Budi",
  "expires_at": "...", "started_at": null, "deadline_at": null,
  "max_minutes": 15, "topic_count": null,
  "question_count": 0, "max_questions": 15, "current_topic": null,
  "voice": VoiceSession | null }
```
`max_minutes` comes from env `SESSION_MAX_MINUTES` (default 15) for every session, and `deadline_at` = `started_at + SESSION_MAX_MINUTES` (when `active`). `current_topic` = `{ "position": 2, "title": "...", "total": 4 }` when `active`. `question_count`, `max_questions`, and `current_topic.total` are the same fields as `session.updated` (EVENTS.md §3). `voice` is `null` for a text session; for a voice session it holds the recording budget and consent state. The mode is fixed when the session was created: editing the job only affects sessions created after the edit.
Error: 404 `session_not_found`, 410 `session_expired`, 410 `session_completed` (including a session that ended with `finish_reason=failed`).

### `POST /sessions/{token}/start` → 200 (same shape as `GET /sessions/{token}`, status `active`)
Req `{ "recording_consent"?: boolean }`. For a voice session the field is required (400 `validation_failed` when missing) and stored in `voice.recording_consent`. With `false` the session continues as a voice session: the interviewer is still read aloud, but every answer is a text fallback with `fallback_reason: "declined_recording"`. For a text session the field is ignored.
The AI opening message is sent via SSE (`message.delta` → `message.completed`). The client opens `GET /sessions/{token}/events` **before** calling `start` so no deltas are lost (the stream accepts `invited` sessions). 409 `session_already_started`, 410 `session_expired`/`session_completed`.

### `POST /sessions/{token}/audio` (multipart/form-data) → 200
Field: `audio` (≤ 1 MB; `audio/webm`, `audio/ogg` or `audio/mp4`).
```json
{ "audio_id": "7c9e6679-…", "transcript": "...", "language": "en", "duration_ms": 18000, "stored": true }
```
The server transcribes the audio (language auto-detected) and stores the recording in the private `audio` bucket. This endpoint does not send an answer: send the returned `audio_id` with `POST /sessions/{token}/messages`. A re-record is a new upload.
Errors: 409 `invalid_state` when the session is not a voice session, the recording consent is not `true`, or the AI is still replying; 400 `validation_failed` when the audio is empty or longer than `voice.max_answer_seconds`; 409 `audio_budget_exceeded` when `voice.used_seconds` would exceed `voice.max_total_seconds`; 413 `file_too_large` over 1 MB; 422 `no_speech` when the transcript is empty; 502 `transcription_unavailable` (retryable; the client re-sends the same recording); 410 when the session is finished; 429 `rate_limited` (60 uploads per session per 10 minutes).
A storage failure is not an error: `stored` is `false` and the answer can still be sent. Unsent uploads are deleted 24 hours after upload, and sent recordings 30 days after upload, by the retention sweeper.

### `GET /sessions/{token}/messages` → 200 `{ "items": [Message] }`
Only `assistant` and `applicant` messages (no `system`). For resuming after a refresh. The applicant's `entry` signals are never returned here.

### `POST /sessions/{token}/messages`
The request takes exactly one of:
- `{ "content": "...", "entry"?: MessageEntry }` — a typed answer (`content` 1–2000 char; each `entry` counter is an integer from 0 to 10,000,000). In a voice session `entry.fallback_reason` is required (400 `validation_failed` when missing).
- `{ "audio_id": "7c9e6679-…", "entry"?: MessageEntry }` — a spoken answer. The answer text is the server's stored transcript for that upload; the client never sends it. 400 `validation_failed` when the upload is unknown, from another session, or already sent.

Both, or neither, is 400 `validation_failed`. → 202
```json
{ "message": Message, "awaiting": "assistant" }
```
`entry` is optional so older clients keep working; a malformed `entry` is 400 `validation_failed` with `details.field`. It never changes the interview flow: it is stored with the applicant message and only reported to the recruiter. The AI reply arrives via SSE. 409 `invalid_state` if the AI has not finished replying (the applicant must wait for `message.completed`) or if the session is waiting for `POST /sessions/{token}/retry`; 410 if the session is finished/time is up; 429 `rate_limited` (max 20 messages/session/10 minutes; `Retry-After` in seconds).

### `POST /sessions/{token}/retry` → 202
```json
{ "status": "retrying" }
```
Re-runs the graph step that failed for this session (the step that emitted the last retryable `error`). The result is streamed on the session SSE like the original step; input stays locked until `message.completed` or `session.ended`. The same loop budgets apply (`docs/ARCHITECTURE.md` §3), and each retry emits `loop.retry` on the run stream. 409 `invalid_state` if no step is waiting for a retry; 410 `session_expired`/`session_completed`. If the step fails permanently (attempt budget exhausted or a non-retryable error), the server sends `session.ended` with `finish_reason: "failed"`, sets the application to `failed`, and the recruiter can issue a new invite via `resend-invite`.

### `GET /sessions/{token}/events` → `text/event-stream` (see EVENTS.md §3)
Accepts sessions in `invited` and `active` status. The client opens it before `POST /start`. After a reconnect the replay contains only `session.*`, `message.completed`, and `error`; `message.delta` is never persisted or replayed (rebuild the transcript with `GET /messages`).

### `GET /graph-runs/{runId}` → 200
```json
{
  "id": "7c9e6679-…", "session_id": "7c9e6679-…", "status": "running",
  "nodes": [ { "id": "planner", "type": "splitter", "status": "succeeded", "attempts": 1, "model": "deepseek/deepseek-v4.1-flash" } ],
  "edges": [ { "from": "planner", "to": "plan_check" } ],
  "last_seq": 42, "total_tokens_in": 12000, "total_tokens_out": 1800, "cost_usd": 0.0021
}
```
`nodes[].id` ∈ `planner|plan_check|interviewer|judge|guard|scorer|evidence_check|authenticity|fluency|recruiter_gate`; `type` ∈ `splitter|worker|gate|code|human_gate`; `status` ∈ `pending|running|succeeded|failed|skipped`. `cost_usd` includes transcription cost.

### `GET /graph-runs/{runId}/events?sse_token=...&since_seq=0` → `text/event-stream` (see EVENTS.md §4)
Missing/invalid/expired `sse_token`, wrong `aud`, or a `run_id` mismatch → 401 `unauthorized`. `?access_token=` is no longer accepted.

## CORS

- Only the SSE routes are called from the browser: `GET /sessions/{token}/events` and `GET /graph-runs/{runId}/events`. CORS headers are sent only on those routes, only for `GET`, only for origins listed in env `FRONTEND_ORIGIN` (comma-separated, no wildcard; e.g. production URL, `http://localhost:5173`). No `Access-Control-Allow-Credentials` (no cookies cross-origin).
- All other routes return no CORS headers (they are called server-to-server by SvelteKit).
- The API never logs query strings of SSE requests (`sse_token`) or the `X-BFF-Secret` header.
