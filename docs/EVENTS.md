# Talent Scout — SSE Event Contract (v1)

> **This is a contract.** Identical in both repos. Event names & shapes must not change without updating this file in both repos.

## 1. Stream

| Stream | Endpoint | For | Contents |
|---|---|---|---|
| Session stream | `GET /api/v1/sessions/{token}/events` | Applicant (chat) | `session.*`, `message.*`, `error` |
| Run stream | `GET /api/v1/graph-runs/{runId}/events?sse_token=&since_seq=` | Recruiter (live graph) | `run.*`, `node.*`, `check.*`, `loop.retry`, `edge.traversed`, `topic.changed`, `message.completed` |

## 2. Wire Format

```
id: 42
event: node.completed
data: {"run_id":"7c9e6679-…","seq":42,"ts":"2026-11-04T06:30:01Z","node_id":"judge","attempt":1,"data":{...}}

```

- `id` = `seq` (monotonically increasing integer per run; the session stream uses the same run seq). The client sends `Last-Event-ID` on reconnect → the server replays events with `seq > Last-Event-ID` from `node_events`, then continues live. The `since_seq` query = manual alternative.
- Heartbeat: a `: ping` comment line every 15 seconds (not an event).
- The `data` envelope is always: `run_id`, `seq`, `ts`, `node_id` (nullable), `attempt` (nullable), `data` (event-specific payload).
- Response headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`.
- The server closes the stream after `session.ended` (session stream) or `run.completed`/`run.failed` (run stream).

## 3. Session Stream Events (applicant)

| Event | Payload `data` | When |
|---|---|---|
| `session.updated` | `{ "status": "active", "deadline_at": "...", "question_count": 3, "max_questions": 15, "current_topic": { "position": 2, "title": "...", "total": 4 } }` | After start, on topic change, after each answer |
| `message.delta` | `{ "message_id": "7c9e6679-…", "delta": "text chunk" }` | Streaming tokens of the Interviewer reply |
| `message.completed` | `{ "message": Message }` (shape from API.md) | AI reply finished → applicant input may be enabled again |
| `session.ended` | `{ "finish_reason": "completed" \| "time_limit" \| "question_limit" \| "abandoned" }` | Session finished; show the thank-you screen |
| `error` | `{ "code": "llm_unavailable", "message": "...", "retryable": true }` | Temporary failure; UI shows "try again" |

The session stream does **not** send node/score events (applicants never see the assessment).

## 4. Run Stream Events (recruiter)

**Auth & reconnect.** The browser connects directly to the API with a short-lived `sse_token` (60 s, `aud=sse`, bound to `run_id`; minted via `POST /auth/sse-token` by the SvelteKit server, see API.md). The token is checked only at connect time. Because a reconnect with the old URL fails once the token has expired (401 ends the `EventSource`), the client handles reconnects itself: on `error` → close the `EventSource` → fetch a fresh `sse_token` from its own SvelteKit endpoint → reopen with `since_seq=<last seq received>`. CORS: `GET` only, origins from `FRONTEND_ORIGIN`, no credentials.

| Event | `node_id` | Payload `data` |
|---|---|---|
| `run.started` | null | `{ "session_id": "7c9e6679-…", "nodes": [ {id,type} ], "edges": [ {from,to} ] }` |
| `node.started` | node | `{ "model": "z-ai/glm-5.3-flash" \| null, "input_summary": "topic 2: State management" }` |
| `node.completed` | node | `{ "output_summary": "verdict=follow_up", "tokens_in": 820, "tokens_out": 64, "duration_ms": 1430, "cost_usd": 0.00006 }` |
| `node.failed` | node | `{ "error": "timeout", "will_retry": true }` (`will_retry` is `true` only for the 1× HTTP retry on the same model; no model fallback in the MVP) |
| `check.passed` | code node (`plan_check`/`guard`/`evidence_check`) or the checked node | `{ "check": "evidence_quotes", "details": {} }` |
| `check.failed` | code node | `{ "check": "evidence_quotes", "errors": [ { "rubric_key": "frontend_depth", "reason": "quote_not_found", "quote": "..." } ] }` |
| `loop.retry` | the retried node (`planner` max 1 / `scorer` max 3) | `{ "attempt": 2, "max_attempts": 3, "reason": "quote_not_found", "feedback": "..." }` |
| `edge.traversed` | null | `{ "from": "guard", "to": "interviewer", "label": "follow_up" }` |
| `topic.changed` | `guard` | `{ "topic_id": "7c9e6679-…", "position": 3, "title": "...", "status": "active" }` |
| `message.completed` | `interviewer` | `{ "message": Message }` (also for applicant answers, `role=applicant`) |
| `run.completed` | null | `{ "status": "succeeded", "finish_reason": "completed", "total_tokens_in": 52000, "total_tokens_out": 7400, "cost_usd": 0.0047, "needs_manual_count": 0 }` |
| `run.failed` | null | `{ "error": "planner_invalid_after_retries" }` |

`label` on `edge.traversed` ∈ `valid|invalid|question|answer|follow_up|next_topic|finish|retry|scored`.

## 5. Event Name List (source of truth for TS types)

```
session.updated  message.delta  message.completed  session.ended  error
run.started  run.completed  run.failed
node.started  node.completed  node.failed
check.passed  check.failed  loop.retry
edge.traversed  topic.changed
```

## 6. Example Sequence (1 topic with 1 follow-up, then scoring with 1 retry)

```
run.started → node.started(planner) → node.completed(planner) → edge.traversed(planner→plan_check)
→ check.passed(plan_check) → topic.changed(1) → node.started(interviewer) → message.completed(assistant)
→ message.completed(applicant) → node.started(judge) → node.completed(judge, follow_up)
→ node.completed(guard) → edge.traversed(guard→interviewer, follow_up) → ...
→ edge.traversed(guard→scorer, finish) → node.started(scorer) → node.completed(scorer)
→ check.failed(evidence_check) → loop.retry(scorer, attempt 2) → node.completed(scorer)
→ check.passed(evidence_check) → edge.traversed(evidence_check→recruiter_gate, scored) → run.completed
```

The client mock (Day 1) must be able to replay this sequence from a fixture file.
