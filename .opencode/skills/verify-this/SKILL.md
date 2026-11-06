---
name: verify-this
description: Verify a fix claim with fresh local evidence (baseline vs treatment) and return VERIFIED, NOT VERIFIED, or INCONCLUSIVE. Use after implementing when an issue or PR claims a fix.
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Verify This

> Adapted from cursor/plugins (cursor-team-kit/skills/verify-this), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

Run after implementing, **when the issue or PR claims a fix** (type:bug, "fixes X", performance claims). Put the verdict block in the PR body.

Verification is not a recap. It proves or disproves a specific claim with repeatable evidence.

## When To Use

- The user asks "verify this", "prove it works", "did this fix it", or "show me the evidence".
- A bug fix needs a before/after repro.
- A UI, CLI, API, performance, or memory claim needs measurement.
- A test passes but the user-visible behavior still needs confirmation.

Do not use this for vague claims like "the code is cleaner". Ask for a measurable claim first.

## Workflow

1. Restate the claim in falsifiable form: condition, metric, and threshold.
2. Pick the smallest local surface that can disprove it.
3. Capture a baseline from the old state: `git stash` / `git worktree add /tmp/verify-base origin/main` (remove it afterwards), or the current broken repro.
4. Capture treatment from the changed state with the same command, data, warmup, and environment.
5. Compare raw artifacts: numbers, screenshots, terminal transcripts, HTTP responses, profiles, heap snapshots, or test output.
6. Return exactly one verdict: `VERIFIED`, `NOT VERIFIED`, or `INCONCLUSIVE`.

## Local Surfaces

- Code behavior: focused `go test ./... -run <Name>` or a minimal repro test.
- API behavior: `go run ./cmd/server` locally (`LLM_MODE=fake`, `EMAIL_MODE=log`) and diff `curl -sS` request/response pairs, SSE via `curl -N`.
- DB behavior: store integration tests (`TEST_DATABASE_URL`) or a migration up/down run.
- Performance: same-machine baseline/treatment timings (`go test -bench`).

## Artifact Layout

When safe to write artifacts:

```text
/tmp/verify-this/<claim-slug>/
├── claim.md
├── timeline.md
├── baseline/
├── treatment/
├── diff/
└── verdict.md
```

If artifacts may contain sensitive code, prompts, screenshots, HTTP bodies, or heap data, keep only the minimal inline evidence unless the user agrees to disk storage.

## Verdict Rules

- `VERIFIED`: baseline and treatment differ in the predicted direction, by the claimed threshold, with no obvious confound.
- `NOT VERIFIED`: the behavior is unchanged, moves the wrong way, or misses the threshold.
- `INCONCLUSIVE`: no valid baseline, noisy signal, failed measurement, or an environment difference invalidates the comparison.

## Output

Use this shape:

```text
VERIFIED | NOT VERIFIED | INCONCLUSIVE
Claim: <falsifiable claim>

Evidence:
<metric/artifact>: baseline=<...>, treatment=<...>, delta=<...>, threshold=<...>

Reasoning:
<one tight paragraph naming the evidence and any confounds>
```

Do not soften a negative result. A clear `NOT VERIFIED` is useful.
