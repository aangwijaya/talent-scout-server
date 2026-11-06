---
name: loop-on-ci
description: Fix a failing PR check reported in a node's gate feedback. The runner watches CI, so never poll it from a session.
compatibility: opencode
---

# Loop on CI

> Adapted from cursor/plugins (cursor-team-kit/skills/loop-on-ci), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

`ops/run` watches PR checks and returns failures in your brief as `EXPECTED / SAW`. Don't poll with `gh pr checks --watch`: agents share one API limit.

1. Read the failing check's log once with `gh run view <run-id> --log-failed`, then use `fix-ci`.
2. Reproduce locally, and fix one cause per attempt.
3. A failure clearly unrelated to the node is a gap: create a `type:gap` issue, set `STATUS: gap`, and stop.
4. A flaky failure goes in `docs/LESSONS.md` with evidence.
