# AGENTS.md — Brief for OpenCode (Go Backend)

## Project Summary
Talent Scout: recruiter-side AI screening. Applicant applies (name, email, CV PDF) → single-use session link (7 days; MVP: `EMAIL_MODE=log`, the recruiter copies the link from the dashboard) → ~15-minute screening chat with a multi-agent graph (planner → interviewer ⇄ judge → guard → scorer ⇄ evidence_check → recruiter_gate) → the recruiter decides. This repo = **Go server**. The SvelteKit client lives in a separate repo and is built against the same contract.

## Read First (mandatory, before writing code)
1. `docs/PRD.md` — what & why
2. `docs/ARCHITECTURE.md` — graph, nodes, loops, data model, token lifecycle
3. `docs/API.md` — **REST contract**
4. `docs/EVENTS.md` — **SSE contract**
5. `docs/TECH-STACK.md` — libraries, env vars, model config
6. `PLAN.md` — 7-day checklist
7. `docs/LESSONS.md` — lessons from previous issues (read before starting a new issue)
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
│   ├── email/                    # Sender interface: log (default), resend (optional)
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
- All graph events: persist to `node_events` **first**, then publish to the bus.
- Code nodes (`plan_check`, `guard`, `evidence_check`) **must not** call the LLM.
- Prompts are stored as templates, not long strings inside functions.
- Small commits, Conventional Commits messages (`feat:`, `fix:`, `test:`, `docs:`, `chore:`).

## Rules
1. **Follow the contract.** Don't add/change endpoints, fields, or event names. Changes to `docs/API.md`/`docs/EVENTS.md`/`docs/PRD.md` only go through a `contract-change` issue approved by a human (an "approved" comment from the repo owner); its PR is marked "CONTRACT CHANGE" and the client copy is synced byte-identical.
2. **Tests are mandatory.** Every handler & node has tests. `go test ./...` uses the in-memory store, fake storage, `LLM_MODE=fake`, `EMAIL_MODE=log` — no network. Postgres store integration tests are skipped when `TEST_DATABASE_URL` is empty (`test` schema in the same Supabase project; see `docs/TECH-STACK.md` §7).
3. **No secrets in the repo.** Only `.env.example`. `.env` is in `.gitignore`. Never print `DATABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY`, or API keys to the log.
4. **RLS is mandatory.** Every table (including `goose_db_version`) gets `ENABLE ROW LEVEL SECURITY` in the migration that creates it, **with no policy**, and `anon`/`authenticated` privileges revoked. Don't use `FORCE ROW LEVEL SECURITY` (backend = owner, must bypass). Never create a public policy. Bucket `cvs` is private; the service role key lives only on the server.
5. **Session tokens:** store only the SHA-256 hash; the raw token only appears in the email (`resend`) or in the log + `resend-invite` response (`EMAIL_MODE=log`).
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

## Tasks (7 days)
Tasks are **no longer** listed here. Source of tasks = GitHub issues in this repo labeled `agent:opencode` (order & dependencies via "Blocked by #N", day via the `day:N` label). A per-day summary is in `PLAN.md`.

## Agentic Workflow (mandatory)
Supporting skills live in `.opencode/skills/` (loaded with the `skill` tool): `issue-workflow`, `add-endpoint-from-contract`, `migration-with-rls`. Review & CI skills (adapted from cursor/plugins, MIT, see `LICENSE-THIRD-PARTY.md` there): `verify-this`, `deslop`, `thermo-nuclear-review`, `thermo-nuclear-code-quality-review`, `loop-on-ci`, `fix-ci`, `get-pr-comments` (order in the work loop below). Backend reference skills (load only when the task matches; licenses in the same file): `supabase-postgres-best-practices` (query/index/pool review, new migration), `go-error-handling`, `go-context`, `go-concurrency`, `go-testing`, `systematic-debugging` (only after the first failed fix attempt or an unexplained failure).

**Labels:** `agent:opencode` (your tasks) · `agent:claude` (client) · `agent:human` (repo owner) · `status:todo` → `status:in-progress` → `status:review` · `status:blocked` · `contract-change` · `skill-update` · `type:feat|bug|chore` · `day:1`…`day:7`.

**Work loop (repeat until no issues remain):**
1. Pick a `status:todo` + `agent:opencode` issue with the lowest `day:N` whose "Blocked by" issues are all **closed** (`gh issue list --label agent:opencode --label status:todo --state open`, then `gh issue view <n>` to check blockers). Cross-repo blockers (`aangwijaya/talent-scout-client#N`) are checked with `gh issue view N -R aangwijaya/talent-scout-client`.
2. Switch the label to `status:in-progress` (`gh issue edit <n> --remove-label status:todo --add-label status:in-progress`).
3. Work **only in a git worktree** (see `WORKFLOW.md` §11): check that `git rev-parse --git-dir` contains `/worktrees/`. If not → stop and ask a human to run `git worktree add ../wt/server-<n> -b feat/<n>-<slug> origin/main`. Don't create branches in the main checkout. 1 issue = 1 worktree = 1 branch = 1 PR. If the worktree has no branch yet, create it from `origin/main`: `feat/<n>-<slug>` (`fix/…` for bugs, `chore/…` for chores).
4. Implement only the acceptance criteria. Claiming a fix (type:bug, "fixes X") → run `verify-this` and put the verdict in the PR body.
5. Run `deslop` on the branch diff, then the green checks: `gofmt -l .` empty, `go build ./...`, `go vet ./...`, `go test ./...`.
6. Run `thermo-nuclear-review` + `thermo-nuclear-code-quality-review` — **report-only, once per PR** (no review→edit loop; token cost). Fix only High findings / presumptive blockers and re-run the green checks; list the rest in the PR body under "Review findings (not fixed)".
7. Failing (checks or blocker fixes) → **max 3 attempts** in total. Still failing → label `status:blocked`, comment on the issue (what was tried, the last error, the suspected cause), push a WIP branch if useful, move to the next issue.
8. Green → push, `gh pr create` with the template and a `Closes #<n>` line; switch the issue label to `status:review`.
9. `loop-on-ci` (+ `fix-ci`) until all PR checks are green — active once CI exists (server #2); same max-3 rule.
10. Human review → `get-pr-comments` → fix → green checks → push (no force) → back to step 9. The human merges.
11. Add an entry to `docs/LESSONS.md` if there was a lesson (recurring review/CI findings included), then go back to step 1.

**Limits:** never merge a PR, never force-push, never push directly to `main`; don't edit contract documents without an approved `contract-change` issue; don't close issues yourself (they close automatically when a human merges the PR); don't create new feature issues (you may create a `type:bug` or `contract-change` issue and stop to wait for approval). Restart the OpenCode session every 2–4 issues to keep the context clean (start the new session by reading `AGENTS.md` + `docs/LESSONS.md`). All GitHub operations go through the `gh` CLI.

**Learning loop:** the same lesson appearing 2× in `docs/LESSONS.md` → open a separate PR labeled `skill-update` that updates the skill in `.opencode/skills/` or the rules in this file. Prefer turning lessons into tests or CI checks over text. A human merges.
