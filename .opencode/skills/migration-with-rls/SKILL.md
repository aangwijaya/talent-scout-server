---
name: migration-with-rls
description: Write goose migrations for Supabase Postgres with RLS enabled on every table, no policies, and anon/authenticated privileges revoked. Use when creating or changing tables.
compatibility: opencode
---

# Migrations with RLS (Supabase)

Location: `internal/store/migrations/NNNNN_<name>.sql` (goose, postgres dialect, embedded).

## Mandatory rules
- Every `CREATE TABLE` is followed, in the same file, by:
  ```sql
  ALTER TABLE <t> ENABLE ROW LEVEL SECURITY;
  REVOKE ALL ON TABLE <t> FROM anon, authenticated;
  ```
- **No policies** at all. **Do not** use `FORCE ROW LEVEL SECURITY` (the backend uses the owner role and must bypass RLS).
- The first migration also enables RLS and revokes for `goose_db_version`.
- Types: `uuid` (`DEFAULT gen_random_uuid()`), `timestamptz` (`DEFAULT now()`), `jsonb` for structured data, `text` + `CHECK` for enums.
- Tables & columns follow `docs/ARCHITECTURE.md` §5. New tables/columns beyond that → `contract-change` issue first.
- Provide a correct `-- +goose Down` block.

## goose notes
- Create files with `goose -dir internal/store/migrations create <name> sql`; prefer sequential numbering (`goose fix` converts timestamped dev files before merge).
- Each file needs `-- +goose Up`; put `-- +goose Down` after it, undoing the Up in reverse order.
- Wrap functions, `DO` blocks and anything with inner semicolons in `-- +goose StatementBegin` / `-- +goose StatementEnd`.
- goose runs each file in one transaction. Statements that can't run in a transaction (`CREATE INDEX CONCURRENTLY`) need `-- +goose NO TRANSACTION` at the top and must be alone in their file.
- Migrations are embedded (`embed.FS` + `goose.SetBaseFS`) and the dialect is `postgres`; don't read SQL from disk at runtime.
- Check with `goose status` before and after `up`; test `down` then `up` once locally for every new file.
- Keep data backfills out of schema files; large backfills go in a separate migration (or a Go migration) in small batches.
- For index and type choices, see `supabase-postgres-best-practices`.

## Verification
1. `go run ./cmd/migrate up` against the dev database (`DATABASE_URL` from `.env`, never print it).
2. Supabase Dashboard → Advisors → Security: **0 findings** for "RLS disabled". A human checks this; note in the PR that it needs checking.
3. Store integration tests (skipped when `TEST_DATABASE_URL` is empty) stay green.
4. Update the in-memory implementation when the schema changes.

## Don't
- Create public policies, enable `anon` access, or put the service role key in a migration file.
- Change a migration that is already merged; create a new file.
