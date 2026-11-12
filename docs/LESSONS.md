# LESSONS — talent-scout-server

Agent lessons log (learning loop). Add entries **at the top** (newest first) after finishing or blocking an issue, only when there is a real lesson: a surprise, a near-repeated mistake, a technical decision, or a command that behaved differently than expected.

Rules:
- Read this file before starting a new issue.
- The **same lesson appears 2×** → open a separate PR labeled `skill-update` that changes the skill in `.opencode/skills/` or the rules in `AGENTS.md`, then fill the "Follow-up" field of both entries with that PR number.
- Prefer turning a lesson into a **test, lint rule, or CI check** over text.
- Never write secrets, tokens, URLs containing credentials, or applicant personal data.
- A human merges `skill-update` PRs.

## Entry template

```markdown
### YYYY-MM-DD — #<issue> <short title>
- **Context:** what was being worked on
- **Problem:** what went wrong / was surprising
- **Cause:** root cause
- **Lesson:** reusable rule (1 sentence)
- **Follow-up:** none | test/CI check in PR #… | skill-update PR #…
- **Repeated?:** first time | same as entry YYYY-MM-DD
```

## Entries

### 2026-11-13 — #14 Reasoning tokens eat `max_tokens`; live prompt tuning and cost
- **Context:** Running the fake and live (OpenRouter) end-to-end flow from `internal/e2e` with the seed job and three made-up CVs, then tuning the planner/interviewer/judge/scorer prompts.
- **Problem:** The first live runs failed loudly on `finish_reason: "length"`: planner ended at exactly 800/800 completion tokens, judge at 200/200, and the interviewer at 500 and later 2000 tokens with no visible text (the reasoning trace consumed the whole budget). A happy-path session cost `$0.010162` (34,984 in / 12,566 out), double the `~$0.005` estimate in `docs/TECH-STACK.md` §8.
- **Cause:** `deepseek/deepseek-v4.1-flash` and `z-ai/glm-5.3-flash` emit reasoning tokens that count toward `completion_tokens`, so the caps tuned for visible JSON/text were far too low. The judge prompt was also strict enough that a substantive answer could still end at the 15-question limit.
- **Lesson:** Size `max_tokens` for reasoning plus output, not output alone: after the fix the observed completion counts were planner 683–1623, judge 164–742, scorer 723–1788, and interviewer 50–1827. Keep planner ≥3000, judge ≥1200 and scorer ≥4000, and set `reasoning: { effort: low }` on the streaming interviewer (it cut interviewer completion to 48–450 on the tuned runs). Recheck cost with real token counts — realistic live sessions here were `$0.006772`, `$0.008021` and `$0.012421`, so §8's `~$0.005` is low but the same order of magnitude. Tuning that worked: planner must cover every rubric key; judge treats a concrete, scoreable account as sufficient and only asks for missing parts; scorer must copy a contiguous verbatim phrase of at least 3 words and never paraphrase.
- **Follow-up:** `internal/e2e` fake/live harness, `internal/e2e/fixture_test.go` (replays the recorded `loop.retry` fixture), live-only `TestLiveEndToEnd` and `TestLiveEvidenceRetryFixture`
- **Repeated?:** first time

### 2026-11-12 — #60 Run SSE must not re-read status after the replay
- **Context:** Hardening the graph-run stream found while verifying server#13.
- **Problem:** The handler re-read the run status after replaying, but the runner emits `run.completed`/`run.failed` before `finishRun` persists the status, so the re-read could see `succeeded` and close the stream before live delivered the terminal event. Stream tests still passed when close-after-terminal was removed because they only checked for any error after the 3 s client timeout.
- **Cause:** An unnecessary post-replay status read raced the terminal event already buffered by the pre-replay subscription; the tests did not bound time-to-EOF.
- **Lesson:** Subscribe before replay and never re-read run status to decide when to close; assert `io.EOF` within about 500 ms after the terminal event, and reject a non-UUID path parameter before any store call (Postgres returns 22P02 → 500).
- **Follow-up:** tests in `internal/http/handlers/graph_test.go` and `internal/http/graph_runs_router_test.go`
- **Repeated?:** first time

### 2026-11-10 — #11 An external finish must serialize with the turn in flight
- **Context:** Review of the sweeper finish path: `finishExternally` emitted `session.ended` and launched scoring while a turn could still be running judge/guard/interviewer.
- **Problem:** The runner and `st.executed` are not concurrency safe, so scoring beside a live turn is a fatal concurrent-map-write, interleaves node events after `session.ended`, can emit `guard→scorer` twice, and can end the run as both `run.failed` and `run.completed`.
- **Cause:** The sweeper's registry check happens outside the manager, so it can win the finish race just after an applicant turn starts; `finishExternally` only cleared `st.busy` instead of coordinating with that turn.
- **Lesson:** Serialize every graph step and the external finish path on one per-session mutex (`runStep`); make the finish edge idempotent with a flag set where the edge is emitted; and when the sweeper already ended the session, make the in-flight turn abort quietly instead of calling `failRun`, so the external finish owns session.ended and scoring.
- **Follow-up:** tests in `internal/graph/session_review_test.go` (`TestExternalFinishWaitsForInFlightTurn`, `TestExternalFinishStopsLateTurnFailure`)
- **Repeated?:** first time

### 2026-11-10 — #11 A sweeper finish hook must not run the scorer inline
- **Context:** Review of the scorer: the sweeper's `NotifyFinished` callback ran `runScoring` synchronously, and that path skipped the `guard→scorer` edge.
- **Problem:** Up to 3 scorer calls plus transport retries blocked the single sweep loop, delaying every other `time_limit`/`abandoned` session; replay of a swept run also had `node.started(scorer)` with no preceding `edge.traversed`.
- **Cause:** The in-process and sweeper finish paths were written at different times and only the in-process path followed the EVENTS.md §6 sequence; "the scorer runs async" was implemented per call site instead of inside the shared finish hook.
- **Lesson:** Start scoring in a goroutine inside the sweeper finish hook after emitting `session.ended` (fetching the session inside the goroutine), emit the same `guard→scorer` edge as the in-process path before `node.started(scorer)`, and have a scoring failure emit `node.skipped(evidence_check, run_failed)` before `run.failed`.
- **Follow-up:** tests in `internal/graph/session_review_test.go` (`TestSweepScoresAsyncAndEmitsGuardEdge`, `TestScoringFailureSkipsEvidenceCheckBeforeRunFailed`)
- **Repeated?:** first time

### 2026-11-09 — #11 Retry only failed criteria; map internal evidence reasons to the contract
- **Context:** Adding the scorer and the evidence check retry loop (max 3 attempts) after the interview ends.
- **Problem:** `domain.EvidenceQuoteRejectReason` returns `quote_too_short` / `quote_too_long`, but `docs/EVENTS.md` only documents `quote_not_found` as a check/retry reason. Retrying every criterion would also re-score already-valid ones, and a static fake scorer reply only works if its quotes match the deterministic fake transcript exactly.
- **Cause:** The domain helper describes why a quote is unusable internally, not the wire contract; and per-criterion validity was not tracked across attempts.
- **Lesson:** Map internal validation reasons onto the single reason the event contract lists at the boundary, keep a per-criterion `validAt` so a retry prompt only contains failed keys plus their feedback, and make `FakeReplies["scorer"]` quote the canonical fake interview answers (the client mock transcript) by alias.
- **Follow-up:** tests in `internal/graph/scorer_test.go`
- **Repeated?:** first time

### 2026-11-09 — #10 Judge transport failure is run-fatal; only invalid JSON defaults
- **Context:** Review of the judge/guard loop, where a first attempt made any judge LLM error degrade to the default `sufficient` verdict.
- **Problem:** That degrade was an unapproved contract change: `EVENTS.md` §6.3 uses the judge as its example for `node.failed(will_retry=false)` → `node.skipped` → `run.failed` → `session.ended(failed)`, and `ARCHITECTURE.md` §2 allows the default `sufficient` only for invalid JSON. Separately, the guard checked question/time limits before "the last topic is done", so a sufficient final answer at Q15 finished as `question_limit` instead of `completed`.
- **Cause:** The gate's safe-default rule was applied to transport failures as well as invalid output, and the guard ordered budget limits before the completion condition.
- **Lesson:** Default a gate to its safe value only for invalid output; a transport or execution failure follows the §6.3 permanent-failure sequence (no applicant retry). Evaluate "no topics left and the answer is sufficient → completed" before any budget limit; and pass judge feedback into the follow-up prompt only inside `prompts.WrapUntrusted("judge_feedback", …)` because it derives from the applicant's answer.
- **Follow-up:** tests in `internal/graph/interview_loop_review_test.go`, `internal/graph/guard_test.go` and `internal/graph/judge_test.go`
- **Repeated?:** first time

### 2026-11-09 — #10 Guard must reserve budget for remaining topics
- **Context:** Adding the deterministic guard after the judge.
- **Problem:** A rule that only checks `verdict=follow_up && follow_up_count < 2` spends the last question or minute on a follow-up and loses a later topic.
- **Cause:** Follow-up affordability was judged against the per-topic counter only, not against the session's remaining questions/time and the pending topics.
- **Lesson:** Allow a follow-up only when the remaining question budget and observed pace (`elapsed/questions`) can still fit the follow-up plus one question per remaining topic; record an invalid judge reply as the default verdict it produced in `node.completed` because the judge is never retried.
- **Follow-up:** tests in `internal/graph/guard_test.go` (15 cases) and `TestGuardDropsFollowUpWhenQuestionsCannotCoverTopics`
- **Repeated?:** first time

### 2026-11-09 — #40 Planner transport outage must be applicant-retryable
- **Context:** A planner `llm.ErrUnavailable` during session start called `failRun` directly, permanently failing the application.
- **Problem:** The planner step mixed two different failures: a `plan_check` rejection (automatic retry, max 2 attempts) and a transport outage (should wait for `POST /retry`). Treating both as run-fatal made a transient 5xx strand the applicant.
- **Cause:** `runPlanner` returned one error for both paths and `startRun` had no retry hook for the planner.
- **Lesson:** Split a node's executor into "one attempt" and "automatic loop", and carry the loop's check budget and latest feedback through an applicant retry: the retry re-enters the loop (outages consume an attempt number but not a check attempt), rather than replaying one isolated attempt that would drop the remaining automatic retries and the rejection feedback.
- **Follow-up:** tests in `internal/graph/planner_retry_test.go` (`TestApplicantRetryAfterOutageKeepsPlanCheckBudget`, `TestApplicantRetryAfterOutageKeepsPlanCheckFeedback`) and `TestRunStartedIncludesApplicantRetryEdges`
- **Repeated?:** first time

### 2026-11-09 — #9 Planner output is untrusted downstream of the CV
- **Context:** Hardening the interviewer prompt after review of PR #39.
- **Problem:** `interviewerUser` rendered `topic.title/goal/seed_question` as trusted text, but the planner wrote those fields after reading the applicant's CV, so a crafted CV could plant instructions that bypass the `<<<UNTRUSTED_DATA>>>` blocks one node later.
- **Cause:** Trust was assigned by node type ("planner output") rather than by data provenance; anything derived from applicant text stays untrusted.
- **Lesson:** Wrap planner-written topic fields in `prompts.WrapUntrusted("planner_topic", …)` (and reject delimiter-bearing or over-long topic fields in `plan_check` so the planner retry handles them); treat derived LLM output as untrusted whenever its input was untrusted.
- **Follow-up:** tests in `internal/graph/plan_check_test.go` and `TestPlannerTopicFieldsReachInterviewerInsideUntrustedBlock`
- **Repeated?:** first time

### 2026-11-09 — #9 Sweeper: track in-flight steps, not live runners
- **Context:** Adding the session runner meant the sweeper had to stop finishing sessions a graph step was using, without leaving idle sessions `active` forever.
- **Problem:** A registry of "sessions with a live runner" skips a session between answers too, so an applicant who stops answering is never abandoned, and (with the guard not built yet) nothing ends the session at the 15-minute limit.
- **Cause:** Liveness was modelled as the existence of a runner goroutine/state, but the runners stay alive while waiting for the applicant.
- **Lesson:** Mark a session only while a graph step is in flight and clear the mark when the step ends; then the sweeper can abandon on `ABANDON_AFTER` idle and finish on the time limit (`time_limit`, never `abandoned`), emitting `session.ended` through the live runner or persisting it when no runner exists.
- **Follow-up:** tests in `internal/session/sweeper_test.go` and `TestSweepEndsIdleLiveSessionThroughManager`
- **Repeated?:** first time

### 2026-11-09 — #9 Retry attempts: derive the next attempt, never hard-code it
- **Context:** The manual `/sessions/{token}/retry` path re-ran the interviewer after a transport retry had already emitted `node.started` attempt 2.
- **Problem:** The retry closure used a fixed `attempt=2`, so `node.started(interviewer, 2)` and `loop.retry` attempt numbers repeated.
- **Cause:** `streamInterviewer` discarded its current attempt when it returned an error, leaving the caller no way to continue the counter.
- **Lesson:** Return the attempt actually emitted even on error and compute the retry as `lastAttempt+1`; attempt numbers must be derived from emitted events, never constants.
- **Follow-up:** test `TestManualRetryContinuesInterviewerAttempts`
- **Repeated?:** first time

### 2026-11-09 — #9 SSE: a stalled reader must not be able to freeze the runner
- **Context:** The session SSE handler fans persisted graph events from the in-process bus, which blocks `Emit` until the subscriber accepts an event, unsubscribes, or the emit context ends.
- **Problem:** A connected client that stops reading fills the 128-event buffer, and because the runner emits with a background context that never cancels, `Emit` blocks the whole session indefinitely.
- **Cause:** The handler had no per-write deadline and did not cancel its subscription when a write failed, so the bus could never release the blocked publisher.
- **Lesson:** Every SSE write needs a `ResponseController.SetWriteDeadline` and a failed/late write must cancel the subscription before returning; the client then reconnects with `Last-Event-ID` and replays.
- **Follow-up:** tests in `internal/sse/writer_test.go` and `TestBusCancelUnblocksStalledPublisher` in `internal/graph/graph_test.go`
- **Repeated?:** first time

### 2026-11-08 — #8 LLM client: zero-valued config and truncated replies must not look successful
- **Context:** Hardening the LLM client after review: temperature 0.0 for judge/scorer disappeared from the request, and a reply cut off at `max_tokens` was returned as success.
- **Problem:** `json:"temperature,omitempty"` drops a deliberate 0.0 so OpenRouter used its own default; `parseChat`/`parseStream` ignored `finish_reason`, so truncated JSON reached the caller as a valid response. Also the fake keyed replies by model id, but three nodes share one model.
- **Cause:** `omitempty` was used for a field where zero is meaningful, and the OpenAI `finish_reason` contract was not checked before returning.
- **Lesson:** Never `omitempty` a request field whose zero value is meaningful; treat `finish_reason: "length"` as a non-retryable truncation error; key test fakes by graph node id, not only model id.
- **Follow-up:** tests in `internal/llm/llm_test.go` and `internal/graph/graph_test.go`
- **Repeated?:** first time

### 2026-11-08 — #8 Graph events: node_events node_id/attempt must be nullable, not defaulted
- **Context:** Building the graph runner + event bus that persist run/node/edge/check events into `node_events`.
- **Problem:** The Day-1 schema had `node_id text NOT NULL` and `attempt integer NOT NULL DEFAULT 1`, and the store defaulted `attempt==0 → 1`; but EVENTS.md §2/§4 mark both `node_id` and `attempt` nullable, and run-level/edge events carry `null` for both — a default would silently turn `run.started` into `attempt=1`.
- **Cause:** The table was written before the event envelope nullability was respected.
- **Lesson:** Read the event envelope contract (EVENTS.md §2) before adding columns or defaults to `node_events`; a store-level default for a nullable contract field corrupts run-level events.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-08 — #7 Apply: two sequential inserts strand an applicant without a session
- **Context:** Review of the apply flow found `CreateApplication` then `CreateSession` as two independent store calls.
- **Problem:** A `CreateSession` failure returned 500 but left the application row, so the applicant's retry hit 409 `already_applied` and `resend-invite` (which only covers `invited` sessions or `failed` applications) could never reach them.
- **Cause:** Two writes that must both happen (an application always has a session) were not composed into one unit of work.
- **Lesson:** Compose multi-row writes that must be all-or-nothing into a single store method backed by a transaction (Postgres) or one lock (memory), not sequential calls.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-08 — #7 Session start: read-then-write race breaks start-once (409)
- **Context:** Review of `POST /sessions/{token}/start` found the handler did a resolve-then-unconditional `UpdateSession`.
- **Problem:** Two concurrent starts both read `invited`, both overwrite `started_at`, and both return 200; the `ErrConflict`→409 branch was unreachable.
- **Cause:** `UpdateSession` had no status guard, so the "already started" check happened outside any transaction/lock.
- **Lesson:** Enforce once-only status transitions with an atomic conditional write (`UPDATE … WHERE status = 'invited' RETURNING` in Postgres, a lock-guarded check in the memory store), never a read-then-write.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-07 — #6 Apply flow: `dslipak/pdf` panics on malformed input; non-UUID jobId reaches Postgres
- **Context:** Hardening the apply endpoint after review of PR #34 (CV extraction + apply handler).
- **Problem:** `dslipak/pdf`'s `Reader.NumPage()`/`Page()` panic on malformed object references (they only recover inside `GetPlainText`), so a crafted PDF drops the connection; and a non-UUID `jobId` reaches Postgres and fails with SQLSTATE 22P02 → 500 instead of the contract's 404.
- **Cause:** Page-tree traversal is unprotected by the library, and the in-memory store masks the UUID cast error (it returns `ErrNotFound` for any unknown id).
- **Lesson:** Recover panics inside `cv.Extract` and cap the pages read; validate UUID path params before any store call so a malformed id returns 404, not 500.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-07 — #6 Apply flow: `ledongthuc/pdf` forces the module onto Go 1.24
- **Context:** Adding CV text extraction (`internal/cv`) with the `github.com/ledongthuc/pdf` library from `docs/TECH-STACK.md` §1.
- **Problem:** `go get github.com/ledongthuc/pdf@latest` bumped the module's `go` directive from `1.23.0` to `1.24.1`, which silently broke the Docker build (Dockerfile pinned `golang:1.23-alpine`, GOTOOLCHAIN=local, so `go mod download` failed with "go.mod requires go >= 1.24.1"). CI uses `go-version-file` and never caught it.
- **Cause:** The library has no tagged releases; its single `go.mod` (added 2025-05) declares `go 1.24.1`, so *any* pinned pseudo-version requires ≥ 1.24.1 — there is no 1.23-compatible version.
- **Lesson:** A dependency whose `go.mod` declares no `go` directive (pre-modules) stays compatible with a `go 1.23` module. Here the parent `github.com/dslipak/pdf v0.0.2` (which `ledongthuc/pdf` forked, same `NewReader`/`GetPlainText` API) has no `go` directive, so use it to keep the module on Go 1.23 without touching the Dockerfile.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-06 — #3 DB migration + store: Supabase pooler DSN normalization
- **Context:** Wiring pgx/goose to Supabase and running `00001_init.sql`.
- **Problem:** A transaction-pooler DSN (`:6543?pgbouncer=true`) breaks pgx prepared statements, and `pgbouncer` is not a valid Postgres runtime parameter, so parsing/connecting fails unless handled.
- **Cause:** pgx forwards unknown query params as runtime settings and defaults to statement caching, which PgBouncer transaction mode does not support.
- **Lesson:** Normalize Supabase DSNs in one place: strip `pgbouncer`, and when the port is `6543` or `pgbouncer=true`, set pgx `DefaultQueryExecMode = simple_protocol`; prefer the session pooler (`:5432`) per `docs/TECH-STACK.md`.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-06 — #2 Contract + Go skeleton: OpenAPI 3.1 validator on Go 1.23
- **Context:** Choosing a conformance harness that validates responses against the OpenAPI 3.1 contract while keeping the server on Go 1.23.
- **Problem:** `kin-openapi` only gained OpenAPI 3.1 support in v0.136.0, and every 3.1-capable release requires Go 1.25 or newer; the latest requires Go 1.25.
- **Cause:** 3.1 support landed only after the module raised its `go` directive, so a Go 1.23 module cannot use it.
- **Lesson:** For OpenAPI 3.1 validation on Go 1.23, pin `pb33f/libopenapi` v0.22.2 with `pb33f/libopenapi-validator` v0.4.7 (both `go 1.23.0`); `go mod tidy` will otherwise bump the `go` line.
- **Follow-up:** none
- **Repeated?:** first time

### 2026-11-06 — #22 Contract sweep: gmail + failure/replay/event sequence
- **Context:** Applying the approved contract-change proposals across `docs/API.md`, `docs/EVENTS.md`, `docs/PRD.md`, the supporting docs, and the affected server issues.
- **Problem:** The email channel had drifted (`resend` vs `gmail`) across docs and issues, and the normative event example omitted node lifecycles and edge traversals.
- **Cause:** Contract decisions were applied locally without a repo-wide sweep or one normative example.
- **Lesson:** For a contract-change issue, grep the old term across all docs/issues and update the normative example in the same PR; supporting docs and issue acceptance criteria are part of the contract surface.
- **Follow-up:** none
- **Repeated?:** first time
