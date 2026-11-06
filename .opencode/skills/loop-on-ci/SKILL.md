---
name: loop-on-ci
description: Watch the PR checks after gh pr create and fix failures until green (max 3 attempts). Uses gh pr checks as the source of truth. Only active once CI exists.
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Loop on CI

> Adapted from cursor/plugins (cursor-team-kit/skills/loop-on-ci), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

**Activates once CI exists** (server #2 adds `.github/workflows/ci.yml`). Before that, `gh pr checks` shows no checks — skip this skill and rely on the local green checks.

Use `gh pr checks` as the source of truth. It includes all PR-attached checks, while `gh run list` only covers GitHub Actions.

## Workflow
1. Resolve the PR for the current branch: `gh pr view --json number,url,headRefName`
2. Inspect current checks before waiting: `gh pr checks --json name,bucket,state,workflow,link`
3. Already failed → diagnose with the `fix-ci` skill first.
4. Pending → watch in the foreground (this replaces a background CI-watcher agent): `gh pr checks --watch --fail-fast`
5. On failure, report status + failing check + link, then run `fix-ci`. After each push, re-check the full check set and repeat until green.
6. Green → keep the issue at `status:review` and stop; the human reviews.

## Guardrails
- Never merge, never force-push, never push to `main`, never bypass hooks (`--no-verify`).
- **Max 3 fix attempts** per issue (shared with the green-check limit). Still red → `gh issue edit <n> --remove-label status:in-progress --add-label status:blocked` + a comment (what was tried, last error, suspected cause), leave the PR open.
- Keep each fix scoped to a single failure cause.
- If the failure is clearly unrelated to the PR and is fixed on main, rebase/merge latest `origin/main` (normal push, no force) instead of bloating the PR.
- Flaky failure → re-run once (`gh run rerun <run-id> --failed`) and report the flake evidence; record it in `docs/LESSONS.md`.

## Output
- Current CI status (passed/failed), PR URL
- Failure summary and fixes applied
