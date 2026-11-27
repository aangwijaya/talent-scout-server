package store_test

import (
	"context"
	"errors"
	"net/url"
	"os"
	"strings"
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
	var audioUploadsRLS bool
	if err := db.QueryRowContext(ctx,
		`SELECT relrowsecurity FROM pg_class WHERE oid = 'audio_uploads'::regclass`).Scan(&audioUploadsRLS); err != nil {
		t.Fatalf("audio_uploads RLS lookup: %v", err)
	}
	if !audioUploadsRLS {
		t.Fatal("audio_uploads must have RLS enabled")
	}
	db.Close()

	pool, err := store.OpenPool(ctx, dsn)
	if err != nil {
		t.Fatalf("OpenPool: %v", err)
	}
	defer pool.Close()

	s := store.NewPostgres(pool)

	job := domain.Job{
		ID:                    domain.NewID(),
		Title:                 "Senior Frontend Engineer",
		Description:           "Build the recruiter dashboard and the applicant screening experience.",
		Rubric:                []domain.RubricCriterion{{Key: "frontend_depth", Label: "Frontend depth", Description: "Svelte/React", Weight: 2}},
		Status:                domain.JobStatusOpen,
		SpokenEnglishRequired: true,
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
	if got.Title != job.Title || len(got.Rubric) != 1 || !got.SpokenEnglishRequired {
		t.Fatalf("GetJob = %+v", got)
	}

	count, err := s.CountJobs(ctx)
	if err != nil || count != 1 {
		t.Fatalf("CountJobs = %d, %v; want 1, nil", count, err)
	}

	// The migration raises the description limit to 12000 characters; an
	// update must persist a description past the old 8000 limit.
	job.Title = "Staff Frontend Engineer"
	job.Description = strings.Repeat("a", 9000)
	job.Status = domain.JobStatusClosed
	job.SpokenEnglishRequired = false
	if err := s.UpdateJob(ctx, job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	updated, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob after update: %v", err)
	}
	if updated.Title != job.Title || updated.Status != domain.JobStatusClosed || len(updated.Description) != 9000 || updated.SpokenEnglishRequired {
		t.Fatalf("GetJob after update = %+v", updated)
	}
	if err := s.UpdateJob(ctx, domain.Job{ID: domain.NewID()}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateJob unknown = %v, want ErrNotFound", err)
	}

	job.Rubric = []domain.RubricCriterion{{Key: "system_design", Label: "System design", Description: "Component boundaries.", Weight: 3}}
	if err := s.UpdateJobRubricIfNoApplications(ctx, job); err != nil {
		t.Fatalf("UpdateJobRubricIfNoApplications without applications: %v", err)
	}
	if got, err := s.GetJob(ctx, job.ID); err != nil || got.Rubric[0].Key != "system_design" {
		t.Fatalf("GetJob after rubric update = %+v, %v", got, err)
	}
	if err := s.UpdateJobRubricIfNoApplications(ctx, domain.Job{ID: domain.NewID()}); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("UpdateJobRubricIfNoApplications unknown = %v, want ErrNotFound", err)
	}

	newerJob := domain.Job{
		ID:          domain.NewID(),
		Title:       "Newer Job",
		Description: strings.Repeat("b", 60),
		Rubric:      []domain.RubricCriterion{{Key: "system_design", Label: "System design", Description: "Component boundaries.", Weight: 2}},
		Status:      domain.JobStatusOpen,
	}
	if err := s.CreateJobWithinLimit(ctx, newerJob, 20); err != nil {
		t.Fatalf("CreateJobWithinLimit: %v", err)
	}
	if err := s.CreateJobWithinLimit(ctx, domain.Job{
		ID:          domain.NewID(),
		Title:       "Over Cap",
		Description: strings.Repeat("c", 60),
		Status:      domain.JobStatusOpen,
	}, 2); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("CreateJobWithinLimit at cap = %v, want ErrConflict", err)
	}
	jobs, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 || jobs[0].ID != newerJob.ID {
		t.Fatalf("ListJobs = %+v, want newest first", jobs)
	}

	app := domain.Application{
		ID:           domain.NewID(),
		JobID:        job.ID,
		Name:         "Budi",
		Email:        "budi@example.com",
		CVObjectPath: "cv.pdf",
		CVText:       "text",
		Status:       domain.ApplicationStatusInvited,
	}
	if err := s.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}

	blocked := job
	blocked.Title = "Should Not Apply"
	blocked.Rubric = []domain.RubricCriterion{{Key: "communication", Label: "Communication", Description: "Clarity and collaboration.", Weight: 1}}
	if err := s.UpdateJobRubricIfNoApplications(ctx, blocked); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("UpdateJobRubricIfNoApplications with application = %v, want ErrConflict", err)
	}
	if got, err := s.GetJob(ctx, job.ID); err != nil || got.Title != job.Title || got.Rubric[0].Key != "system_design" {
		t.Fatalf("job changed despite conflict = %+v, %v", got, err)
	}

	expiresAt := time.Now().Add(168 * time.Hour).UTC()
	session := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     "hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     expiresAt,
		Voice:         true,
	}
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	storedSession, err := s.GetSessionByTokenHash(ctx, "hash")
	if err != nil || storedSession.ID != session.ID || !storedSession.Voice || storedSession.RecordingConsent != nil {
		t.Fatalf("GetSessionByTokenHash = %+v, %v", storedSession, err)
	}

	startedAt := time.Now().UTC()
	consent := true
	started, err := s.StartSessionWithConsent(ctx, session.ID, startedAt, &consent)
	if err != nil {
		t.Fatalf("StartSessionWithConsent: %v", err)
	}
	if started.Status != domain.SessionStatusActive || started.StartedAt == nil || !started.Voice ||
		started.RecordingConsent == nil || !*started.RecordingConsent {
		t.Fatalf("StartSessionWithConsent = %+v", started)
	}
	if _, err := s.StartSession(ctx, session.ID, startedAt); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second StartSession = %v, want ErrConflict", err)
	}
	if _, err := s.StartSession(ctx, domain.NewID(), startedAt); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown StartSession = %v, want ErrNotFound", err)
	}
	startedApp, err := s.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("GetApplication after start: %v", err)
	}
	if startedApp.Status != domain.ApplicationStatusInSession {
		t.Fatalf("application status after start = %q, want in_session", startedApp.Status)
	}

	app2 := domain.Application{
		ID:           domain.NewID(),
		JobID:        job.ID,
		Name:         "Sari",
		Email:        "sari@example.com",
		CVObjectPath: "cv2.pdf",
		CVText:       "text2",
		Status:       domain.ApplicationStatusInvited,
	}
	sess2 := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app2.ID,
		TokenHash:     "hash2",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     expiresAt,
	}
	if err := s.CreateApplicationWithSession(ctx, app2, sess2); err != nil {
		t.Fatalf("CreateApplicationWithSession: %v", err)
	}
	if got, err := s.GetSessionByTokenHash(ctx, "hash2"); err != nil || got.ApplicationID != app2.ID {
		t.Fatalf("CreateApplicationWithSession session = %+v, %v", got, err)
	}

	count, err = s.CountApplicationsByJob(ctx, job.ID)
	if err != nil || count != 2 {
		t.Fatalf("CountApplicationsByJob = %d, %v; want 2, nil", count, err)
	}

	nextSession := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app2.ID,
		TokenHash:     "hash-next",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     expiresAt,
	}
	if err := s.ResendInvite(ctx, &sess2.ID, nextSession); err != nil {
		t.Fatalf("ResendInvite: %v", err)
	}
	revoked, err := s.GetSession(ctx, sess2.ID)
	if err != nil || revoked.Status != domain.SessionStatusRevoked {
		t.Fatalf("revoked session = %+v, %v", revoked, err)
	}
	if got, err := s.GetSession(ctx, nextSession.ID); err != nil || got.Status != domain.SessionStatusInvited {
		t.Fatalf("new session = %+v, %v", got, err)
	}

	// Revoking a session that is no longer invited conflicts and stores nothing.
	stale := domain.Session{ID: domain.NewID(), ApplicationID: app2.ID, TokenHash: "hash-stale", Status: domain.SessionStatusInvited, ExpiresAt: expiresAt}
	if err := s.ResendInvite(ctx, &sess2.ID, stale); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("ResendInvite with a revoked session = %v, want ErrConflict", err)
	}
	if _, err := s.GetSession(ctx, stale.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("stale resend stored a session (GetSession err = %v)", err)
	}

	// The failed-application path (nothing to revoke) works once; a second,
	// concurrent-style resend after the application left failed conflicts.
	if err := s.UpdateApplicationStatus(ctx, app2.ID, domain.ApplicationStatusFailed); err != nil {
		t.Fatalf("UpdateApplicationStatus failed: %v", err)
	}
	fromFailed := domain.Session{ID: domain.NewID(), ApplicationID: app2.ID, TokenHash: "hash-failed-1", Status: domain.SessionStatusInvited, ExpiresAt: expiresAt}
	if err := s.ResendInvite(ctx, nil, fromFailed); err != nil {
		t.Fatalf("ResendInvite for a failed application: %v", err)
	}
	if got, err := s.GetApplication(ctx, app2.ID); err != nil || got.Status != domain.ApplicationStatusInvited {
		t.Fatalf("application after failed resend = %+v, %v", got, err)
	}
	again := domain.Session{ID: domain.NewID(), ApplicationID: app2.ID, TokenHash: "hash-failed-2", Status: domain.SessionStatusInvited, ExpiresAt: expiresAt}
	if err := s.ResendInvite(ctx, nil, again); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second failed-path ResendInvite = %v, want ErrConflict", err)
	}
	if _, err := s.GetSession(ctx, again.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second failed-path resend stored a session (GetSession err = %v)", err)
	}

	dupApp := domain.Application{
		ID:           domain.NewID(),
		JobID:        job.ID,
		Name:         "Sari",
		Email:        "sari@example.com",
		CVObjectPath: "cv3.pdf",
		CVText:       "text3",
		Status:       domain.ApplicationStatusInvited,
	}
	dupSess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: dupApp.ID,
		TokenHash:     "hash-dup",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     expiresAt,
	}
	if err := s.CreateApplicationWithSession(ctx, dupApp, dupSess); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("duplicate CreateApplicationWithSession = %v, want ErrConflict", err)
	}
	if _, err := s.GetApplication(ctx, dupApp.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("duplicate application should not exist, err = %v", err)
	}

	expiredSess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app2.ID,
		TokenHash:     "hash-expired",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(-time.Hour).UTC(),
	}
	if err := s.CreateSession(ctx, expiredSess); err != nil {
		t.Fatalf("CreateSession expired: %v", err)
	}
	changed, err := s.ExpireSession(ctx, expiredSess.ID, time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("ExpireSession = %v, %v; want true, nil", changed, err)
	}
	if got, _ := s.GetSession(ctx, expiredSess.ID); got.Status != domain.SessionStatusExpired {
		t.Fatalf("expired session status = %q, want expired", got.Status)
	}
	changed, err = s.ExpireSession(ctx, expiredSess.ID, time.Now().UTC())
	if err != nil || changed {
		t.Fatalf("second ExpireSession = %v, %v; want false, nil", changed, err)
	}

	if err := s.UpdateApplicationStatus(ctx, app2.ID, domain.ApplicationStatusInSession); err != nil {
		t.Fatalf("UpdateApplicationStatus in_session: %v", err)
	}
	activeStarted := time.Now().Add(-2 * time.Hour).UTC()
	activeSess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app2.ID,
		TokenHash:     "hash-active",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     expiresAt,
		StartedAt:     &activeStarted,
	}
	if err := s.CreateSession(ctx, activeSess); err != nil {
		t.Fatalf("CreateSession active: %v", err)
	}
	changed, err = s.AbandonSession(ctx, activeSess.ID, time.Now().UTC())
	if err != nil || !changed {
		t.Fatalf("AbandonSession = %v, %v; want true, nil", changed, err)
	}
	gotAbandoned, err := s.GetSession(ctx, activeSess.ID)
	if err != nil {
		t.Fatalf("GetSession abandoned: %v", err)
	}
	if gotAbandoned.Status != domain.SessionStatusCompleted || gotAbandoned.FinishReason == nil || *gotAbandoned.FinishReason != domain.FinishReasonAbandoned || gotAbandoned.EndedAt == nil {
		t.Fatalf("abandoned session = %+v", gotAbandoned)
	}
	abandonedApp, err := s.GetApplication(ctx, app2.ID)
	if err != nil || abandonedApp.Status != domain.ApplicationStatusScoring {
		t.Fatalf("application after AbandonSession = %+v, %v; want scoring", abandonedApp, err)
	}
	changed, err = s.AbandonSession(ctx, activeSess.ID, time.Now().UTC())
	if err != nil || changed {
		t.Fatalf("second AbandonSession = %v, %v; want false, nil", changed, err)
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
		Entry:     &domain.MessageEntry{PastedChars: 120, TypedChars: 300, DurationMS: 4500},
	}
	if err := s.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	messages, err := s.ListMessagesBySession(ctx, session.ID, false)
	if err != nil || len(messages) != 1 {
		t.Fatalf("ListMessagesBySession = %+v, %v", messages, err)
	}
	if messages[0].Entry == nil || *messages[0].Entry != *message.Entry {
		t.Fatalf("stored entry = %+v, want %+v", messages[0].Entry, message.Entry)
	}

	testPostgresAudioUploads(ctx, t, s, session.ID, message.ID)

	assessedAt := time.Now().UTC().Truncate(time.Microsecond)
	auth := domain.Authenticity{
		Level:      domain.AuthenticityLevelPossible,
		Reasons:    []string{"1 answer was pasted in full"},
		AssessedAt: assessedAt,
	}
	if err := s.SetApplicationAuthenticity(ctx, app.ID, &auth); err != nil {
		t.Fatalf("SetApplicationAuthenticity: %v", err)
	}
	assessed, err := s.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("GetApplication after authenticity: %v", err)
	}
	if assessed.Authenticity == nil || assessed.Authenticity.Level != domain.AuthenticityLevelPossible || len(assessed.Authenticity.Reasons) != 1 {
		t.Fatalf("stored authenticity = %+v, want the possible assessment", assessed.Authenticity)
	}
	if !assessed.Authenticity.AssessedAt.Equal(assessedAt) {
		t.Fatalf("assessed_at = %s, want %s", assessed.Authenticity.AssessedAt, assessedAt)
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
	running, err := s.ListGraphRunsByStatus(ctx, domain.GraphRunStatusRunning)
	if err != nil || len(running) != 1 || running[0].ID != run.ID {
		t.Fatalf("ListGraphRunsByStatus = %+v, %v; want only %s", running, err, run.ID)
	}
	first, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: domain.StrPtr("planner"), Attempt: domain.IntPtr(1), Type: "node.started", Payload: []byte(`{"k":1}`)})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	second, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: domain.StrPtr("planner"), Attempt: domain.IntPtr(1), Type: "node.completed"})
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

// testPostgresAudioUploads checks the retention SQL itself: which uploads are due
// (sent after 30 days, unsent after 24 hours, never one already deleted), marking
// one deleted, and attaching an upload to an answer.
func testPostgresAudioUploads(ctx context.Context, t *testing.T, s *store.Postgres, sessionID, messageID string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Microsecond)
	upload := func(uploadedAt time.Time, sent bool) store.AudioUpload {
		id := domain.NewID()
		u := store.AudioUpload{
			ID:          id,
			SessionID:   sessionID,
			ObjectPath:  "sessions/" + sessionID + "/" + id + ".webm",
			ContentType: "audio/webm",
			SizeBytes:   1024,
			DurationMS:  3000,
			Transcript:  "a spoken answer",
			Language:    "en",
			CostUSD:     0.0001,
			Stored:      true,
			Counted:     true,
			UploadedAt:  uploadedAt,
		}
		if sent {
			u.MessageID = &messageID
		}
		return u
	}
	sentOld := upload(now.Add(-31*24*time.Hour), true)
	sentNew := upload(now.Add(-29*24*time.Hour), true)
	unsentOld := upload(now.Add(-25*time.Hour), false)
	unsentNew := upload(now.Add(-23*time.Hour), false)
	deleted := upload(now.Add(-40*24*time.Hour), false)
	rejected := upload(now, false)
	rejected.ID = domain.NewID()
	rejected.ObjectPath = ""
	rejected.DurationMS = 9000
	rejected.CostUSD = 0.0002
	rejected.Counted = false
	rejected.Stored = false // the helper stores every upload; a rejected one never reached the bucket
	for _, u := range []store.AudioUpload{sentOld, sentNew, unsentOld, unsentNew, deleted} {
		if err := s.CreateAudioUpload(ctx, u); err != nil {
			t.Fatalf("CreateAudioUpload: %v", err)
		}
	}
	if err := s.CreateAudioUpload(ctx, rejected); err != nil {
		t.Fatalf("CreateAudioUpload(rejected): %v", err)
	}

	// The new metadata round-trips, including counted=false for a rejected
	// transcription that is billed but does not consume the budget, and the
	// stored flag that tells a not_stored recording apart from a deleted one.
	stored, err := s.GetAudioUpload(ctx, rejected.ID)
	if err != nil {
		t.Fatalf("GetAudioUpload(rejected): %v", err)
	}
	if stored.Transcript != rejected.Transcript || stored.Language != "en" || stored.CostUSD != rejected.CostUSD || stored.Counted {
		t.Fatalf("rejected upload = %+v, want transcript/language/cost/counted=false", stored)
	}
	if stored.Stored {
		t.Fatal("rejected upload stored = true, want false")
	}
	if err := s.SetAudioUploadStored(ctx, rejected.ID, true); err != nil {
		t.Fatalf("SetAudioUploadStored: %v", err)
	}
	if got, err := s.GetAudioUpload(ctx, rejected.ID); err != nil || !got.Stored {
		t.Fatalf("stored flag = %v, %v; want true after SetAudioUploadStored", got.Stored, err)
	}
	if err := s.SetAudioUploadStored(ctx, domain.NewID(), true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("SetAudioUploadStored unknown = %v, want ErrNotFound", err)
	}
	usage, err := s.GetAudioUsage(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetAudioUsage: %v", err)
	}
	if usage.DurationMS != 5*3000 {
		t.Fatalf("usage duration = %d, want only the five counted uploads (15000ms)", usage.DurationMS)
	}
	if want := 5*0.0001 + 0.0002; usage.CostUSD < want-1e-12 || usage.CostUSD > want+1e-12 {
		t.Fatalf("usage cost = %v, want every upload's cost (%v)", usage.CostUSD, want)
	}
	// Insert never stores deleted_at; only the sweeper's mark sets it.
	if err := s.MarkAudioUploadDeleted(ctx, deleted.ID, now.Add(-time.Hour)); err != nil {
		t.Fatalf("MarkAudioUploadDeleted(already deleted): %v", err)
	}

	dueIDs := func() map[string]bool {
		t.Helper()
		due, err := s.ListAudioUploadsDue(ctx, now.Add(-30*24*time.Hour), now.Add(-24*time.Hour))
		if err != nil {
			t.Fatalf("ListAudioUploadsDue: %v", err)
		}
		ids := map[string]bool{}
		for _, u := range due {
			ids[u.ID] = true
		}
		return ids
	}
	if got := dueIDs(); len(got) != 2 || !got[sentOld.ID] || !got[unsentOld.ID] {
		t.Fatalf("due = %v, want only the 31-day sent and the 25-hour unsent upload", got)
	}

	if err := s.MarkAudioUploadDeleted(ctx, sentOld.ID, now); err != nil {
		t.Fatalf("MarkAudioUploadDeleted: %v", err)
	}
	if got := dueIDs(); len(got) != 1 || !got[unsentOld.ID] {
		t.Fatalf("due after mark = %v, want only the 25-hour unsent upload", got)
	}
	if got, err := s.GetAudioUpload(ctx, sentOld.ID); err != nil || got.DeletedAt == nil || !got.DeletedAt.Equal(now) {
		t.Fatalf("deleted upload = %+v, %v; want deleted_at %s", got, err, now)
	}

	if err := s.AttachAudioUpload(ctx, unsentNew.ID, messageID); err != nil {
		t.Fatalf("AttachAudioUpload: %v", err)
	}
	if got, err := s.GetAudioUpload(ctx, unsentNew.ID); err != nil || got.MessageID == nil || *got.MessageID != messageID {
		t.Fatalf("attached upload = %+v, %v", got, err)
	}
	if err := s.AttachAudioUpload(ctx, unsentNew.ID, messageID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second AttachAudioUpload = %v, want ErrConflict", err)
	}
	if err := s.AttachAudioUpload(ctx, domain.NewID(), messageID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown AttachAudioUpload = %v, want ErrNotFound", err)
	}

	// A spoken answer inserts the message and attaches the upload in one
	// transaction; a second send of the same upload cannot also succeed.
	spokenUpload := upload(now, false)
	spokenUpload.ID = domain.NewID()
	spokenUpload.ObjectPath = "sessions/" + sessionID + "/" + spokenUpload.ID + ".webm"
	if err := s.CreateAudioUpload(ctx, spokenUpload); err != nil {
		t.Fatalf("CreateAudioUpload(spoken): %v", err)
	}
	spoken := domain.Message{
		ID:        domain.NewID(),
		SessionID: sessionID,
		Role:      domain.MessageRoleApplicant,
		Kind:      domain.MessageKindAnswer,
		Content:   spokenUpload.Transcript,
		Entry:     &domain.MessageEntry{Mode: "spoken", SpeakingMS: 3000},
	}
	if err := s.CreateMessageWithAudioUpload(ctx, spoken, spokenUpload.ID); err != nil {
		t.Fatalf("CreateMessageWithAudioUpload: %v", err)
	}
	if got, err := s.GetAudioUpload(ctx, spokenUpload.ID); err != nil || got.MessageID == nil || *got.MessageID != spoken.ID {
		t.Fatalf("spoken upload = %+v, %v; want attached to %s", got, err, spoken.ID)
	}
	second := spoken
	second.ID = domain.NewID()
	if err := s.CreateMessageWithAudioUpload(ctx, second, spokenUpload.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateMessageWithAudioUpload = %v, want ErrConflict", err)
	}
	if _, err := s.ListMessagesBySession(ctx, sessionID, false); err != nil {
		t.Fatalf("ListMessagesBySession after attach: %v", err)
	}

	// The upload join used by the recruiter detail can read a session's uploads.
	sessionUploads, err := s.ListAudioUploadsBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListAudioUploadsBySession: %v", err)
	}
	var found bool
	for _, u := range sessionUploads {
		if u.ID == spokenUpload.ID {
			found = true
			if !u.Stored || u.DurationMS != spokenUpload.DurationMS || u.Language != spokenUpload.Language {
				t.Fatalf("joined upload = %+v, want stored metadata", u)
			}
		}
	}
	if !found {
		t.Fatalf("ListAudioUploadsBySession did not return the attached upload")
	}

	// A rejected upload cannot be attached by the send transaction.
	rejectedMessage := spoken
	rejectedMessage.ID = domain.NewID()
	if err := s.CreateMessageWithAudioUpload(ctx, rejectedMessage, rejected.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("CreateMessageWithAudioUpload(rejected) = %v, want ErrNotFound", err)
	}
}
