---
name: get-pr-comments
description: After the human reviews a PR, fetch and summarize the review comments into an ordered fix list. Use when a PR you opened got review comments or changes requested.
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Get PR comments

> Adapted from cursor/plugins (cursor-team-kit/skills/get-pr-comments), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

## Workflow
1. Resolve the PR: `gh pr view --json number,url,reviewDecision,headRefName`
2. Fetch review state, inline comments, and discussion:
```bash
gh pr view <pr> --comments
gh api repos/{owner}/{repo}/pulls/<pr>/reviews --jq '.[] | {user: .user.login, state, body}'
gh api repos/{owner}/{repo}/pulls/<pr>/comments --jq '.[] | {user: .user.login, path, line, body}'
```
3. Group feedback by severity and actionability; return a concise ordered action list.
4. Fix in the same branch, run the green checks (`test -z "$(gofmt -l .)" && go build ./... && go vet ./... && go test ./...`), push (no force), reply to each addressed comment with what changed, then go back to `loop-on-ci`. Keep the label at `status:review`.
5. General review rules ("always check RLS", "no raw hex") → entry in `docs/LESSONS.md`.

## Guardrails
- Never merge, never resolve the human's threads yourself, never force-push.
- Unclear comment → ask in the PR thread instead of guessing.
- Max 3 fix attempts per round; then `status:blocked` + comment.

## Output
- Grouped feedback summary
- Action list ordered by priority
- Open questions that still need clarification
