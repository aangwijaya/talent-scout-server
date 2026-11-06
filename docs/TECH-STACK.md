# Talent Scout — Tech Stack

## 1. Choices & Reasons

| Layer | Choice | Reason |
|---|---|---|
| Client | SvelteKit + TypeScript, server-side (BFF) on `@sveltejs/adapter-vercel` (SSR, loads/actions call Go server-to-server; design in the client's `docs/SERVER-SIDE.md`) | The recruiter token stays in an httpOnly cookie (never in browser JS), env is private, one place for auth/errors. Only SSE goes browser → Go directly. Fits Vercel Hobby (300 s, 4.5 MB) because SSE is never proxied. |
| Client hosting | Vercel Hobby (free) | Free, auto-deploy from GitHub, preview per PR. Note: Hobby is for non-commercial use (fits a portfolio). |
| Server | Go (≥ 1.23), `net/http` + `chi` router | Single binary, natural graph concurrency (goroutines), small memory → cheap on Railway. |
| Server hosting | Railway Hobby ($5/month, includes $5 usage) | Deploy from GitHub (Dockerfile), env vars. No volume (state in Supabase). |
| Database | **Supabase Postgres (free)**, driver `jackc/pgx/v5` (`pgxpool`) via **Supavisor session pooler** | Free managed Postgres, dashboard for inspecting data during the demo. |
| Migrations | `pressly/goose` (SQL files, embedded) Postgres dialect, run at startup / `cmd/migrate` | Simple, versions recorded. |
| CV files | **Supabase Storage**, private bucket `cvs`, object `{application_id}.pdf`; extracted text in column `applications.cv_text` | Free (1 GB). The backend uploads/downloads with the service role key; the client never touches Supabase. |
| PDF text | `github.com/ledongthuc/pdf` | Pure Go. Scanned (image) PDFs are out of scope. |
| LLM gateway | OpenRouter (OpenAI-compatible `/chat/completions`) | One key, many models, model per node. The base URL can be switched to Ollama **after the MVP**. |
| Email | **Default `EMAIL_MODE=log`** (link printed to the log + shown in the dashboard). Resend (free: 3,000/month, 100/day) **optional** | Without a verified domain Resend can only send to the account owner's email (see §6), so email is not required for the 1-week MVP. |
| Realtime | Server-Sent Events (SSE) from Go | One-way server→client is enough; no Supabase Realtime (the client doesn't talk to Supabase). |
| Contract | `docs/API.md` → `api/openapi.yaml` (Day 1) | Contract-first; conformance tests (server) and TS types (client, `openapi-typescript`). |
| Test | Go `testing` + `httptest`; client `svelte-check` + **1 Playwright smoke test** (mock) | Green checks that code can evaluate (details §7). |

## 2. Supabase: Security Rules (mandatory)

- **RLS enabled on every table from the first migration, with no public policies at all.** Supabase exposes the `public` schema through the Data API (PostgREST) to the `anon`/`authenticated` roles; Security Advisor flags tables without RLS as **critical** because anyone with the project URL + anon key can read/change/delete data. RLS on + zero policies = public roles can access nothing.
- The backend connects as the `postgres` role (table owner). The table owner bypasses RLS as long as `FORCE ROW LEVEL SECURITY` is **not** used, so the backend works normally.
- Extra defense-in-depth in the first migration: revoke all table/sequence privileges in `public` from `anon` and `authenticated`, including default privileges for new tables.
- The goose version table (`goose_db_version`) is also in `public` → enable RLS on it too (or put the goose table in a non-public schema). The Advisor must be clean (0 RLS findings) before deploy.
- Bucket `cvs` is **private**, with no public storage policy. Upload/download only from the backend with `SUPABASE_SERVICE_ROLE_KEY`. The recruiter downloads CVs through a Go endpoint (`GET /api/v1/applications/{applicationId}/cv`) that proxies the file — no signed URL is sent to the client.
- The anon key & service role key **never** appear in the client repo or in Vercel env.

## 3. Database Connection

- `DATABASE_URL` = **session pooler** URL from Supabase (Connect → Session pooler), format: `postgresql://postgres.<project_ref>:<password>@aws-0-<region>.pooler.supabase.com:5432/postgres?sslmode=require`.
- Why the session pooler (port 5432): IPv4-compatible (the Supabase free direct connection is IPv6 only, and Railway outbound IPv6 is not guaranteed) and supports pgx prepared statements. **Don't** use the transaction pooler (6543) unless pgx is set to the simple protocol.
- `pgxpool` max 5 connections (the free-tier pooler limit is small).
- goose migrations use the same URL.

## 4. Cost Estimate

| Item | Cost |
|---|---|
| Vercel Hobby (client) | $0 |
| Railway Hobby (Go server, no volume) | $5/month (usage usually < $5 credit) |
| Supabase Free (Postgres 500 MB, Storage 1 GB) | $0 |
| Resend free (optional) | $0 |
| Custom domain (optional, for Resend to anyone + `apply.` subdomain) | ~$10–15/year, optional |
| LLM per session (estimate, see §8) | ~$0.003–0.008 |
| 100 demo sessions | < $1 |
| OpenCode (coding agent, separate key) | capped by credit limit, e.g. $3 |

**Supabase free caveat:** the project is **paused automatically after 7 days of inactivity**. Before a demo/interview: open the Supabase dashboard and make sure the status is *Active* (restore if paused, takes a few minutes), then run 1 quick flow. Optional: a weekly ping to `GET /api/v1/health?deep=1` (DB query) to keep it active.

## 5. Environment Variables

### Server (Railway Variables / local `.env`)

| Name | Example | Notes |
|---|---|---|
| `PORT` | `8080` | Set automatically by Railway. |
| `APP_ENV` | `development` \| `production` | |
| `DATABASE_URL` | session pooler URL (§3) | Secret. |
| `SUPABASE_URL` | `https://<project_ref>.supabase.co` | For the Storage API. |
| `SUPABASE_SERVICE_ROLE_KEY` | (secret) | **Server only.** Never log it. |
| `SUPABASE_STORAGE_BUCKET` | `cvs` | Private bucket. |
| `PUBLIC_APP_URL` | `https://talentscout.vercel.app` | Base for email links (`${PUBLIC_APP_URL}/s/<token>`) and `apply_url` (`${PUBLIC_APP_URL}/apply/<jobId>`). |
| `FRONTEND_ORIGIN` | `https://talentscout.vercel.app,http://localhost:5173` | Allowed CORS origins for the two SSE `GET` routes only (REST is server-to-server), comma-separated. No wildcard. |
| `BFF_SHARED_SECRET` | (secret, 32+ bytes) | Must equal the client's `BFF_SHARED_SECRET`. Only when `X-BFF-Secret` matches (constant-time) is `X-Forwarded-For` trusted as the applicant IP for the apply rate limit. Never log it. |
| `RECRUITER_PASSWORD` | (secret) | Single recruiter password (the only recruiter auth in the MVP). |
| `AUTH_TOKEN_SECRET` | (secret, 32+ bytes) | HMAC for the recruiter bearer token. |
| `LLM_MODE` | `openrouter` \| `fake` | `fake` = deterministic responses for tests/e2e. |
| `LLM_BASE_URL` | `https://openrouter.ai/api/v1` | Later can be `http://localhost:11434/v1` (Ollama, post-MVP). |
| `OPENROUTER_API_KEY` | (secret) | **Key #2 `talent-scout-app`**, not the OpenCode key. |
| `MODELS_CONFIG_PATH` | `./config/models.yaml` | |
| `EMAIL_MODE` | `log` (default) \| `resend` | `log` = link printed to the log and returned by `resend-invite` for the dashboard. `resend` is optional. |
| `RESEND_API_KEY` | (secret) | Only when `EMAIL_MODE=resend`. |
| `EMAIL_FROM` | `Talent Scout <noreply@talentscout.com>` | Without a domain: `onboarding@resend.dev` (only to the account owner's email). |
| `SESSION_TTL_HOURS` | `168` | 7 days. |
| `SESSION_MAX_MINUTES` | `15` | |
| `SESSION_MAX_QUESTIONS` | `15` | |
| `TEST_DATABASE_URL` | session pooler URL + `search_path=test` | Optional, only for store integration tests (§7). |

### Client (Vercel Environment Variables / local `.env`, all private server-side via `$env/dynamic/private`)

| Name | Example |
|---|---|
| `API_BASE_URL` | `https://talent-scout-api.up.railway.app` (also returned to the browser by page loads as the SSE base URL) |
| `USE_MOCK` | `true` \| `false` |
| `BFF_SHARED_SECRET` | (secret, same value as the server) |

No `PUBLIC_*` variables.

All secrets live only in `.env` (gitignored), Railway Variables, or Vercel env. Each repo must have a `.env.example` without secret values.

## 6. Deploy Topology & Domain

```mermaid
flowchart LR
  A[Applicant / recruiter browser] -->|HTTPS pages, form actions, cookie| VC[Vercel Hobby<br/>talent-scout-client SvelteKit SSR<br/>talentscout.vercel.app]
  VC -->|REST server-to-server<br/>Bearer, X-Forwarded-For + X-BFF-Secret| RW[Railway<br/>talent-scout-server Go]
  A -->|SSE only, CORS GET<br/>sse_token / path token| RW
  RW -->|pgx · session pooler| PG[(Supabase Postgres<br/>RLS on, no public policy)]
  RW -->|Storage API · service role| ST[(Supabase Storage<br/>private bucket cvs)]
  RW -->|chat/completions| OR[OpenRouter]
  RW -.->|send email (optional)| RS[Resend]
  RS -.->|link /s/token| A
```

- **One frontend app, routed by path:** `/dashboard/...` (recruiter), `/apply/[jobId]` (apply form), `/s/[token]` (AI session).
- Default domain: `talentscout.vercel.app` (or another name still available when creating the Vercel project; adjust `PUBLIC_APP_URL` & `FRONTEND_ORIGIN`).
- Vercel's free subdomain is single-level, so `apply.talentscout.vercel.app` is **not possible**.
- Optional later: buy a domain (e.g. `talentscout.com`), point the root and `apply.` to the same Vercel project, and verify that domain in Resend → email can be sent to any applicant. Just change `PUBLIC_APP_URL`, `FRONTEND_ORIGIN`, `EMAIL_FROM`; no code changes.

## 7. Local Development & Tests

- **Local (simplest):** the local server uses **the same Supabase project** via `DATABASE_URL` in `.env`. Optional: `supabase start` (needs Docker) for local Postgres + Storage — not required.
- **`go test ./...` (the main green check) needs no DB:** handlers & nodes are tested with the in-memory store (an implementation of the `store` interface) + `LLM_MODE=fake` + `EMAIL_MODE=log` + fake storage. Always runs without network.
- **Store integration tests (real Postgres):** use a **separate `test` schema in the same Supabase project** (`TEST_DATABASE_URL` with `search_path=test`). Tests are skipped automatically when `TEST_DATABASE_URL` is empty; each run resets the `test` schema and then runs the goose migrations. Chosen because it needs no installs (no Docker). Testcontainers = optional, not used in the MVP.
- Migrations also enable RLS in the `test` schema.

## 8. Model per Node (`config/models.yaml`)

OpenRouter prices per 1M tokens (input/output), checked 2026-11-02:

| Node | Model | Price |
|---|---|---|
| `planner` | `deepseek/deepseek-v4.1-flash` | $0.045 / $0.5 |
| `interviewer` | `z-ai/glm-5.3-flash` | $0.029 / $0.092 |
| `judge` | `deepseek/deepseek-v4.1-flash` | $0.045 / $0.5 |
| `scorer` | `deepseek/deepseek-v4.1-flash` | $0.045 / $0.5 |

Model fallback (e.g. `:free`) = **post-MVP**; in the MVP each node uses one model.

Config shape (contract, not implementation):

```yaml
default:
  temperature: 0.3
  timeout_seconds: 30
  max_retries_on_http_error: 1   # retry the same model, no fallback
nodes:
  planner:     { model: deepseek/deepseek-v4.1-flash, temperature: 0.2, max_tokens: 800 }
  interviewer: { model: z-ai/glm-5.3-flash,           temperature: 0.6, max_tokens: 300, stream: true }
  judge:       { model: deepseek/deepseek-v4.1-flash, temperature: 0.0, max_tokens: 200 }
  scorer:      { model: deepseek/deepseek-v4.1-flash, temperature: 0.0, max_tokens: 1200 }
```

Note: model prices & availability can change — recheck at openrouter.ai/models before deploy. Estimate per session: ~25–35 LLM calls, ~60k input tokens, ~8k output tokens → ~$0.005.
