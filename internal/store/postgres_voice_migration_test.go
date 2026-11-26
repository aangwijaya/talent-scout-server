package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pressly/goose/v3"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
	"github.com/aangwijaya/talent-scout-server/internal/store/migrations"
)

// jobs added before the voice snapshot keep working as text sessions: the new
// columns are NOT NULL DEFAULT false (or NULL for consent).
func TestVoiceMigrationDefaultsExistingRowsToText(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is empty")
	}

	ctx := context.Background()
	schema := testSchema(t, dsn)
	db, err := store.OpenSQLDB(dsn)
	if err != nil {
		t.Fatalf("OpenSQLDB: %v", err)
	}
	defer db.Close()

	if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.UpToContext(ctx, db, ".", 5); err != nil {
		t.Fatalf("migrate to before the voice snapshot: %v", err)
	}

	jobID := domain.NewID()
	appID := domain.NewID()
	sessionID := domain.NewID()
	if _, err := db.ExecContext(ctx, `
		INSERT INTO jobs (id, title, description, rubric, status)
		VALUES ($1, 'Senior Frontend Engineer', 'We are hiring a senior frontend engineer with deep Svelte and React experience.', '[]', 'open')`,
		jobID); err != nil {
		t.Fatalf("insert pre-migration job: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO applications (id, job_id, name, email, cv_object_path, status)
		VALUES ($1, $2, 'Budi', 'budi@example.com', 'cv.pdf', 'invited')`,
		appID, jobID); err != nil {
		t.Fatalf("insert pre-migration application: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO sessions (id, application_id, token_hash, status, expires_at)
		VALUES ($1, $2, 'pre-migration-hash', 'invited', now() + interval '1 day')`,
		sessionID, appID); err != nil {
		t.Fatalf("insert pre-migration session: %v", err)
	}

	if err := goose.UpContext(ctx, db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	var spokenEnglish bool
	if err := db.QueryRowContext(ctx, `SELECT spoken_english_required FROM jobs WHERE id = $1`, jobID).Scan(&spokenEnglish); err != nil {
		t.Fatalf("read job flag: %v", err)
	}
	var voice bool
	var consent *bool
	if err := db.QueryRowContext(ctx, `SELECT voice, recording_consent FROM sessions WHERE id = $1`, sessionID).Scan(&voice, &consent); err != nil {
		t.Fatalf("read session voice snapshot: %v", err)
	}
	if spokenEnglish || voice || consent != nil {
		t.Fatalf("existing rows = spoken %v, voice %v, consent %v; want false, false, null", spokenEnglish, voice, consent)
	}
}
