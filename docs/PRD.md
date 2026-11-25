# Talent Scout — PRD (MVP, 1-week build)

## 1. Problem

Recruiters spend time on repetitive initial screening: reading CVs, making calls, asking the same questions. The results are often subjective and undocumented. Talent Scout runs a **short AI screening chat** whose questions adapt to the job description, the CV, and the applicant's answers, then produces rubric scores **with evidence quotes** from the applicant's answers. The final decision stays with a human.

## 2. Users

One web app, routed by path: recruiter at `/dashboard/...`, apply form at `/apply/{jobId}`, applicant AI session at `/s/{token}`.


| Role | Needs |
|---|---|
| **Recruiter** (1 person, single-tenant) | Create, edit, open and close jobs with their rubrics, see applicants, read transcripts, scores & evidence, decide shortlist/reject, watch the agent graph run. |
| **Applicant** | Apply quickly (name, email, PDF CV), receive a session link, answer a chat limited by `SESSION_MAX_MINUTES` (default 15 minutes) without an account. |

## 3. User Flow

### Recruiter
1. Open `/dashboard`, log in with the single password (env `RECRUITER_PASSWORD`) → get a bearer token.
2. Create or edit a job (`title`, `description`, `rubric` with 3–6 criteria), optionally starting from an AI-suggested rubric the recruiter edits. The seeded demo job is also available, and jobs can be opened or closed.
3. Share the public apply link `https://talentscout-id.vercel.app/apply/{jobId}`.
4. Open the applicant list → status of each. For applicants with status `invited`, click **Get invite link** → copy the session link and send it manually (default `EMAIL_MODE=log`).
5. Open applicant detail: CV text, transcript, topics, per-criterion scores + evidence quotes, graph run (replay/live).
6. Decide **shortlist** or **reject** (+ notes).

### Applicant
1. Open `https://talentscout-id.vercel.app/apply/{jobId}` → enter name, email, upload CV (PDF ≤ 2 MB) → submit.
2. Receive the link `https://talentscout-id.vercel.app/s/{token}` (valid for 7 days): by email when Gmail is enabled (optional), or from the recruiter who copies it from the dashboard (MVP default).
3. Open the link → intro screen (duration, number of topics, rules) → **Start**.
4. Chat with the AI: 3–5 topics, max 2 follow-ups per topic, time limit `SESSION_MAX_MINUTES` (default 15 minutes). A job that requires spoken English runs a voice interview (FR-16). If an AI step fails temporarily, the applicant can retry that step; if it fails permanently, the session ends as `failed` and the recruiter can issue a new invite.
5. Session ends → "Thank you" screen. The link cannot be used again (a new invite can be issued by the recruiter).

## 4. Functional Requirements

| ID | Requirement |
|---|---|
| FR-1 | The recruiter can create, edit, open and close jobs; an AI-suggested rubric is a draft the recruiter edits. One demo job is also created via seed (`cmd/seed`). |
| FR-2 | Public apply page; validates email, name, PDF ≤ 2 MB; one email can apply only once per job. |
| FR-3 | The server extracts CV text from the PDF. |
| FR-4 | The server creates a random session token (one-time, valid for 7 days). Default `EMAIL_MODE=log`: the link is printed to the server log and the recruiter can get it in the dashboard. `EMAIL_MODE=gmail` (optional): the link is sent via the Gmail API (OAuth2, no SMTP). |
| FR-5 | The session link can be reopened (resume) while status is `active` and the time limit has not passed; after `completed`/`expired` it is rejected. |
| FR-6 | The Planner creates 3–5 topics from JD + CV + rubric; each topic maps to ≥ 1 rubric criterion. |
| FR-7 | The Interviewer asks per topic; the Judge decides `sufficient` or `follow_up` (max 2 follow-ups/topic). |
| FR-8 | Guard (code node) stops the session when time ≥ `SESSION_MAX_MINUTES` (default 15 minutes) or questions ≥ 15, and ensures all topics are covered before a normal finish. |
| FR-9 | The Scorer gives a 1–5 score per criterion; each score requires ≥ 1 exact quote from the applicant's answers. Evidence Check verifies; failure → retry, max 3 attempts in total (initial + 2 retries) → if it still fails, the criterion is marked `needs_manual`. |
| FR-10 | AI replies are streamed to the applicant via SSE. The server stops streaming at the 120-word limit; `message.completed` carries the final text, which the client always renders. |
| FR-11 | The recruiter watches the graph run live/replay via SSE (node lifecycle, `node.skipped`, and edge-traversal events). |
| FR-12 | The recruiter submits a `shortlist`/`reject` decision; the final status is stored. |
| FR-13 | The recruiter can get a new invite link ("Get invite link": new token, old token revoked) while the session has not started or after the application failed; in `EMAIL_MODE=log` the link is shown in the dashboard for copying. |
| FR-14 | If an AI step fails with a retryable error, the applicant can retry the failed step (`POST /sessions/{token}/retry`). If it still fails, the session ends with `finish_reason=failed`, the application becomes `failed`, and the recruiter can issue a new invite. |
| FR-15 | The recruiter sees an advisory answer-authenticity warning (`none`/`possible`/`likely` with short reasons) built from the client's typing/paste signals, the voice signals (start latency, speaking time, re-records), the fallback reasons, and an LLM judgement. It never rejects an applicant and never changes a score. The applicant is told on the intro screen that how answers were given is noted and that a person reviews it: "We note how answers were given (typed, pasted or spoken). A person reviews this." |
| FR-16 | Jobs with `spoken_english_required` run a voice interview: the interviewer's questions are read aloud and the applicant answers by speaking. The server transcribes the recording (language auto-detected), stores it in the private `audio` bucket, and uses the transcript as the answer text; the applicant cannot edit it. A text fallback is always allowed and is marked with its reason. The intro asks for recording consent: "Your spoken answers are recorded and transcribed by a third-party service (Groq). The recordings are kept for 30 days so the recruiter can listen, then deleted. You can answer in text instead." The recruiter can play a recording through the API and sees advisory fluency signals (pace, pauses, fillers, transcription confidence, languages, a short summary); fluency never changes a score or rejects anyone (ADR 0001). Recordings are deleted 30 days after upload; unsent uploads after 24 hours. The mode is fixed when the session is created, so editing the job only affects sessions created after the edit. |

## 5. Non-Functional

- **Cost:** total ≤ $5/month (Railway Hobby; Vercel Hobby, Supabase Free, optional Gmail API within the free Google quota = $0); text sessions stay < $0.01 in LLM cost; voice sessions are ≤ $0.05 including LLM and transcription.
- **Latency:** first token of an AI reply < 3 seconds (p50).
- **Security:** session tokens stored as SHA-256 hashes; secrets only in env; RLS enabled on all Supabase tables with no public policies; CVs in a private Storage bucket, downloadable only by the recruiter through the API; recordings go in a private `audio` bucket and are streamed only through the API; the frontend never talks to Supabase directly; CORS only `FRONTEND_ORIGIN`.
- **Deterministic checks:** all checks in the loop are evaluated by code, not by an LLM.
- **Observability:** every node execution is recorded in `node_events` (short input, output, model, tokens, duration).
- **Testability:** `LLM_MODE=fake` so tests & e2e run at no cost. Client E2E = 1 Playwright smoke test (mock).

## 6. Out of Scope

Multi-tenant/organizations; applicant accounts; auth other than the single recruiter password; Ollama/local LLM (after the MVP); payments; video; non-PDF CVs or scanned PDFs (OCR); email notifications to the recruiter; ATS integration; analytics; Supabase Auth/Realtime or direct Supabase access from the client; a separate subdomain (`apply.`) before a custom domain exists.

### Post-MVP (deliberately cut from the 1-week build)

- Gmail email as the default + custom domain verification (MVP: `EMAIL_MODE=log`, link copied from the dashboard).
- Fuller recruiter auth (multi-user, refresh tokens, server-side logout).
- Model fallback (`:free`) and advanced retries (regenerate interviewer, retry judge, planner attempts > 2). What stays: scorer/evidence check max 3 attempts in total and Judge follow-ups max 2 per topic.
- Automatic runner resume after a server restart.
- Full Playwright suite (MVP: 1 smoke test).
- Dark mode.

## 7. Success Metrics (for the demo)

- End-to-end apply → session link (from the dashboard) → session → score → shortlist works in production.
- 100% of scores have evidence that passes Evidence Check (or are explicitly marked `needs_manual`).
- The graph view shows ≥ 1 visible `loop.retry` (can be triggered with the demo job).
- Text-session LLM cost is recorded and < $0.01; a voice session including transcription is ≤ $0.05.
- Demo video ≤ 60 seconds; the README can be followed to run locally in < 10 minutes.
