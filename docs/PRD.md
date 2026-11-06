# Talent Scout — PRD (MVP, 1-week build)

## 1. Problem

Recruiters spend time on repetitive initial screening: reading CVs, making calls, asking the same questions. The results are often subjective and undocumented. Talent Scout runs a **short AI screening chat** whose questions adapt to the job description, the CV, and the applicant's answers, then produces rubric scores **with evidence quotes** from the applicant's answers. The final decision stays with a human.

## 2. Users

One web app, routed by path: recruiter at `/dashboard/...`, apply form at `/apply/{jobId}`, applicant AI session at `/s/{token}`.


| Role | Needs |
|---|---|
| **Recruiter** (1 person, single-tenant) | See the demo job (1 seeded job with its rubric), see applicants, read transcripts, scores & evidence, decide shortlist/reject, watch the agent graph run. |
| **Applicant** | Apply quickly (name, email, PDF CV), receive a session link, answer a ~10–15 minute chat without an account. |

## 3. User Flow

### Recruiter
1. Open `/dashboard`, log in with the single password (env `RECRUITER_PASSWORD`) → get a bearer token.
2. The demo job already exists (created by the seed: `title`, `description`, `rubric` with 3–6 criteria). No create/edit job UI in the MVP.
3. Share the public apply link `https://talentscout.vercel.app/apply/{jobId}`.
4. Open the applicant list → status of each. For applicants with status `invited`, click **Get invite link** → copy the session link and send it manually (default `EMAIL_MODE=log`).
5. Open applicant detail: CV text, transcript, topics, per-criterion scores + evidence quotes, graph run (replay/live).
6. Decide **shortlist** or **reject** (+ notes).

### Applicant
1. Open `https://talentscout.vercel.app/apply/{jobId}` → enter name, email, upload CV (PDF ≤ 2 MB) → submit.
2. Receive the link `https://talentscout.vercel.app/s/{token}` (valid for 7 days): by email when Resend is enabled (optional), or from the recruiter who copies it from the dashboard (MVP default).
3. Open the link → intro screen (duration, number of topics, rules) → **Start**.
4. Chat with the AI: 3–5 topics, max 2 follow-ups per topic, 15-minute time limit.
5. Session ends → "Thank you" screen. The link cannot be used again.

## 4. Functional Requirements

| ID | Requirement |
|---|---|
| FR-1 | One demo job is created via seed (`cmd/seed`); the recruiter can list & view job details (read-only). Create/edit job = post-MVP. |
| FR-2 | Public apply page; validates email, name, PDF ≤ 2 MB; one email can apply only once per job. |
| FR-3 | The server extracts CV text from the PDF. |
| FR-4 | The server creates a random session token (one-time, valid for 7 days). Default `EMAIL_MODE=log`: the link is printed to the server log and the recruiter can get it in the dashboard. `EMAIL_MODE=resend` (optional): the link is sent by email. |
| FR-5 | The session link can be reopened (resume) while status is `active` and the time limit has not passed; after `completed`/`expired` it is rejected. |
| FR-6 | The Planner creates 3–5 topics from JD + CV + rubric; each topic maps to ≥ 1 rubric criterion. |
| FR-7 | The Interviewer asks per topic; the Judge decides `sufficient` or `follow_up` (max 2 follow-ups/topic). |
| FR-8 | Guard (code node) stops the session when time ≥ 15 minutes or questions ≥ 15, and ensures all topics are covered before a normal finish. |
| FR-9 | The Scorer gives a 1–5 score per criterion; each score requires ≥ 1 exact quote from the applicant's answers. Evidence Check verifies; failure → retry (max 3) → if it still fails, the criterion is marked `needs_manual`. |
| FR-10 | AI replies are streamed to the applicant via SSE. |
| FR-11 | The recruiter watches the graph run live/replay via SSE (nodes light up according to events). |
| FR-12 | The recruiter submits a `shortlist`/`reject` decision; the final status is stored. |
| FR-13 | The recruiter can get a new invite link ("Get invite link": new token, old token revoked) while the session has not started; in `EMAIL_MODE=log` the link is shown in the dashboard for copying. |

## 5. Non-Functional

- **Cost:** total ≤ $5/month (Railway Hobby; Vercel Hobby, Supabase Free, optional Resend free = $0); LLM < $0.01 per session.
- **Latency:** first token of an AI reply < 3 seconds (p50).
- **Security:** session tokens stored as SHA-256 hashes; secrets only in env; RLS enabled on all Supabase tables with no public policies; CVs in a private Storage bucket, downloadable only by the recruiter through the API; the frontend never talks to Supabase directly; CORS only `FRONTEND_ORIGIN`.
- **Deterministic checks:** all checks in the loop are evaluated by code, not by an LLM.
- **Observability:** every node execution is recorded in `node_events` (short input, output, model, tokens, duration).
- **Testability:** `LLM_MODE=fake` so tests & e2e run at no cost. Client E2E = 1 Playwright smoke test (mock).

## 6. Out of Scope

Multi-tenant/organizations; applicant accounts; auth other than the single recruiter password; Ollama/local LLM (after the MVP); payments; voice/video; non-PDF CVs or scanned PDFs (OCR); email notifications to the recruiter; ATS integration; analytics; Supabase Auth/Realtime or direct Supabase access from the client; a separate subdomain (`apply.`) before a custom domain exists.

### Post-MVP (deliberately cut from the 1-week build)

- Create/edit job UI & endpoints (`POST /jobs`, `PATCH /jobs/{jobId}`, rubric editor); the MVP uses 1 seeded job.
- Resend email as the default + custom domain verification (MVP: `EMAIL_MODE=log`, link copied from the dashboard).
- Fuller recruiter auth (multi-user, refresh tokens, server-side logout).
- Model fallback (`:free`) and advanced retries (regenerate interviewer, retry judge, planner retry > 1). What stays: scorer/evidence check retry max 3 and Judge follow-ups max 2 per topic.
- Automatic runner resume after a server restart.
- Full Playwright suite (MVP: 1 smoke test).
- Dark mode.

## 7. Success Metrics (for the demo)

- End-to-end apply → session link (from the dashboard) → session → score → shortlist works in production.
- 100% of scores have evidence that passes Evidence Check (or are explicitly marked `needs_manual`).
- The graph view shows ≥ 1 visible `loop.retry` (can be triggered with the demo job).
- LLM cost per session is recorded and < $0.01.
- Demo video ≤ 60 seconds; the README can be followed to run locally in < 10 minutes.
