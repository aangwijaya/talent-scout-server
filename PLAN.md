# PLAN — Backend (talent-scout-server), 7 Days

Built by OpenCode + DeepSeek. References: `AGENTS.md`, `docs/*`; rules are in `AGENTS.md`, and the order of work comes from `ops/next` in `aangwijaya/talent-scout`. Each block below = one or more GitHub issues labeled `agent:opencode` + `day:N`; work through issues, not from this list. Tick when the DoD is met.

1-week scope: 1 seeded job (no `POST/PATCH /jobs`), `EMAIL_MODE=log` by default (Gmail optional), 1 recruiter password from env, no model fallback. Kept: evidence check max 3 attempts in total, Judge max 2 follow-ups.

## Day 1 — Contract, Schema, Skeleton
- [ ] `api/openapi.yaml` from `docs/API.md` (OpenAPI 3.1), lint passes
- [ ] `go mod init`, `cmd/server`, env config, `GET /api/v1/health`, error writer, CORS (`FRONTEND_ORIGIN`, SSE `GET` routes only — #20)
- [ ] `.env.example` (all vars in `docs/TECH-STACK.md` §5), `.gitignore` (`.env`), Dockerfile
- [ ] Migration `00001_init.sql` (8 tables, RLS on for all tables + `goose_db_version`, revoke `anon`/`authenticated`) + `cmd/migrate`; Supabase **Advisors → Security** = 0 RLS findings
- [ ] Store interface + pgx impl + in-memory impl
- [ ] `cmd/seed` + `config/seed-job.yaml` (rubric validation per `docs/API.md` "Job Seed"; idempotent)
- [ ] Minimal GitHub Actions CI (`go vet`, `go test`)
- **Done:** `go test ./...` green; health passes conformance

## Day 2 — Apply, Session, Auth, initial Runner
- [ ] `POST /auth/login` (compare `RECRUITER_PASSWORD`, 12-hour HMAC token) + bearer middleware (header only)
- [ ] `GET /jobs`, `GET /jobs/{jobId}`, `GET /jobs/{jobId}/public`
- [ ] `internal/storage` (private bucket `cvs`) + fake; `POST /jobs/{jobId}/applications` (PDF ≤ 2 MB, text extraction), rate limit 5/IP/hour using the trusted client IP (#21: `X-Forwarded-For` only with a valid `X-BFF-Secret`)
- [ ] 32-byte session token (store hash, TTL 168 hours); `email.Mailer` `log` (default) + `gmail` (optional); `GET /sessions/{token}`, `POST /sessions/{token}/start`; expiry sweeper
- [ ] `internal/llm` (chat, stream, 1× HTTP retry, `fake`) + `config/models.yaml`; graph runner + event bus + `node_events` `seq`
- [ ] `planner` + `plan_check` (max 2 attempts total); streaming `interviewer` (stops at 120 words); `POST/GET /sessions/{token}/messages`; `POST /sessions/{token}/retry`; SSE `GET /sessions/{token}/events` (accepts `invited`, replay filter)
- **Done:** tests for apply validation, token lifecycle 404/410/409, event order with `LLM_MODE=fake`

## Day 3 — Judge, Guard, Scorer, Recruiter API
- [ ] `judge` (invalid JSON → default `sufficient`) + `guard` (follow-ups ≤ 2, questions ≤ 15, time ≤ 15 minutes)
- [ ] `scorer` + `evidence_check`; max 3 attempts total → `needs_manual`; events `check.failed` + `loop.retry`; `cost_usd`
- [ ] `GET /jobs/{jobId}/applications`, `GET /applications/{id}`, `/cv`, `/decision`, `/resend-invite` (returns `invite_url` in log mode; accepts `failed` applications)
- [ ] `POST /auth/sse-token` + `sse_token` auth on run SSE + query-string log redaction (#19)
- [ ] `GET /graph-runs/{runId}` + SSE `/graph-runs/{runId}/events?sse_token=` (`Last-Event-ID`/`since_seq` replay, heartbeat)
- [ ] Contract conformance tests for all endpoints
- **Done:** guard table ≥ 10 cases; fake quote → retry → success / 3× failure → `needs_manual`

## Day 4 — Integration & Tuning
- [ ] Run together with the local client (`FRONTEND_ORIGIN` includes `http://localhost:5173`); fix mismatches through `contract-change` issues
- [ ] Prompt tuning with OpenRouter (seed job + 2–3 sample CVs); make sure ≥ 1 `loop.retry` happens naturally
- [ ] Record tuning findings in `docs/LESSONS.md`
- **Done:** local e2e apply → link → session → score → decision, once with `LLM_MODE=fake` and once with OpenRouter

## Day 5 — Railway Deploy + README
- [ ] Service from GitHub (Dockerfile); env vars (`docs/TECH-STACK.md` §5): `DATABASE_URL` = session pooler, `SUPABASE_*`, `PUBLIC_APP_URL`, `FRONTEND_ORIGIN`, `BFF_SHARED_SECRET` (same value as Vercel), `EMAIL_MODE=log`
- [ ] Production migrate + seed; Supabase Advisor clean; `GET /health?deep=1`
- [ ] Server README (summary, graph diagram, how to run, env, costs, model per node)
- **Done:** public `/api/v1/health` OK; 1 real session succeeds

## Day 6 — Demo & Bug Fixes
- [ ] Clean seed data for the demo recording (by you, `agent:human` issue)
- [ ] Bug fixes from staging QA

## Day 7 — Buffer
- [ ] Bug fixes only, no new features

## Post-MVP
`POST/PATCH /jobs`, Gmail as default + domain, model fallback, advanced retries, runner resume after restart.
