package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestMemoryJobs(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()

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
		t.Fatalf("CreateJob duplicate error = %v, want ErrConflict", err)
	}

	got, err := s.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Title != job.Title || len(got.Rubric) != 1 || got.CreatedAt.IsZero() {
		t.Fatalf("GetJob = %+v", got)
	}

	list, err := s.ListJobs(ctx)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListJobs len = %d, want 1", len(list))
	}

	if _, err := s.GetJob(ctx, domain.NewID()); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetJob missing error = %v, want ErrNotFound", err)
	}
}

func TestMemoryApplications(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()

	app := domain.Application{
		ID:           domain.NewID(),
		JobID:        jobID,
		Name:         "Budi",
		Email:        "budi@example.com",
		CVObjectPath: "cv.pdf",
		CVText:       "text",
		Status:       domain.ApplicationStatusApplied,
	}

	if err := s.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	duplicate := app
	duplicate.ID = domain.NewID()
	if err := s.CreateApplication(ctx, duplicate); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("CreateApplication duplicate error = %v, want ErrConflict", err)
	}

	got, err := s.GetApplicationByJobEmail(ctx, jobID, app.Email)
	if err != nil {
		t.Fatalf("GetApplicationByJobEmail: %v", err)
	}
	if got.ID != app.ID {
		t.Fatalf("GetApplicationByJobEmail ID = %q, want %q", got.ID, app.ID)
	}

	if err := s.UpdateApplicationStatus(ctx, app.ID, domain.ApplicationStatusReview); err != nil {
		t.Fatalf("UpdateApplicationStatus: %v", err)
	}
	app2, err := s.GetApplication(ctx, app.ID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app2.Status != domain.ApplicationStatusReview {
		t.Fatalf("Status = %q, want review", app2.Status)
	}

	decidedAt := time.Now().UTC()
	app3, err := s.SetApplicationDecision(ctx, app.ID, domain.ApplicationStatusShortlisted, "strong", decidedAt)
	if err != nil {
		t.Fatalf("SetApplicationDecision: %v", err)
	}
	if app3.Status != domain.ApplicationStatusShortlisted || app3.DecisionNote != "strong" || app3.DecidedAt == nil {
		t.Fatalf("SetApplicationDecision = %+v", app3)
	}

	review := domain.ApplicationStatusReview
	list, err := s.ListApplicationsByJob(ctx, jobID, &review)
	if err != nil {
		t.Fatalf("ListApplicationsByJob: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListApplicationsByJob(review) len = %d, want 0", len(list))
	}
	list, err = s.ListApplicationsByJob(ctx, jobID, nil)
	if err != nil {
		t.Fatalf("ListApplicationsByJob: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListApplicationsByJob len = %d, want 1", len(list))
	}
}

func TestMemorySessions(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	applicationID := domain.NewID()

	older := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: applicationID,
		TokenHash:     "hash-old",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
		CreatedAt:     time.Now().Add(-time.Hour).UTC(),
	}
	newer := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: applicationID,
		TokenHash:     "hash-new",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}

	if err := s.CreateSession(ctx, older); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := s.CreateSession(ctx, newer); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSessionByTokenHash(ctx, "hash-new")
	if err != nil {
		t.Fatalf("GetSessionByTokenHash: %v", err)
	}
	if got.ID != newer.ID {
		t.Fatalf("GetSessionByTokenHash ID = %q, want %q", got.ID, newer.ID)
	}

	latest, err := s.GetLatestSessionByApplication(ctx, applicationID)
	if err != nil {
		t.Fatalf("GetLatestSessionByApplication: %v", err)
	}
	if latest.ID != newer.ID {
		t.Fatalf("latest session ID = %q, want %q", latest.ID, newer.ID)
	}

	duplicate := newer
	duplicate.ID = domain.NewID()
	if err := s.CreateSession(ctx, duplicate); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("CreateSession duplicate token error = %v, want ErrConflict", err)
	}

	startedAt := time.Now().UTC()
	newer.Status = domain.SessionStatusActive
	newer.StartedAt = &startedAt
	if err := s.UpdateSession(ctx, newer); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	got, err = s.GetSession(ctx, newer.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Status != domain.SessionStatusActive || got.StartedAt == nil {
		t.Fatalf("GetSession = %+v", got)
	}
}

func TestMemoryStartSessionOnce(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()

	sess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: domain.NewID(),
		TokenHash:     "hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	startedAt := time.Now().UTC()
	got, err := s.StartSession(ctx, sess.ID, startedAt)
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if got.Status != domain.SessionStatusActive || got.StartedAt == nil || !got.StartedAt.Equal(startedAt) {
		t.Fatalf("StartSession = %+v", got)
	}

	if _, err := s.StartSession(ctx, sess.ID, startedAt); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second StartSession error = %v, want ErrConflict", err)
	}

	if _, err := s.StartSession(ctx, domain.NewID(), startedAt); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("unknown StartSession error = %v, want ErrNotFound", err)
	}
}

func TestMemoryStartSessionConcurrent(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	sess := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: domain.NewID(),
		TokenHash:     "hash",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour).UTC(),
	}
	if err := s.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	const n = 32
	results := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			_, err := s.StartSession(ctx, sess.ID, time.Now().UTC())
			results <- err
		}()
	}

	var success int
	for i := 0; i < n; i++ {
		if err := <-results; err == nil {
			success++
		} else if !errors.Is(err, store.ErrConflict) {
			t.Fatalf("unexpected StartSession error: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("success count = %d, want exactly 1", success)
	}
}

func TestMemoryTopicsAndMessages(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	sessionID := domain.NewID()

	topics := []domain.Topic{
		{ID: domain.NewID(), SessionID: sessionID, Position: 2, Title: "Second", Goal: "goal", RubricKeys: []string{"a"}, Status: domain.TopicStatusPending},
		{ID: domain.NewID(), SessionID: sessionID, Position: 1, Title: "First", Goal: "goal", RubricKeys: []string{"a", "b"}, Status: domain.TopicStatusActive},
	}
	if err := s.CreateTopics(ctx, topics); err != nil {
		t.Fatalf("CreateTopics: %v", err)
	}

	list, err := s.ListTopicsBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("ListTopicsBySession: %v", err)
	}
	if len(list) != 2 || list[0].Position != 1 || list[1].Position != 2 {
		t.Fatalf("ListTopicsBySession = %+v", list)
	}

	topics[1].Status = domain.TopicStatusDone
	if err := s.UpdateTopic(ctx, topics[1]); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}

	now := time.Now().UTC()
	topicID := topics[1].ID
	messages := []domain.Message{
		{ID: domain.NewID(), SessionID: sessionID, Role: domain.MessageRoleSystem, Kind: domain.MessageKindOpening, Content: "system", CreatedAt: now},
		{ID: domain.NewID(), SessionID: sessionID, TopicID: &topicID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "q1", CreatedAt: now.Add(time.Second)},
		{ID: domain.NewID(), SessionID: sessionID, TopicID: &topicID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "a1", CreatedAt: now.Add(2 * time.Second)},
	}
	for _, message := range messages {
		if err := s.CreateMessage(ctx, message); err != nil {
			t.Fatalf("CreateMessage: %v", err)
		}
	}

	visible, err := s.ListMessagesBySession(ctx, sessionID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(visible) != 2 {
		t.Fatalf("visible messages len = %d, want 2", len(visible))
	}
	all, err := s.ListMessagesBySession(ctx, sessionID, true)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("all messages len = %d, want 3", len(all))
	}
}

func TestMemoryScores(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	applicationID := domain.NewID()
	score := 4

	if err := s.UpsertScores(ctx, []domain.Score{{
		ID:            domain.NewID(),
		ApplicationID: applicationID,
		RubricKey:     "frontend_depth",
		Score:         &score,
		Rationale:     "good",
		Evidence:      []domain.ScoreEvidence{{MessageID: domain.NewID(), Quote: "I used Svelte"}},
		Status:        domain.ScoreStatusValid,
		Attempts:      1,
	}}); err != nil {
		t.Fatalf("UpsertScores: %v", err)
	}

	improved := 5
	if err := s.UpsertScores(ctx, []domain.Score{{
		ApplicationID: applicationID,
		RubricKey:     "frontend_depth",
		Score:         &improved,
		Rationale:     "great",
		Status:        domain.ScoreStatusValid,
		Attempts:      2,
	}}); err != nil {
		t.Fatalf("UpsertScores: %v", err)
	}

	list, err := s.ListScoresByApplication(ctx, applicationID)
	if err != nil {
		t.Fatalf("ListScoresByApplication: %v", err)
	}
	if len(list) != 1 || list[0].Score == nil || *list[0].Score != 5 {
		t.Fatalf("ListScoresByApplication = %+v", list)
	}
}

func TestMemoryGraphRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	sessionID := domain.NewID()

	run := domain.GraphRun{ID: domain.NewID(), SessionID: sessionID, Status: domain.GraphRunStatusRunning}
	if err := s.CreateGraphRun(ctx, run); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}

	got, err := s.GetGraphRunBySession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetGraphRunBySession: %v", err)
	}
	if got.ID != run.ID {
		t.Fatalf("GetGraphRunBySession ID = %q, want %q", got.ID, run.ID)
	}

	first, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: "planner", Type: "node.started", Payload: []byte(`{"a":1}`)})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	second, err := s.AppendNodeEvent(ctx, domain.NodeEvent{RunID: run.ID, NodeID: "planner", Type: "node.completed"})
	if err != nil {
		t.Fatalf("AppendNodeEvent: %v", err)
	}
	if first.Seq != 1 || second.Seq != 2 {
		t.Fatalf("seqs = %d, %d; want 1, 2", first.Seq, second.Seq)
	}

	events, err := s.ListNodeEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(events) != 1 || events[0].Seq != 2 {
		t.Fatalf("ListNodeEvents = %+v", events)
	}

	endedAt := time.Now().UTC()
	run.Status = domain.GraphRunStatusSucceeded
	run.EndedAt = &endedAt
	run.TotalTokensIn = 10
	run.CostUSD = 0.001
	if err := s.UpdateGraphRun(ctx, run); err != nil {
		t.Fatalf("UpdateGraphRun: %v", err)
	}
	got, err = s.GetGraphRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetGraphRun: %v", err)
	}
	if got.Status != domain.GraphRunStatusSucceeded || got.EndedAt == nil {
		t.Fatalf("GetGraphRun = %+v", got)
	}
}

func TestMemoryStatusDefaults(t *testing.T) {
	ctx := context.Background()
	s := store.NewMemory()
	jobID := domain.NewID()
	appID := domain.NewID()
	sessionID := domain.NewID()

	job := domain.Job{ID: jobID, Title: "T", Description: "D", Rubric: []domain.RubricCriterion{}}
	if err := s.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	got, err := s.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != domain.JobStatusOpen {
		t.Fatalf("job status = %q, want open", got.Status)
	}

	app := domain.Application{ID: appID, JobID: jobID, Name: "B", Email: "b@example.com", CVObjectPath: "cv.pdf"}
	if err := s.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	gotApp, err := s.GetApplication(ctx, appID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if gotApp.Status != domain.ApplicationStatusApplied {
		t.Fatalf("application status = %q, want applied", gotApp.Status)
	}

	session := domain.Session{ID: sessionID, ApplicationID: appID, TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour)}
	if err := s.CreateSession(ctx, session); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	gotSession, err := s.GetSession(ctx, sessionID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if gotSession.Status != domain.SessionStatusInvited {
		t.Fatalf("session status = %q, want invited", gotSession.Status)
	}
}
