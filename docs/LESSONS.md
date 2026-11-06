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

### 2026-11-06 — #22 Contract sweep: gmail + failure/replay/event sequence
- **Context:** Applying the approved contract-change proposals across `docs/API.md`, `docs/EVENTS.md`, `docs/PRD.md`, the supporting docs, and the affected server issues.
- **Problem:** The email channel had drifted (`resend` vs `gmail`) across docs and issues, and the normative event example omitted node lifecycles and edge traversals.
- **Cause:** Contract decisions were applied locally without a repo-wide sweep or one normative example.
- **Lesson:** For a contract-change issue, grep the old term across all docs/issues and update the normative example in the same PR; supporting docs and issue acceptance criteria are part of the contract surface.
- **Follow-up:** none
- **Repeated?:** first time
