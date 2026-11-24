---
name: llm-node-changes
description: Change an LLM node's prompt, model, max_tokens, timeout, or reply shape. Use when editing internal/prompts/*, a node's user-message builder, config/models.yaml, or adding an LLM node; covers reasoning-token budgets and deterministic reply-shape guards.
compatibility: opencode
---

# Changing an LLM node (prompt, model, caps)

Two failures have already happened twice. Apply both rules before editing a
prompt or `config/models.yaml`; see `docs/LESSONS.md` #79, #81 and #63.

## 1. Budget reasoning tokens, not visible output

Reasoning models spend part of `max_tokens` on hidden reasoning. A cap sized for
the expected JSON or the visible question truncates (`finish_reason: "length"`)
as soon as the prompt grows.

- Size a cap from a **live gate**, not from the expected output: run 3 sessions,
  take the largest completion per node, and keep about **2x** headroom.
- **Recheck the cap whenever the node's prompt or role context grows**: adding
  the job description, rubric text, or a new output field raises reasoning use
  even when the JSON shape did not change.
- A cap only costs what is actually generated, so do not shave it to save money.
- Current floors (live gates on 2026-11-20): planner `6000`, interviewer `4000`,
  judge `2400`, scorer `8000`, authenticity `2000`, rubric_suggester `3000`.
  They are enforced by `scripts/llm_gate_checks_test.go`, which fails on a lower
  cap, on an LLM node missing from its floor table, and on an interviewer that
  stops streaming or loses `reasoning: { effort: low }`.
- The live harness (`nodeFromRequest` and `reportLLMCalls`, with
  `truncationProblems` in `internal/e2e/llm_calls.go`) labels every prompt in
  `internal/prompts` (including `rubric_suggester`) and checks every labeled
  node, so a new node is covered by default. Do not add a hand-picked node list
  to `truncationProblems`.
- The interviewer is the one exception for `finish_reason`: its stream is
  stopped on purpose at the first `?` or 120 words and then drained for usage,
  so the provider can still report `finish_reason=length` for content generated
  after the guard stop. It is checked only against
  `completion_tokens > max_tokens`. Authenticity was once `unknown`, and its
  truncation was invisible.

## 2. Pair a hard reply-shape rule with a deterministic guard

A prompt alone did not stop two-part interviewer questions on
`z-ai/glm-5.3-flash`: 9 of ~36 turns still contained 2+ question marks.

- For a hard shape rule, add a **streaming guard** that stops the reply, exactly
  like the 120-word stop. The interviewer stops at the first `?`, forwards and
  stores only the prefix through that `?`.
- Keep draining the stream after the stop so the trailing usage chunk is still
  captured (`docs/LESSONS.md` #86). Return the forwarded text, usage and the
  stop error.
- Still fix the prompt: name the exact rule and the exact wrong pattern
  (`"What did you build? And why did it matter?"` is two questions), and require
  exactly one `?`, as the last character. Do not rely on a negative instruction
  alone.
- Keep the behavior checks that prove both sides:
  `TestInterviewerReplyEndsAtFirstQuestion`,
  `TestInterviewerPromptAsksOneOpenEndedQuestion`, and
  `TestInterviewerTurnProblems`.

## Checklist

- [ ] Live gate run (3 sessions). The harness labels every node and fails on
      truncation for every labeled node; only the interviewer relies on
      `completion_tokens > max_tokens` because its reply is guard-stopped. Still
      read each changed node's `finish_reason` in the call log.
- [ ] No call ends `finish_reason=length`; caps meet the floors and 2x the
      largest observed completion.
- [ ] Prompt growth rechecked against `config/models.yaml` and
      `docs/TECH-STACK.md` §8.
- [ ] Hard reply-shape rule has a streaming guard plus a prompt statement and
      tests for both.
- [ ] `gofmt -l .` empty, `go vet ./...`, `go test ./...`.
