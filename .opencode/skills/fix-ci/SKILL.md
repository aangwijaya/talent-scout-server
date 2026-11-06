---
name: fix-ci
description: Find the failing PR check, read its log, and apply the smallest focused fix (max 3 attempts). Used by loop-on-ci. Only active once CI exists.
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Fix CI

> Adapted from cursor/plugins (cursor-team-kit/skills/fix-ci), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

**Activates once CI exists** (server #2 adds `.github/workflows/ci.yml`). Before that, `gh pr checks` shows no checks — skip this skill and rely on the local green checks.

## Workflow
1. Resolve the active PR and inspect `gh pr checks --json name,bucket,state,workflow,link`.
2. Extract the first actionable error: `gh run view <run-id> --log-failed` (run id from the check link).
3. Reproduce locally with the same command: `test -z "$(gofmt -l .)" && go build ./... && go vet ./... && go test ./...`
4. Apply the smallest safe fix, re-run the local checks, then `git push` (no force).
5. Re-check the PR check set; repeat via `loop-on-ci`.

## Guardrails
- Never merge, never force-push, never push to `main`, never bypass hooks (`--no-verify`).
- **Max 3 fix attempts** per issue (shared with the green-check limit). Still red → `gh issue edit <n> --remove-label status:in-progress --add-label status:blocked` + a comment (what was tried, last error, suspected cause), leave the PR open.
- Fix one actionable failure at a time; prefer minimal, low-risk changes before refactors.
- Don't weaken the check to pass (no skipping tests, no disabling lint rules, no editing `ci.yml` unless the issue is about CI).
- Keep `gh pr checks` as the source of truth for overall PR CI state.

## Output
- Primary failing job and root error
- Fixes applied in iteration order
- Current CI status and next action
