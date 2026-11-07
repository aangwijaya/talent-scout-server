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
