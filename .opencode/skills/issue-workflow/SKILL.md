---
name: issue-workflow
description: Pick up, work on, and hand off one GitHub issue in talent-scout-server (labels, worktree, branch, green checks, PR, LESSONS). Use every time you start or finish an issue.
compatibility: opencode
---

# Issue Workflow (server)

Repo: `aangwijaya/talent-scout-server`. All GitHub operations go through `gh`.

## 1. Pick an issue
```bash
gh issue list --label agent:opencode --label status:todo --state open --limit 50
gh issue view <n>
```
- Take the lowest `day:N`. Check every "Blocked by" line in the body: all must be `CLOSED`.
  - Same repo: `gh issue view <m> --json state`
  - Cross-repo: `gh issue view <m> -R aangwijaya/talent-scout-client --json state`
- No issue can be picked → stop and report.

## 2. Start
```bash
git rev-parse --git-dir   # MUST contain /worktrees/ — if not: STOP, ask the human to create a worktree
gh issue edit <n> --remove-label status:todo --add-label status:in-progress
git fetch origin
git branch --show-current # must be feat/<n>-<slug>; empty/new worktree → git switch -c feat/<n>-<slug> origin/main
cp ../../talent-scout-server/.env .env   # if missing; .env is not copied into worktrees
```
- Never create a branch or work in the main checkout (`~/projects/talent-scout/talent-scout-server`); it always stays on `main`.
- 1 issue = 1 worktree (`~/projects/talent-scout/wt/server-<n>`) = 1 branch = 1 PR. The human creates the worktree: `git worktree add ../wt/server-<n> -b feat/<n>-<slug> origin/main` (details: `WORKFLOW.md` §11).
- `fix/<n>-…` for type:bug, `chore/<n>-…` for type:chore.
Read `docs/LESSONS.md` and the documents in the issue's "Docs to read" section.

## 3. Work & check
Green checks (all must pass):
```bash
test -z "$(gofmt -l .)"
go build ./...
go vet ./...
go test ./...
```
Failing → fix. **At most 3 fix attempts.** After 3 failures:
```bash
gh issue edit <n> --remove-label status:in-progress --add-label status:blocked
gh issue comment <n> --body "Blocked: <what was tried>, last error: <short>, suspected cause: <...>"
```
then go back to step 1.

Order inside this step (skills in `.opencode/skills/`):
1. Implement only the acceptance criteria.
2. `verify-this` — only when the issue/PR claims a fix (type:bug, "fixes X"); verdict goes in the PR body.
3. `deslop` — clean AI slop from the branch diff.
4. Green checks above.
5. `thermo-nuclear-review` then `thermo-nuclear-code-quality-review` — **report-only, once per PR**, no review→edit loop (token cost). Fix only High findings / presumptive blockers, re-run the green checks; list the rest under "Review findings (not fixed)" in the PR body.

All fix attempts in this step share the **max 3** limit.

## 4. Hand off
```bash
git push -u origin HEAD
cp .github/pull_request_template.md /tmp/pr-<n>.md   # fill in the template; must include "Closes #<n>"
gh pr create --base main --title "feat: <short> (#<n>)" --body-file /tmp/pr-<n>.md
gh issue edit <n> --remove-label status:in-progress --add-label status:review
```
- Then `loop-on-ci` (+ `fix-ci`) until all PR checks are green — active once CI exists (server #2); before that the local green checks are the gate. Max 3 fix attempts, then `status:blocked`.
- After the human reviews: `get-pr-comments` → fix → green checks → push (no force) → `loop-on-ci` again. The human merges.
- Don't merge, don't force-push, don't push to `main`, don't close the issue.
- Touch `docs/API.md`/`EVENTS.md`/`PRD.md` only when the issue is labeled `contract-change` and approved.

## 5. Learn
Add an entry to `docs/LESSONS.md` (format in that file) when there is a surprise, a repeated mistake, a decision, or a review/CI finding that recurs. The same lesson already exists once → open a separate PR labeled `skill-update`.

Restart the session every 2–4 issues. After the PR is merged, the human removes the worktree (`git worktree remove`, `git branch -d`, `git worktree prune`); next issue = new worktree.
