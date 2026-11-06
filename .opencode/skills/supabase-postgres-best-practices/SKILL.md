---
name: supabase-postgres-best-practices
description: "Postgres rules for query/index/connection-pool review or writing a new migration (pgx pool, indexes, pagination, upserts, batch inserts, short transactions, SKIP LOCKED, RLS-on/no-policy privileges). Load only for those tasks, not for every SQL line."
license: MIT
---

> Adapted from [supabase/agent-skills](https://github.com/supabase/agent-skills/tree/main/skills/supabase-postgres-best-practices) v1.1.1 (MIT, Copyright (c) 2026 Supabase). Trimmed to 12 rule files; see `../LICENSE-THIRD-PARTY.md`.

# Postgres Best Practices (trimmed)

Repo context: Supabase Postgres, Go + `pgx/v5` pool, goose migrations. The backend connects as `postgres` (bypasses RLS); every table has RLS **on with no policies** so the anon/authenticated API roles see nothing. Do not add `auth.uid()` policies.

## How to use

Open only the rule file that matches the task. Each file has: why it matters, incorrect SQL, correct SQL, notes.

| Task | Read |
|---|---|
| Pool size, timeouts, Supabase pooler | `references/conn-pooling.md`, `references/conn-limits.md` |
| Transaction pooler (port 6543) vs pgx prepared statements | `references/conn-prepared-statements.md` |
| New query or slow query: indexes | `references/query-missing-indexes.md`, `references/schema-foreign-key-indexes.md` |
| List endpoints | `references/data-pagination.md` (keyset, not big OFFSET) |
| Insert-or-update, bulk writes | `references/data-upsert.md`, `references/data-batch-inserts.md` |
| Transactions, job queue / sweeper | `references/lock-short-transactions.md`, `references/lock-skip-locked.md` |
| New table security | `references/security-rls-basics.md` (only the "enable RLS" part applies), `references/security-privileges.md` |

## Files kept

- `references/conn-pooling.md`
- `references/conn-limits.md`
- `references/conn-prepared-statements.md`
- `references/query-missing-indexes.md`
- `references/schema-foreign-key-indexes.md`
- `references/data-pagination.md`
- `references/data-upsert.md`
- `references/data-batch-inserts.md`
- `references/lock-short-transactions.md`
- `references/lock-skip-locked.md`
- `references/security-rls-basics.md`
- `references/security-privileges.md`

Every other upstream rule file was intentionally dropped; do not look for them.
