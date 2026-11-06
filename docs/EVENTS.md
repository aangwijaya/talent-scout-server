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
- **Session replay filter.** Replaying `GET /sessions/{token}/events` returns only `session.*`, `message.completed`, and `error`. `message.delta` is never persisted and never replayed; after a reconnect the client rebuilds the transcript from `GET /api/v1/sessions/{token}/messages`. The run stream replays all run events.
- **Final text.** `message.completed` is authoritative: the client always renders `message.content`, replacing any accumulated `message.delta` chunks. The server stops streaming at the 120-word limit, so the streamed text and the stored text are identical.
- Heartbeat: a `: ping` comment line every 15 seconds (not an event).
- The `data` envelope is always: `run_id`, `seq`, `ts`, `node_id` (nullable), `attempt` (nullable, 1-based), `data` (event-specific payload).
- Response headers: `Content-Type: text/event-stream`, `Cache-Control: no-cache`, `X-Accel-Buffering: no`.
- The server closes the stream after `session.ended` (session stream) or `run.completed`/`run.failed` (run stream).

## 3. Session Stream Events (applicant)

The stream can be opened for a session in `invited` status. The client opens it **before** calling `POST /sessions/{token}/start`; the opening-message events arrive on the already-open stream.

| Event | Payload `data` | When |
|---|---|---|
| `session.updated` | `{ "status": "active", "deadline_at": "...", "question_count": 3, "max_questions": 15, "current_topic": { "position": 2, "title": "...", "total": 4 } }` | After start, on topic change, after each answer |
| `message.delta` | `{ "message_id": "7c9e6679-…", "delta": "text chunk" }` | Streaming tokens of the Interviewer reply; the server stops at the 120-word limit |
| `message.completed` | `{ "message": Message }` (shape from API.md) | AI reply finished → applicant input may be enabled again; `message.content` is the final text and is always rendered |
| `session.ended` | `{ "finish_reason": "completed" \| "time_limit" \| "question_limit" \| "abandoned" \| "failed" }` | Session finished; show the thank-you screen. `failed` = permanent graph failure: the application becomes `failed` and the recruiter can issue a new invite |
| `error` | `{ "code": "llm_unavailable", "message": "...", "retryable": true }` | Temporary failure; the UI shows "try again" and calls `POST /sessions/{token}/retry`. If the retry budget is exhausted, the server ends the session with `finish_reason: "failed"` |

The session stream does **not** send node/score events (applicants never see the assessment).

## 4. Run Stream Events (recruiter)

**Auth & reconnect.** The browser connects directly to the API with a short-lived `sse_token` (60 s, `aud=sse`, bound to `run_id`; minted via `POST /auth/sse-token` by the SvelteKit server, see API.md). The token is checked only at connect time. Because a reconnect with the old URL fails once the token has expired (401 ends the `EventSource`), the client handles reconnects itself: on `error` → close the `EventSource` → fetch a fresh `sse_token` from its own SvelteKit endpoint → reopen with `since_seq=<last seq received>`. CORS: `GET` only, origins from `FRONTEND_ORIGIN`, no credentials.

| Event | `node_id` | Payload `data` |
|---|---|---|
| `run.started` | null | `{ "session_id": "7c9e6679-…", "nodes": [ {id,type} ], "edges": [ {from,to} ] }` |
| `node.started` | node | `{ "model": "z-ai/glm-5.3-flash" \| null, "input_summary": "topic 2: State management" }` |
| `node.completed` | node | `{ "output_summary": "verdict=follow_up", "tokens_in": 820, "tokens_out": 64, "duration_ms": 1430, "cost_usd": 0.00006 }` |
| `node.failed` | node | `{ "error": "timeout", "will_retry": true }` (`will_retry` is `true` only for the 1× HTTP retry on the same model; no model fallback in the MVP) |
| `node.skipped` | node | `{ "reason": "run_failed" }` (the node was not executed because the run ended first) |
| `check.passed` | code node (`plan_check`/`guard`/`evidence_check`) or the checked node | `{ "check": "evidence_quotes", "details": {} }` |
| `check.failed` | code node | `{ "check": "evidence_quotes", "errors": [ { "rubric_key": "frontend_depth", "reason": "quote_not_found", "quote": "..." } ] }` |
| `loop.retry` | the retried node (`planner`, `max_attempts` 2 / `scorer`, `max_attempts` 3) | `{ "attempt": 2, "max_attempts": 3, "reason": "quote_not_found", "feedback": "..." }` |
| `edge.traversed` | null | `{ "from": "guard", "to": "interviewer", "label": "follow_up" }` |
| `topic.changed` | `plan_check` (first topic) / `guard` (next topic) | `{ "topic_id": "7c9e6679-…", "position": 3, "title": "...", "status": "active" }` |
| `message.completed` | `interviewer` | `{ "message": Message }` (also for applicant answers, `role=applicant`) |
| `run.completed` | null | `{ "status": "succeeded", "finish_reason": "completed", "total_tokens_in": 52000, "total_tokens_out": 7400, "cost_usd": 0.0047, "needs_manual_count": 0 }` |
| `run.failed` | null | `{ "error": "planner_invalid_after_retries" }` |

Every attempt of a node emits `node.started` and then exactly one terminal event: `node.completed` or `node.failed`; a node that is not executed emits `node.skipped` instead. Every graph transition emits `edge.traversed`. `check.passed`/`check.failed` may follow a code node's `node.completed`. `recruiter_gate` is a human gate outside the runner and does not emit node events.

`label` on `edge.traversed` ∈ `planned|invalid|valid|question|answer|verdict|follow_up|next_topic|finish|retry|scored`.

## 5. Event Name List (source of truth for TS types)

```
session.updated  message.delta  message.completed  session.ended  error
run.started  run.completed  run.failed
node.started  node.completed  node.failed  node.skipped
check.passed  check.failed  loop.retry
edge.traversed  topic.changed
```

## 6. Normative Event Sequence

This section is the source of truth for the client fixtures. `node.started`/`node.completed`/`node.failed`/`node.skipped` and `edge.traversed` are mandatory for every machine node and transition; `seq` increases across all events. `attempt` is 1-based.

### 6.1 Successful run (1 topic with 1 follow-up, then scoring with 1 retry)

```
run.started
node.started(planner, attempt=1) → node.completed(planner, attempt=1)
edge.traversed(planner→plan_check, planned)
node.started(plan_check, attempt=1) → node.completed(plan_check, attempt=1, valid)
check.passed(plan_check)
topic.changed(plan_check, topic 1, active)
edge.traversed(plan_check→interviewer, valid)
node.started(interviewer, attempt=1) → message.completed(assistant, opening) → node.completed(interviewer, attempt=1)
message.completed(applicant, answer 1)
edge.traversed(interviewer→judge, answer)
node.started(judge, attempt=1) → node.completed(judge, attempt=1, follow_up)
edge.traversed(judge→guard, verdict)
node.started(guard, attempt=1) → node.completed(guard, attempt=1, follow_up)
edge.traversed(guard→interviewer, follow_up)
node.started(interviewer, attempt=1) → message.completed(assistant, follow_up) → node.completed(interviewer, attempt=1)
message.completed(applicant, answer 2)
edge.traversed(interviewer→judge, answer)
node.started(judge, attempt=1) → node.completed(judge, attempt=1, sufficient)
edge.traversed(judge→guard, verdict)
node.started(guard, attempt=1) → node.completed(guard, attempt=1, finish)
edge.traversed(guard→scorer, finish)
node.started(scorer, attempt=1) → node.completed(scorer, attempt=1)
edge.traversed(scorer→evidence_check, scored)
node.started(evidence_check, attempt=1) → node.completed(evidence_check, attempt=1, invalid)
check.failed(evidence_check)
loop.retry(scorer, attempt=2, max_attempts=3)
edge.traversed(evidence_check→scorer, retry)
node.started(scorer, attempt=2) → node.completed(scorer, attempt=2)
edge.traversed(scorer→evidence_check, scored)
node.started(evidence_check, attempt=2) → node.completed(evidence_check, attempt=2, valid)
check.passed(evidence_check)
edge.traversed(evidence_check→recruiter_gate, scored)
run.completed
```

When the guard activates the next topic, the order is `node.completed(guard, next_topic) → topic.changed(guard, topic N, active) → edge.traversed(guard→interviewer, next_topic)`.

### 6.2 Planner failure after both attempts

```
run.started
node.started(planner, attempt=1) → node.completed(planner, attempt=1)
edge.traversed(planner→plan_check, planned)
node.started(plan_check, attempt=1) → node.completed(plan_check, attempt=1, invalid)
check.failed(plan_check)
loop.retry(planner, attempt=2, max_attempts=2)
edge.traversed(plan_check→planner, invalid)
node.started(planner, attempt=2) → node.completed(planner, attempt=2)
edge.traversed(planner→plan_check, planned)
node.started(plan_check, attempt=2) → node.completed(plan_check, attempt=2, invalid)
check.failed(plan_check)
node.skipped(interviewer, reason=run_failed) → node.skipped(judge, reason=run_failed) → node.skipped(guard, reason=run_failed) → node.skipped(scorer, reason=run_failed) → node.skipped(evidence_check, reason=run_failed)
run.failed(error=planner_invalid_after_retries)
```

### 6.3 Transport retry and permanent failure

A 1× HTTP retry on the same model emits `node.failed(will_retry=true)` and then a new `node.started` for the same node; it does **not** emit `loop.retry`:

```
node.started(judge, attempt=1) → node.failed(judge, error=timeout, will_retry=true)
node.started(judge, attempt=2) → node.completed(judge, attempt=2)
```

If the transport retry also fails (`will_retry=false`) or a node cannot continue, the run emits `node.failed` (and `node.skipped` for downstream machine nodes) → `run.failed`, and the session stream sends `session.ended` with `finish_reason: "failed"`. The application becomes `failed`.

If `evidence_check` still fails on its final allowed attempt (`max_attempts=3`), no further `loop.retry` is emitted: the criterion becomes `needs_manual`, `evidence_check` completes, the transition to `recruiter_gate` is emitted, and `run.completed` reports `needs_manual_count > 0`.

The client mock (Day 1) must be able to replay these sequences from fixture files.
