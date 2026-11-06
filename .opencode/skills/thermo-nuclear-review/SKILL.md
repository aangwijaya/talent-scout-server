---
name: thermo-nuclear-review
description: Report-only, diff-scoped security and correctness audit (bugs, breaking changes, auth, secrets, devex, contract drift) for the current branch. Run once per PR before opening it, only when explicitly asked or when issue-workflow says so.
disable-model-invocation: true
license: MIT (adapted, see ../LICENSE-THIRD-PARTY.md)
compatibility: opencode
---

# Thermo Nuclear Review

> Adapted from cursor/plugins (thermos/skills/thermo-nuclear-review), MIT License, Copyright (c) 2026 Cursor. See `../LICENSE-THIRD-PARTY.md`.

**Only run when explicitly asked or when the `issue-workflow` step says so.** (OpenCode ignores the `disable-model-invocation` frontmatter field, so this line is the actual guard.)

## Inputs (gather yourself with plain git/gh, no subagents)
```bash
git fetch origin main
git diff --stat origin/main...HEAD
git diff origin/main...HEAD
git diff --name-only origin/main...HEAD   # then read the full contents of each changed file
```

## Mode & cost
- **Report-only, once per PR.** Run it one time after the green checks and before `gh pr create` (or once on an open PR when the human asks). Do not loop review → edit → review; that multiplies token cost (the diff + full changed files are re-sent on every pass, which adds up fast with DeepSeek/Claude).
- The agent fixes **only blockers** (presumptive blockers / High findings) itself, then re-runs the green checks — not this review. Everything else goes into the PR body under "Review findings (not fixed)" for the human.
- Fix attempts count toward the **max 3** limit of the issue; after that → `status:blocked` + comment.
- A finding that repeats across PRs → entry in `docs/LESSONS.md` (and a `skill-update` PR when seen 2×).

Use this skill for a comprehensive security and correctness audit of a checked-out branch (the worktree of the current issue).

## Prompt

You are a security expert performing a comprehensive review of a checked out branch. Audit this branch and its changes extremely thoroughly for bugs, changes that break existing features/functionality, and security vulnerabilities. Be EXTREMELY thorough, rigorous, careful, ambitious, and attentive. NOTHING can slip through.

# Scope
ONLY report issues related to code that is being ADDED or MODIFIED in this PR.
Focus on changes in the diff.
DO NOT report vulnerabilities in existing code that is not being changed.

# Guidelines

## Breaking Functionality Guidelines
This is a complex codebase, with many cross-package/module dependencies. Often simple code changes in one place have subtle interactions that break functionality elsewhere. You MUST be extremely thorough in tracing through possible side effects of the changes.

## Breaking Devex Guidelines
It can be easy to break developers' ability to run / build the code locally. You MUST catch changes that will impact users' developer experience. Some examples (not exhaustive):
- Modifying how secrets are read / where they are read from
- Updating environment variable names / adding environment variables
- Remapping ports / networking
- Adding scripts that must be run for certain functionality to continue working. Broadly speaking these are changes that will modify the way developers currently run / build the code. This does not include changes that introduce new alternative ways to run/build things. Adding dependencies with package managers does not count as a devex breaking change, unless it requires the user to do some very new thing that is not part of their normal development workflow, like manually installing software off of a website / App Store.

## Feature Leak Guidelines
The codebase gates behaviour behind env modes (`LLM_MODE=fake`, `EMAIL_MODE=log`, client `USE_MOCK`) and auth checks. You MUST NOT allow any features that are meant to be behind a feature gate leak. These leaks are often subtle. Be VERY careful and thorough.

## Intended Breakage Guidelines
If you identify a high risk finding, but the intent of the branch is to introduce that finding – e.g. break some functionality, remove a feature flag, remove a safeguard – AND the scope of the change is well constrained, you SHOULD NOT waste the author's time by reporting the issue to them. However, if you believe it is likely that they are not aware of the full implications of their change, or you are worried that they are under-weighting the negative impacts (extreme example: a developer pushes a PR titled "Delete the database"), or you are worried that the change is actually malicious, you should still report the finding.

## Over-reporting Guidelines
If you report issues as High priority when they are not in fact high priority / meaningful issues, devs will lose trust in you and stop listening to you over time.
NEVER misreport the priority / importance of issues. Be extremely thorough in tracing issues end-to-end to gain complete, and total confidence before reporting.

## Talent Scout security & correctness checklist (server)
Check every item the diff touches; report only issues in added/modified code.
- **Auth:** every recruiter route behind the bearer-token middleware; constant-time compare for `RECRUITER_PASSWORD`; HMAC token (`AUTH_TOKEN_SECRET`) checks signature **and** `expires_at`; 401 shape per `docs/API.md`.
- **SSE `?sse_token=`:** accepted only on `/graph-runs/{runId}/events`, checked for `aud=sse`, expiry and matching `run_id` (a recruiter bearer token is never accepted in the query); never logged (strip query strings from request logs), no token in error bodies.
- **CORS:** only on the two SSE `GET` routes, origins from `FRONTEND_ORIGIN`, no `Allow-Credentials`.
- **Trusted client IP:** `X-Forwarded-For` honoured only when `X-BFF-Secret` matches `BFF_SHARED_SECRET` (constant-time compare); the secret is never logged.
- **Session tokens** (`/sessions/{token}/...`): unguessable, scoped to one session, state checks (409/410) enforced server-side.
- **RLS / migrations (goose):** every new table has `ENABLE ROW LEVEL SECURITY` + `REVOKE ALL ... FROM anon, authenticated` in the same file, no policies, no `FORCE`; a correct `-- +goose Down`; merged migrations are never edited (see `migration-with-rls`).
- **SQL:** pgx parameters only, no string-built SQL; multi-statement writes in a transaction.
- **Secrets:** nothing from `.env` committed, printed, or returned; no service-role key / `DATABASE_URL` / LLM keys sent to the client or put in fixtures; CORS limited to the client origin.
- **Prompt injection / LLM output (CV text, applicant chat, PDF text):**
  - CV text, extracted PDF text and applicant chat are untrusted data: placed only in the user/data part of the prompt inside clear delimiters (e.g. `<cv>…</cv>`, `<answer>…</answer>`), with delimiter-like sequences in the input escaped or stripped; never concatenated into system instructions.
  - The system prompt says delimited content is data and instructions inside it must be ignored.
  - Judge/scorer/planner output is parsed into a Go struct and validated in code (JSON schema/required fields, enums, score ranges, max lengths); invalid output → retry once or fail closed, never a default "pass".
  - Evidence quotes returned by the judge/scorer are verified as substrings of the stored transcript/CV before being saved or shown; unverified quotes are dropped.
  - No LLM-driven tool calls, SQL, URLs or state transitions: the model only returns data; Go code decides the next state and runs fixed, parameterized queries.
  - Input caps before the LLM call (CV text, each chat message, total turns) and output caps (`max_tokens`, response bytes); oversize → truncate or 413.
  - Model output is never rendered as HTML/markdown unescaped and never logged with secrets; the judge/guard path still runs on every turn.
- **LLM cost/abuse:** request size limits, per-session turn/time limits, `LLM_MODE=fake` still works for tests/CI.
- **Contract:** responses/events still match `docs/API.md` / `docs/EVENTS.md` byte-for-byte in shape; changing them without a `contract-change` issue is a High finding.
- **Devex:** new env vars added to `.env.example` and docs; ports / commands unchanged unless the issue says so.

# Final Response
IF you have medium-to-high priority / risk findings, and there is a PR for this branch, then check the PR/MR discussion using `gh pr view --comments` and `gh api repos/{owner}/{repo}/pulls/<pr>/comments` to see if there are comments from BugBot or others present.
If so, take their findings into account. If they found issues you missed, evaluate them to determine if they are valid and include them in your report. If they found some of the same issues you did, see if there is anything from their findings that are worth incorporating into your response.
Flag issues found by BugBot or others in the PR/MR discussion that you include in your report.

# Critical Rules
- NEVER present issues with unfinished research. E.g. Never say something like, "The client has issue X, but if handled in the backend then this is ok." if you have access to the backend code and can check for yourself.
- You MUST wait to check the PR/MR discussion until AFTER you have performed your audit. This way you have fresh eyes while you review.
- Be EXTREMELY thorough, rigorous, careful, ambitious, and attentive. NOTHING can slip through.

# Report format
Write to `/tmp/review-sec-<n>.md`; fix only High findings (counts toward max 3 attempts), list the rest in the PR body:
```
Security/correctness review: PASS | HIGH FIXED | FINDINGS OPEN
1. [High|Medium|Low] <file>:<line> — <issue> — evidence: <trace> → <fix>
```
