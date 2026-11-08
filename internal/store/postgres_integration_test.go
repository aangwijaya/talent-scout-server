package store_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
	"github.com/aangwijaya/talent-scout-server/internal/store/migrations"
)

func TestPostgresStore(t *testing.T) {
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
	if _, err := db.ExecContext(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); err != nil {
		t.Fatalf("drop schema: %v", err)
	}
	if _, err := db.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if err := migrations.Run(ctx, db, "up"); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	db.Close()

	pool, err := store.OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	defer pool.Close()

	s := store.NewPostgres(pool)

	job := domain.Job{
		ID:          domain.NewID(),
		Title:       "Senior Frontend Engineer",
		Description: "Build the recruiter dashboard and the applicant screening experience.",
		Rubric:      []domain.RubricCriterion{{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte/React", Weight: 2}},
		Status:      domain.JobStatusOpen,
	}
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := s.CreateJob(ctx, job); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("CreateJob duplicate = %v, want ErrConflict", err)
	}
	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Title != job.Title || len(got.Rubric) != 1 {
		t.Fatalf("GetJob = %+v", got)
	}

	app := domain.Application{
		ID:           domain.NewID(),
		JobID:        job.ID,
		Name:         "Budi",
		Email:        "budi@example.com",
		CVObjectPath: "cv.pdf",
		CVText:       "text",
		Status:       domain.ApplicationStatusApplied,
	}
	if err := s.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}

	expiresAt := time.Now().Add(168 * time.Hour).UTC()
	session := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     expiresAt,
	}
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got, err := s.GetSessionByTokenHash(ctx, "hash"); err != nil || got.ID != session.ID {
		t.Fatalf("GetSessionByTokenHash = %+v, %v", got, err)
	}

	startedAt := time.Now().UTC()
	started, err := s.StartSession(ctx, session.ID, startedAt)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if started.Status != domain.SessionStatusActive || started.StartedAt == nil {
		t.Fatalf("StartSession = %+v", started)
	}
	if _, err := s.StartSession(ctx, session.ID, startedAt); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second StartSession = %v, want ErrConflict", err)
	}
	if _, err := s.StartSession(ctx, domain.NewID(), startedAt); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown StartSession = %v, want ErrNotFound", err)
	}

	topic := domain.Topic{
		ID:           domain.NewID(),
		SessionID:    session.ID,
		Position:     1,
		Title:        "State management",
		Goal:         "Assess state experience",
		RubricKeys:   []string{"frontend_depth"},
		SeedQuestion: "How do you manage state?",
		Status:       domain.TopicStatusActive,
	}
	if err := s.CreateTopics(ctx, []domain.Topic{topic}); err != nil {
		t.Fatalf("CreateTopics: %v", err)
	}
	topics, err := s.ListTopicsBySession(ctx, session.ID)
	if err != nil || len(topics) != 1 || len(topics[0].RubricKeys) != 1 {
		t.Fatalf("ListTopicsBySession = %+v, %v", topics, err)
	}

	message := domain.Message{
		ID:        domain.NewID(),
		SessionID: session.ID,
		TopicID:   &topic.ID,
		Role:      domain.MessageRoleApplicant,
		Kind:      domain.MessageKindAnswer,
		Content:   "I moved state into a separate store.",
	}
	if err := s.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	messages, err := s.ListMessagesBySession(ctx, session.ID, false)
	if err != nil || len(messages) != 1 {
		t.Fatalf("ListMessagesBySession = %+v, %v", messages, err)
	}

	scoreValue := 4
	if err := s.UpsertScores(ctx, []domain.Score{{
		ApplicationID: app.ID,
		RubricKey:     "frontend_depth",
		Score:         &scoreValue,
		Rationale:     "good",
		Evidence:      []domain.ScoreEvidence{{MessageID: message.ID, Quote: "separate store"}},
		Status:        domain.ScoreStatusValid,
		Attempts:      1,
	}}); err != nil {
		t.Fatalf("UpsertScores: %v", err)
	}
	scores, err := s.ListScoresByApplication(ctx, app.ID)
	if err != nil || len(scores) != 1 || scores[0].Score == nil || *scores[0].Score != 4 {
		t.Fatalf("ListScoresByApplication = %+v, %v", scores, err)
	}

	run := domain.GraphRun{ID: domain.NewID(), SessionID: session.ID, Status: domain.GraphRunStatusRunning}
	if err := s.CreateGraphRun(ctx, run); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	first, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: "planner", Type: "node.started", Payload: []byte(`{"k":1}`)})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	second, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: "planner", Type: "node.completed"})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seqs = %d, %d", first.Seq, second.Seq)
	}
	events, err := s.ListNodeEvents(ctx, run.ID, 1)
	if err != nil || len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("ListNodeEvents = %+v, %v", events, err)
	}
}

func testSchema(t *testing.T, dsn string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse TEST_DATABASE_URL: %v", err)
	}
	schema := u.Query().Get("search_path")
	if schema == "" {
		schema = "test"
	}
	return schema
}
