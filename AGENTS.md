# AGENTS.md — Brief for OpenCode (Go Backend)

## Project Summary
Talent Scout: recruiter-side AI screening. Applicant applies (name, email, CV PDF) → single-use session link (7 days; MVP: `EMAIL_MODE=log`, the recruiter copies the link from the dashboard) → ~15-minute screening chat with a multi-agent graph (planner → interviewer ⇄ judge → guard → scorer ⇄ evidence_check → recruiter_gate) → the recruiter decides. This repo = **Go server**. The SvelteKit client lives in a separate repo and is built against the same contract.

## Read First (mandatory, before writing code)
1. `docs/PRD.md` — what & why
2. `docs/ARCHITECTURE.md` — graph, nodes, loops, data model, token lifecycle
3. `docs/API.md` — **REST contract**
4. `docs/EVENTS.md` — **SSE contract**
5. `docs/TECH-STACK.md` — libraries, env vars, model config
6. `PLAN.md` — scope summary; the order of work comes from the graph, not this file
7. `docs/LESSONS.md` — lessons from previous issues (read before starting a new node)
8. The GitHub issue being worked on (`gh issue view <n>`) — the source of the task

## Folder Structure (proposed)
```
talent-scout-server/
├── cmd/server/main.go            # wiring: config, db, router, runner, sweeper
├── cmd/migrate/main.go           # goose up/down/status against DATABASE_URL
├── cmd/seed/main.go              # create 1 demo job from config/seed-job.yaml (idempotent)
├── internal/
│   ├── config/                   # env + models.yaml
│   ├── http/                     # router (chi), handlers, middleware (auth, cors, ratelimit), error writer
│   ├── store/                    # store interface + Postgres impl (pgx) + in-memory impl (tests)
│   │   └── migrations/           # goose SQL (embedded); 00001_init.sql = all tables + RLS
│   ├── storage/                  # CVStorage interface: Supabase Storage + fake
│   ├── domain/                   # types & enums: Job, Application, Session, Topic, Message, Score
│   ├── session/                  # token generate/hash, lifecycle, sweeper
│   ├── cv/                       # store PDF + extract text
│   ├── email/                    # Mailer interface: log (default), gmail (optional)
│   ├── llm/                      # OpenAI-compatible client, streaming, 1× HTTP retry, fake (no model fallback)
│   ├── graph/                    # runner, node & edge definitions, event bus
│   │   └── nodes/                # planner, plan_check, interviewer, judge, guard, scorer, evidence_check
│   ├── prompts/                  # prompt templates (.tmpl, embedded)
│   └── sse/                      # writer, replay from node_events, heartbeat
├── config/models.yaml
├── config/seed-job.yaml          # demo job + rubric (MVP: the only job)
├── api/openapi.yaml              # generated on Day 1 from docs/API.md
├── testdata/                     # sample CV PDFs, fake LLM fixtures, example transcripts
├── docs/                         # PRD, TECH-STACK, ARCHITECTURE, API, EVENTS, LESSONS
├── .opencode/skills/             # OpenCode skills: issue-workflow, add-endpoint-from-contract, migration-with-rls + review/CI: verify-this, deslop, thermo-nuclear-review, thermo-nuclear-code-quality-review, loop-on-ci, fix-ci, get-pr-comments + backend: supabase-postgres-best-practices, go-error-handling, go-context, go-concurrency, go-testing, systematic-debugging
├── .github/                      # ISSUE_TEMPLATE/task.md, pull_request_template.md, workflows/ci.yml
├── .env.example
└── Dockerfile                    # multi-stage, static binary
```

## Conventions
- Go ≥ 1.23, clean `gofmt`/`go vet`. Router `chi`, Postgres `jackc/pgx/v5` (`pgxpool`, max 5 connections) via the Supabase **session pooler** (`DATABASE_URL`), `goose` migrations (postgres dialect), logging with `log/slog` (JSON in production).
- The client is a server-side SvelteKit app (BFF): REST is called server-to-server, so **CORS applies only to the two SSE `GET` routes** (`/graph-runs/{runId}/events`, `/sessions/{token}/events`), origins from `FRONTEND_ORIGIN` (comma-separated, no wildcard, no `Allow-Credentials`). Recruiter bearer token: `Authorization` header only; run SSE uses the 60 s `?sse_token=` (`POST /auth/sse-token`). Never log query strings.
- `X-Forwarded-For` is trusted (for the apply rate limit) only when `X-BFF-Secret` matches `BFF_SHARED_SECRET` (constant-time compare); otherwise use the TCP peer address. Never log the secret. All public URLs are built from `PUBLIC_APP_URL` (`/apply/{jobId}`, `/s/{token}`).
- HTTP errors always use the format `{"error":{"code","message","details"}}` with codes from `docs/API.md`.
- JSON fields `snake_case`, UUID IDs (`gen_random_uuid()`), times `timestamptz` → ISO-8601 UTC, structured data `jsonb`.
- All LLM access goes through the interface in `internal/llm`; nodes never call HTTP directly.
- All email goes through the `internal/email` interface: `log` (default) or `gmail` (Gmail API, OAuth2 refresh token; no SMTP).
- All graph events except `message.delta`: persist to `node_events` **first**, then publish to the bus.
- Code nodes (`plan_check`, `guard`, `evidence_check`) **must not** call the LLM.
- Prompts are stored as templates, not long strings inside functions.
- Small commits, Conventional Commits messages (`feat:`, `fix:`, `test:`, `docs:`, `chore:`).

## Rules
1. **Follow the contract.** Don't add/change endpoints, fields, or event names. Changes to `docs/API.md`/`docs/EVENTS.md`/`docs/PRD.md` only go through a `contract-change` issue approved by a human (an "approved" comment from the repo owner); its PR is marked "CONTRACT CHANGE" and the client copy is synced byte-identical.
2. **Tests are mandatory.** Every handler & node has tests. `go test ./...` uses the in-memory store, fake storage, `LLM_MODE=fake`, `EMAIL_MODE=log` — no network. Postgres store integration tests are skipped when `TEST_DATABASE_URL` is empty (`test` schema in the same Supabase project; see `docs/TECH-STACK.md` §7).
3. **No secrets in the repo.** Only `.env.example`. `.env` is in `.gitignore`. Never print `DATABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`, or API keys to the log.
4. **RLS is mandatory.** Every table (including `goose_db_version`) gets `ENABLE ROW LEVEL SECURITY` in the migration that creates it, **with no policy**, and `anon`/`authenticated` privileges revoked. Don't use `FORCE ROW LEVEL SECURITY` (backend = owner, must bypass). Never create a public policy. Bucket `cvs` is private; the service role key lives only on the server.
5. **Session tokens:** store only the SHA-256 hash; the raw token only appears in the email (`gmail`) or in the log + `resend-invite` response (`EMAIL_MODE=log`).
6. **Loop limits** follow `docs/ARCHITECTURE.md` §3 — constants/config, not scattered numbers.
7. Don't add large dependencies without justification in the PR. No ORM.
8. Out of scope: multi-tenant, applicant accounts, Ollama, payments, OCR. Post-MVP (don't build): `POST/PATCH /jobs`, model fallback, advanced retries, resuming the runner after restart.

## Definition of Done (per task)
- [ ] `go vet ./...` and `go test ./...` green
- [ ] Contract conformance tests pass (responses validated against `api/openapi.yaml`, e.g. with `kin-openapi`)
- [ ] Errors follow the contract format & codes
- [ ] Emitted events match `docs/EVENTS.md` (name, shape, ordered `seq`)
- [ ] No secrets/credentials in the diff
- [ ] New tables (if any) have RLS on with no policy; Supabase Security Advisor clean
- [ ] All acceptance criteria in the issue checked
- [ ] New lessons (if any) added to `docs/LESSONS.md`

## How work runs (mandatory)
Work is scheduled by the control plane in `aangwijaya/talent-scout` (`WORKFLOW.md`, `ops/`), not by picking issues by hand. You receive one node: a GitHub issue with a `fence`, `blocked_by`, and `checks` block. `ops/brief server#<n>` prints the exact context, including the previous session's handoff.

**Order:** SCOPE (confirm dependencies exist) → RED (write checks, watch them fail) → GREEN → HANDOFF. Stay inside the fence. When docs and the installed framework disagree, the installed code and types win.

**Findings:** fix it here if it is inside the fence and blocking. Outside the fence and blocking: create a `type:gap` issue with a node block, set `STATUS: gap`, and stop. Not blocking: comment the exact fix on the issue that owns it, or create a `type:gap` issue if none does. Needs a decision: `STATUS: needs_human`. Nothing goes in a PR body as "not fixed".

**Walls:** the runner pushes, opens PRs, and merges; these commands are denied to you. Don't edit contract documents without an approved `contract-change` issue. Don't use production credentials.

**Skills:** `add-endpoint-from-contract`, `migration-with-rls`, and the backend reference skills when the task matches, `systematic-debugging` after a failed attempt, `verify-this` when claiming a fix, `deslop` before committing. The `thermo-nuclear-*` reviews run in a separate session, not by the author.

**Learning:** add an entry to `docs/LESSONS.md` when there is a real lesson. Repeats become `skill-update` nodes through `ops/triage`. Prefer a test, lint rule, or CI check over text.
