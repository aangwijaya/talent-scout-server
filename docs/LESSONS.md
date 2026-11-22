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

### 2026-11-22 — #92 A fix without a sabotage check can leave a branch uncovered
- **Context:** Verifying server#90 (stream stops keep usage) found one sabotage that no test caught: `Fake.Stream` could return `Response{}` with the stop error and every test still passed.
- **Problem:** The new tests only exercised the real OpenRouter client; the fake used by graph tests had no test for the stopped-stream response, so fake-driven usage could silently drop to zero.
- **Cause:** The behavior was added to the fake to mirror the real client but only the real client got a test.
- **Lesson:** When a fix changes a fake to mirror production, test the fake too; prove the test guards the behavior by applying the sabotage (`return Response{}, err`) and watching it fail before reverting.
- **Follow-up:** `TestFakeStreamStoppedEarlyKeepsContentAndUsage` in `internal/llm/llm_test.go`; fails on the sabotage, passes on the fix.
- **Repeated?:** first time

### 2026-11-21 — #88 A gap can describe wrapping the code does not have
- **Context:** Closing the scorer role-context gap reported from server#79: the issue said `scorerUser` wraps `job_title`, `job_description` and `rubric_criterion` as untrusted data.
- **Problem:** `scorerUser` only renders `rubric_criterion` blocks; it never included the job title or description (server#79 added those to the planner and interviewer only). The requested sentence would name blocks the scorer never receives.
- **Cause:** The gap was written from the shared `SafetyRule` wording and the planner/interviewer change, not from the scorer's actual user message.
- **Lesson:** Read the node's user-message builder and its wrapping tests before changing its system prompt, and flag a prompt that names inputs the node never receives instead of shipping it. The human chose to send the role context: `scorerUser` now opens with `roleContext(job)` (wrapped title, capped `JobDescriptionBlock`), the same blocks the interviewer gets.
- **Follow-up:** `TestScorerPromptIncludesJobAsUntrustedRoleContext` pins the job blocks, their wrapping and order; `TestPromptsTreatJobBlocksAsRoleContext` covers the scorer sentence; `TestScorerPromptTreatsRubricTextAsUntrusted` still guards the rubric wrapping.
- **Repeated?:** first time

### 2026-11-21 — #86 Early stream stops lost usage and cost
- **Context:** The interviewer stops the SSE stream at the first `?` (and at 120 words) by returning an error from `onDelta`. The run then recorded zero interviewer tokens and `$0` for that call.
- **Problem:** `parseStream` returned immediately on the stop error, so it never read OpenRouter's trailing usage chunk (and `OpenRouter.call` discarded the partial `Response` on any non-retryable error, so even a returned usage would have been dropped).
- **Cause:** The stream parser treated "the reply is complete" as "stop reading", but usage arrives after the last content delta.
- **Lesson:** Once a reply is complete (guard stop or `finish_reason: "length"`), stop forwarding deltas but keep draining the stream to `[DONE]` so the usage chunk is captured; return the forwarded content, usage, and the stop error, and pass that `Response` through the retry wrapper. If the provider closes without a usage chunk, keep zero usage rather than estimating.
- **Follow-up:** `TestOpenRouterStreamStoppedEarlyKeepsUsageAndForwardedText`, `TestOpenRouterStreamTruncatedKeepsUsage`, and `TestStoppedInterviewerReplyRecordsUsage` in `internal/graph/interviewer_test.go`.
- **Repeated?:** first time

### 2026-11-20 — #81 Planner truncated at max_tokens after the job context grew
- **Context:** The server#81 live gate (3 sessions) failed one session before the opening question: the planner hit `finish_reason=length` at `max_tokens: 3000` (1,536 prompt tokens). Its other calls used 1,585–2,030. The scorer reached 5,852 of 8,000; the new authenticity node used 347–390 of 2,000.
- **Problem:** Since #79 the planner reads the full job and must tag each topic's `source`, so the reasoning model has less headroom; an occasional long reasoning pass truncates and the applicant never gets a question.
- **Cause:** The 3000 cap was sized before the job context and sources were added.
- **Lesson:** Recheck every reasoning node's cap whenever its prompt grows; size from the largest live completion with about 2x headroom. Planner is now `6000`. The live test's call log must label every LLM node (authenticity was logged as `unknown`) so its truncation check covers it.
- **Follow-up:** `TestJudgeAndScorerHaveRoomForReasoning` now covers the planner; `nodeFromRequest` labels authenticity and checks it for truncation.
- **Repeated?:** yes, same as the 2026-11-20 #79 judge/scorer entry

### 2026-11-20 — #79 Judge and scorer were truncated at max_tokens
- **Context:** The server#79 live gate (richer seed job, 3 sessions) failed two sessions on `finish_reason=length`: the judge at `max_tokens: 1200` on the second answer, and the scorer at `max_tokens: 4000` with 3,779 prompt tokens after a full 15-question interview. The applicant ended without a score.
- **Problem:** `deepseek/deepseek-v4.1-flash` spends part of its output on reasoning, so caps sized for the visible JSON leave no headroom when the prompt grows.
- **Cause:** The caps were set before the job description reached the prompts; a longer job means more reasoning.
- **Lesson:** Size reasoning-model caps from the live gate, not from the expected JSON: judge `2400`, scorer `8000`. The scorer produced 4,000 tokens in under ~17 s, so 8,000 still fits the 60 s timeout. A cap only costs what is actually generated.
- **Follow-up:** `TestJudgeAndScorerHaveRoomForReasoning` in `internal/e2e/models_config_test.go`.
- **Repeated?:** same family as the 2026-11-18 interviewer timeout/max_tokens entry

### 2026-11-20 — #79 A planner-output change can stay inside a narrow fence
- **Context:** Adding a job/cv `source` to planned topics, a job-topic validation rule, and job context for the interviewer. The fence named only `planner.go`, `interviewer.go`, `prompts/*`, the graph tests, the seed job and `internal/e2e/*`.
- **Problem:** The obvious implementation needs `PlannedTopic`/`CheckPlan` (`plan_check.go`), the job lookup in `session.go`, and a `source` in the fake planner reply (`fake_replies.go`) — all outside the fence, so the gate rejected the first attempt.
- **Cause:** The plan type, its validation, the caller that supplies new inputs, and the fake fixture were outside the named files.
- **Lesson:** Keep the change inside the fence instead of widening it: parse and validate `source` inside `parsePlan` (a source rejection still reaches the planner as a retryable `plan_check` failure through the existing `invalid_json` path), read a missing source as `job` so the existing fake fixture stays valid, and load the job for the interviewer in `streamInterviewer`, which already holds the runner's store and session id. Tests still drive the same helpers.
- **Follow-up:** tests in `internal/graph/plan_check_test.go`, `internal/graph/job_context_test.go`, `internal/graph/session_test.go`
- **Repeated?:** first time

### 2026-11-20 — #63 The answer-choice check flagged "pick one real example"
- **Context:** The second live gate (e030e35, three sessions) had no two-part turns and no timeouts, but each session failed once on "offers answer choices".
- **Problem:** The flagged turns asked the applicant to "pick one interaction where users felt lag … How did you capture it?". That is an open question about their own work, not multiple choice.
- **Cause:** The checker (and the prompt) banned bare "pick one", "choose one", "which one" and "select one", which also match open requests for one concrete example.
- **Lesson:** Flag only choices the interviewer supplies: option letters, "Option A", "one of these three", "just the letter". Say in the prompt that asking for one real example of their own is fine. Check a live-gate failure's text before tightening the model further; the checker can be the bug.
- **Follow-up:** `internal/e2e/questions_test.go` keeps both sides (the live "pick one interaction" turn passes, "pick one of these three: …" fails); `internal/graph/interviewer_test.go` now proves the 120-word stop ends the stream and that a `?` after word 120 cannot lengthen the reply.
- **Repeated?:** no

### 2026-11-20 — #63 A prompt alone did not stop two-part questions; cut at the first `?`
- **Context:** The human live gate for #63 ran three sessions on `z-ai/glm-5.3-flash` with the new prompt: 0 interviewer timeouts (every call `finish_reason="stop"`, longest 412 completion tokens) and no answer choices, but 9 of ~36 interviewer turns still held 2+ question marks and all three sessions failed the one-question check. Costs were `$0.006874`, `$0.005392` and `$0.007097` per session.
- **Problem:** The failures were always a question followed by a second one ("…? And …?", "…? Could you walk me through…?"). Negative instructions ("never ask a second question") did not hold on this model.
- **Cause:** The prompt asked for compliance with no deterministic backstop, so any model turn that ignored the rule reached the applicant and the transcript.
- **Lesson:** For a hard reply-shape rule, pair the prompt with a streaming guard: stop at the first `?` exactly like the 120-word stop, keep the deltas already sent, and store only that prefix. Add the exact wrong pattern ("…? And …?") to the prompt and require exactly one `?`, at the last character. Keep the 60 s timeout and `max_tokens: 4000` with `reasoning: { effort: low }`; the gate saw no timeouts and ≤412 completion tokens, and live costs fit the `~$0.013` per-session budget.
- **Follow-up:** tests in `internal/graph/interviewer_test.go` (two questions stream only the first, a single question is unchanged) and `internal/prompts/prompts_test.go`; live E2E `interviewerTurnProblems` still checks the stored turns
- **Repeated?:** same as entry 2026-11-18 (prompt-only rule was not enough)

### 2026-11-18 — #63 Interviewer asks one open question; size the timeout from recorded latency
- **Context:** Following #14, live runs still produced multiple-choice and two-part interviewer turns, a 30 s timeout sat close to a recorded 26.2 s reply, and TECH-STACK §8 still carried the old `~$0.005` estimate.
- **Problem:** The prompt said "one focused question" but did not ban option letters, "which one", "pick one", or a follow-on request, so the model still returned "(a)/(b)/(c)" turns. `config/models.yaml` had one shared 30 s timeout for every node; the recorded fixture contains an interviewer reply of 1827 completion tokens in 26,179 ms and a later `node.failed` (`timeout`) on the same node.
- **Cause:** Negative instructions were too generic and left the exact choice formats open, and the timeout was not re-sized after raising the interviewer to `max_tokens: 4000` with reasoning.
- **Lesson:** In the interviewer system prompt, require exactly one open-ended question per turn, at most 80 words, ending with a single question mark, and explicitly forbid a second question, answer choices, option letters, "which one", "pick one", "choose one", "select one", and "just the letter"; keep the runtime 120-word stop unchanged. Size the shared request timeout from recorded latency: 60 s (the OpenRouter client cap) leaves room over the 26.2 s reply; keep `max_tokens: 4000` and `reasoning: { effort: low }`. Measured cost from the #14 runs: normal sessions `$0.006772` (26,882 in / 7,909 out) and `$0.008021` (36,380 in / 8,068 out), retry-heavy `$0.012421` (37,192 in / 17,599 out), so budget about `$0.013` per session rather than `$0.005`.
- **Follow-up:** offline prompt test in `internal/prompts/prompts_test.go`, question-form checker in `internal/e2e/questions_test.go` used by the live E2E, and the config guard in `internal/e2e/models_config_test.go`. Fresh live sessions were not possible in this node because `OPENROUTER_API_KEY` was not in the process environment; re-run the live E2E when the key is available.
- **Repeated?:** first time

### 2026-11-16 — #31 Boot reconciliation must close every started node and fail orphaned runs
- **Context:** Reconciling runs left `running` after a restart, plus three review findings (abandoned app state, cancelled-ctx node terminals, planner outage with no topics).
- **Problem:** A running run kept its application in `scoring` forever, and a failure persisted with the caller's cancelled context left `node.started` with no terminal event. On boot there is no runner to resume, so finishing such sessions as `abandoned` and scoring them is not possible. EVENTS.md listed no error value for "closed because the server restarted" (server#66 added `server_restart`).
- **Cause:** Reconciliation was only time-based in the sweeper, and terminal events reused the request/shutdown context that had just died. The abandoned/time-limit path assumed a live in-process manager.
- **Lesson:** Reconcile persisted state on boot before listeners start: close each dangling `(node_id, attempt)` with exactly one terminal event, then `node.skipped(run_failed)` for every machine node that never ran, then `run.failed`, then `session.ended(failed)`, and move the application to `failed`. Emit terminal node events with `context.WithoutCancel` plus a short timeout, and never emit a retry pair when the parent context is done. Ending a session before any topic exists is a failed run, not an abandoned/scored one. When a needed wire value is outside the contract, open a `contract-change` issue and mark the node `gap` instead of inventing one.
- **Follow-up:** tests in `internal/graph/reconcile_test.go`, `internal/graph/runner_terminal_ctx_test.go`, `internal/session/sweeper_test.go`, `internal/graph/planner_retry_test.go`; contract-change issue server#66 added `server_restart` for both fields
- **Repeated?:** first time

### 2026-11-14 — #64 Accept an answer when message.completed is published, not when the turn ends
- **Context:** Fixing the 409 `invalid_state` storm the client saw after the opening question: it unlocks the answer box on `message.completed(assistant)`, but the server kept `st.busy` set until the whole turn tail (`node.completed`, question count, `session.updated`) finished.
- **Problem:** `POST /sessions/{token}/messages` checked only `st.busy`, which flips after `message.completed` is published, so a POST that follows the event could be rejected even though the contract says input is enabled again. Reordering the tail or clearing `busy` before the event makes `Wait`/the sweeper think the step is done and lets the applicant event interleave before `node.completed(interviewer)`.
- **Cause:** One flag described two different things: "a graph step is using the runner" and "the applicant may answer". The turn tail still needs the runner after the reply is complete.
- **Lesson:** Track `awaiting` separately from `busy`, set it before publishing `message.completed`, and serialize the applicant's stored answer and `message.completed(applicant)` on the same per-session `stepMu` as the preceding step, so the event is accepted immediately while `node.completed` and the next step keep the contract order and never use the runner concurrently. A gated test store that pauses `node.completed(interviewer)` reproduces the race deterministically.
- **Follow-up:** tests in `internal/http/handlers/session_answer_ready_test.go` (opening and later turns)
- **Repeated?:** first time

### 2026-11-13 — #14 Live tests read env only; a recorded fixture can bake in server bugs
- **Context:** Round 2 of the live E2E work re-read the fixture that round 1 recorded and fixed the live-test credential lookup.
- **Problem:** `liveCredentials` fell back to reading the repo `.env` when `OPENROUTER_API_KEY` was empty, so `-tags live` ran live instead of skipping. The fixture also carried two server bugs verbatim: every assistant `message.completed` had `created_at: 0001-01-01T00:00:00Z` (the store sets `CreatedAt` on a copy), and one `node.completed.output_summary` ended in U+FFFD because `summarize` cut at a byte offset inside a rune.
- **Cause:** The live gate consulted a file instead of the process environment, and the fixture was recorded from real output without asserting the contract fields it claimed to demonstrate.
- **Lesson:** Live tests resolve credentials from `os.Getenv` only and skip on an empty key; set `CreatedAt` on the `domain.Message` value that is both stored and emitted; cut summaries on a rune boundary; and back a recorded fixture with tests that fail when its event codes, node ids, retry budget or timestamps drift.
- **Follow-up:** tests in `internal/e2e/credentials_test.go`, `internal/e2e/e2e_fake_test.go`, `internal/e2e/fixture_test.go` and `internal/graph/runner_test.go`
- **Repeated?:** first time

### 2026-11-13 — #14 Reasoning tokens eat `max_tokens`; live prompt tuning and cost
- **Context:** Running the fake and live (OpenRouter) end-to-end flow from `internal/e2e` with the seed job and three made-up CVs, then tuning the planner/interviewer/judge/scorer prompts.
- **Problem:** The first live runs failed loudly on `finish_reason: "length"`: planner ended at exactly 800/800 completion tokens, judge at 200/200, and the interviewer at 500 and later 2000 tokens with no visible text (the reasoning trace consumed the whole budget). A happy-path session cost `$0.010162` (34,984 in / 12,566 out), double the `~$0.005` estimate in `docs/TECH-STACK.md` §8.
- **Cause:** `deepseek/deepseek-v4.1-flash` and `z-ai/glm-5.3-flash` emit reasoning tokens that count toward `completion_tokens`, so the caps tuned for visible JSON/text were far too low. The judge prompt was also strict enough that a substantive answer could still end at the 15-question limit.
- **Lesson:** Size `max_tokens` for reasoning plus output, not output alone: in the recorded retry run the completion counts were planner 1623, judge 151–476, scorer 988–1602, and interviewer 95–1827. Keep planner ≥3000, judge ≥1200 and scorer ≥4000, and set `reasoning: { effort: low }` on the streaming interviewer (recorded interviewer completions then stayed within 95–1827). Recheck cost with real token counts: normal live sessions cost `$0.006772` and `$0.008021`; the retry-heavy one-word-answer run kept in `testdata/recorded-run-with-loop-retry.json` cost `$0.012421` (37,192 in / 17,599 out). §8's `~$0.005` per session is low but the same order of magnitude. Tuning that worked: planner must cover every rubric key; judge treats a concrete, scoreable account as sufficient and only asks for missing parts; scorer must copy a contiguous verbatim phrase of at least 3 words and never paraphrase. Open question: a 30 s interviewer timeout against `max_tokens` 4000 could still cut a long reasoning reply; round 2 made no live calls, so this needs live evidence before changing it.
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
