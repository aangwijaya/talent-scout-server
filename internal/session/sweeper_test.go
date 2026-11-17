package session

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func seedSession(t *testing.T, st store.Store, s domain.Session) {
	t.Helper()
	if err := st.CreateSession(context.Background(), s); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func seedApplication(t *testing.T, st store.Store, app domain.Application) {
	t.Helper()
	if err := st.CreateApplication(context.Background(), app); err != nil {
		t.Fatalf("seed application: %v", err)
	}
}

func seedMessage(t *testing.T, st store.Store, message domain.Message) {
	t.Helper()
	if err := st.CreateMessage(context.Background(), message); err != nil {
		t.Fatalf("seed message: %v", err)
	}
}

func seedTopic(t *testing.T, st store.Store, sessionID string) {
	t.Helper()
	if err := st.CreateTopics(context.Background(), []domain.Topic{{
		ID: "topic-" + sessionID, SessionID: sessionID, Position: 1,
		Title: "State management", Goal: "Assess state experience",
		RubricKeys: []string{"frontend_depth"}, SeedQuestion: "How?",
		Status: domain.TopicStatusActive,
	}}); err != nil {
		t.Fatalf("seed topic: %v", err)
	}
}

func newSweeper(t *testing.T, st store.Store, registry *Registry) *Sweeper {
	t.Helper()
	return NewSweeper(st, registry, 15*time.Minute, slog.New(slog.NewTextHandler(discardWriter{}, nil)))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestSweepExpiresInvitedPastTTL(t *testing.T) {
	st := store.NewMemory()
	seedSession(t, st, domain.Session{
		ID:            "s-expired",
		ApplicationID: "a1",
		TokenHash:     "hash-expired",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(-time.Hour),
	})
	seedSession(t, st, domain.Session{
		ID:            "s-future",
		ApplicationID: "a1",
		TokenHash:     "hash-future",
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     time.Now().Add(time.Hour),
	})

	expired, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if expired != 1 {
		t.Errorf("expired = %d, want 1", expired)
	}
	if abandoned != 0 || timedOut != 0 {
		t.Errorf("abandoned = %d timedOut = %d, want 0/0", abandoned, timedOut)
	}

	got, _ := st.GetSession(context.Background(), "s-expired")
	if got.Status != domain.SessionStatusExpired {
		t.Errorf("s-expired status = %q, want expired", got.Status)
	}
	still, _ := st.GetSession(context.Background(), "s-future")
	if still.Status != domain.SessionStatusInvited {
		t.Errorf("s-future status = %q, want invited", still.Status)
	}
}

func TestSweepAbandonsIdleActiveSessionWithNoRunner(t *testing.T) {
	st := store.NewMemory()
	seedApplication(t, st, domain.Application{ID: "a-idle", JobID: "j1", Name: "Budi", Email: "b@example.com", Status: domain.ApplicationStatusInSession})
	// Idle past ABANDON_AFTER (10 min) but still inside the session's time
	// limit, so the reason must be abandoned, not time_limit.
	started := time.Now().Add(-12 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-stale",
		ApplicationID: "a-idle",
		TokenHash:     "hash-stale",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	seedTopic(t, st, "s-stale")
	recent := time.Now().Add(-5 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-recent",
		ApplicationID: "a-idle",
		TokenHash:     "hash-recent",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &recent,
	})

	_, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 1 {
		t.Errorf("abandoned = %d, want 1", abandoned)
	}
	if timedOut != 0 {
		t.Errorf("timedOut = %d, want 0", timedOut)
	}

	got, _ := st.GetSession(context.Background(), "s-stale")
	if got.Status != domain.SessionStatusCompleted {
		t.Errorf("s-stale status = %q, want completed", got.Status)
	}
	if got.FinishReason == nil || *got.FinishReason != domain.FinishReasonAbandoned {
		t.Errorf("s-stale finish_reason = %v, want abandoned", got.FinishReason)
	}
	if got.EndedAt == nil {
		t.Errorf("s-stale ended_at is nil")
	}
	app, _ := st.GetApplication(context.Background(), "a-idle")
	if app.Status != domain.ApplicationStatusScoring {
		t.Errorf("application status = %q, want scoring", app.Status)
	}
	still, _ := st.GetSession(context.Background(), "s-recent")
	if still.Status != domain.SessionStatusActive {
		t.Errorf("s-recent status = %q, want active", still.Status)
	}
}

func TestSweepEndsSessionPastTimeLimit(t *testing.T) {
	st := store.NewMemory()
	seedApplication(t, st, domain.Application{ID: "a-limit", JobID: "j1", Name: "Budi", Email: "b@example.com", Status: domain.ApplicationStatusInSession})
	started := time.Now().Add(-20 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-limit",
		ApplicationID: "a-limit",
		TokenHash:     "hash-limit",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	seedTopic(t, st, "s-limit")
	run := domain.GraphRun{ID: "run-limit", SessionID: "s-limit", Status: domain.GraphRunStatusRunning}
	if err := st.CreateGraphRun(context.Background(), run); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}

	_, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if timedOut != 1 {
		t.Errorf("timedOut = %d, want 1", timedOut)
	}
	if abandoned != 0 {
		t.Errorf("abandoned = %d, want 0: a session past its limit is time_limit, never abandoned", abandoned)
	}

	got, _ := st.GetSession(context.Background(), "s-limit")
	if got.Status != domain.SessionStatusCompleted {
		t.Errorf("status = %q, want completed", got.Status)
	}
	if got.FinishReason == nil || *got.FinishReason != domain.FinishReasonTimeLimit {
		t.Errorf("finish_reason = %v, want time_limit", got.FinishReason)
	}
	app, _ := st.GetApplication(context.Background(), "a-limit")
	if app.Status != domain.ApplicationStatusScoring {
		t.Errorf("application status = %q, want scoring", app.Status)
	}

	// With no live runner the sweeper persists session.ended itself so a
	// reconnecting client can replay it.
	events, err := st.ListNodeEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var ended *domain.NodeEvent
	for i := range events {
		if events[i].Type == "session.ended" {
			ended = &events[i]
		}
	}
	if ended == nil {
		t.Fatal("no persisted session.ended event")
	}
	if string(ended.Payload) != `{"finish_reason":"time_limit"}` {
		t.Errorf("session.ended payload = %s, want time_limit", ended.Payload)
	}
}

func TestSweepLeavesSessionMidStepAlone(t *testing.T) {
	st := store.NewMemory()
	started := time.Now().Add(-20 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-midstep",
		ApplicationID: "a1",
		TokenHash:     "hash-midstep",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	seedTopic(t, st, "s-midstep")
	registry := NewRegistry()
	registry.Add("s-midstep")

	_, abandoned, timedOut, err := newSweeper(t, st, registry).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 0 || timedOut != 0 {
		t.Errorf("abandoned=%d timedOut=%d, want 0/0 while a step is in flight", abandoned, timedOut)
	}
	got, _ := st.GetSession(context.Background(), "s-midstep")
	if got.Status != domain.SessionStatusActive {
		t.Errorf("status = %q, want active while a step is in flight", got.Status)
	}

	// Once the step ends the session is eligible again.
	registry.Remove("s-midstep")
	_, _, timedOut, err = newSweeper(t, st, registry).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce after step end: %v", err)
	}
	if timedOut != 1 {
		t.Errorf("timedOut = %d, want 1 after the step ended", timedOut)
	}
}

func TestSweepUsesLastApplicantMessageForIdle(t *testing.T) {
	st := store.NewMemory()
	started := time.Now().Add(-14 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-answered",
		ApplicationID: "a1",
		TokenHash:     "hash-answered",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	seedMessage(t, st, domain.Message{
		ID: "m-answer", SessionID: "s-answered", Role: domain.MessageRoleApplicant,
		Kind: domain.MessageKindAnswer, Content: "recent answer", CreatedAt: time.Now().Add(-2 * time.Minute),
	})

	_, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 0 || timedOut != 0 {
		t.Errorf("abandoned=%d timedOut=%d, want 0/0: the last applicant answer was only 2 minutes ago", abandoned, timedOut)
	}
	got, _ := st.GetSession(context.Background(), "s-answered")
	if got.Status != domain.SessionStatusActive {
		t.Errorf("s-answered status = %q, want active", got.Status)
	}
}

func TestSweepIgnoresCompleted(t *testing.T) {
	st := store.NewMemory()
	ended := time.Now()
	seedSession(t, st, domain.Session{
		ID:            "s-done",
		ApplicationID: "a1",
		TokenHash:     "hash-done",
		Status:        domain.SessionStatusCompleted,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     func() *time.Time { x := time.Now().Add(-2 * time.Hour); return &x }(),
		EndedAt:       &ended,
	})

	expired, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if expired != 0 || abandoned != 0 || timedOut != 0 {
		t.Errorf("expired=%d abandoned=%d timedOut=%d, want 0/0/0", expired, abandoned, timedOut)
	}
}

// raceStore reports every conditional transition as skipped, as if another
// writer changed the session between the sweeper's list and its update.
type raceStore struct{ store.Store }

func (raceStore) ExpireSession(context.Context, string, time.Time) (bool, error)  { return false, nil }
func (raceStore) AbandonSession(context.Context, string, time.Time) (bool, error) { return false, nil }
func (raceStore) FinishSession(context.Context, string, domain.FinishReason, time.Time) (bool, error) {
	return false, nil
}

func TestSweepCountsOnlySessionsItChanged(t *testing.T) {
	mem := store.NewMemory()
	started := time.Now().Add(-time.Hour)
	seedSession(t, mem, domain.Session{
		ID: "s-expired", ApplicationID: "a1", TokenHash: "hash-expired",
		Status: domain.SessionStatusInvited, ExpiresAt: time.Now().Add(-time.Hour),
	})
	seedSession(t, mem, domain.Session{
		ID: "s-stale", ApplicationID: "a2", TokenHash: "hash-stale",
		Status: domain.SessionStatusActive, ExpiresAt: time.Now().Add(time.Hour), StartedAt: &started,
	})

	expired, abandoned, timedOut, err := newSweeper(t, raceStore{mem}, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if expired != 0 || abandoned != 0 || timedOut != 0 {
		t.Errorf("expired, abandoned, timedOut = %d, %d, %d, want 0, 0, 0 when another writer got there first", expired, abandoned, timedOut)
	}
}

func TestSweepNoTopicsFailsSessionApplicationAndRun(t *testing.T) {
	st := store.NewMemory()
	seedApplication(t, st, domain.Application{ID: "a-no-topics", JobID: "j1", Name: "Budi", Email: "b@example.com", Status: domain.ApplicationStatusInSession})
	started := time.Now().Add(-12 * time.Minute)
	seedSession(t, st, domain.Session{
		ID:            "s-no-topics",
		ApplicationID: "a-no-topics",
		TokenHash:     "hash-no-topics",
		Status:        domain.SessionStatusActive,
		ExpiresAt:     time.Now().Add(24 * time.Hour),
		StartedAt:     &started,
	})
	run := domain.GraphRun{ID: "run-no-topics", SessionID: "s-no-topics", Status: domain.GraphRunStatusRunning}
	if err := st.CreateGraphRun(context.Background(), run); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	appendNodeEvent(t, st, run.ID, domain.StrPtr("planner"), domain.IntPtr(1), "node.started", map[string]any{})
	appendNodeEvent(t, st, run.ID, domain.StrPtr("planner"), domain.IntPtr(1), "node.failed", map[string]any{"error": "planner_failed", "will_retry": false})

	_, abandoned, timedOut, err := newSweeper(t, st, nil).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 0 || timedOut != 0 {
		t.Errorf("abandoned=%d timedOut=%d, want 0/0: a no-topics finish is failed, not abandoned", abandoned, timedOut)
	}

	got, _ := st.GetSession(context.Background(), "s-no-topics")
	if got.Status != domain.SessionStatusCompleted || got.FinishReason == nil || *got.FinishReason != domain.FinishReasonFailed {
		t.Errorf("session = %+v, want completed/failed: the plan never existed", got)
	}
	app, _ := st.GetApplication(context.Background(), "a-no-topics")
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application = %q, want failed so resend-invite works", app.Status)
	}
	gotRun, _ := st.GetGraphRun(context.Background(), run.ID)
	if gotRun.Status != domain.GraphRunStatusFailed {
		t.Errorf("run status = %q, want failed", gotRun.Status)
	}

	events, err := st.ListNodeEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	runFailedAt, endedAt, lastSkippedAt := -1, -1, -1
	skips := make(map[string]int)
	for i, event := range events {
		switch event.Type {
		case "node.skipped":
			if event.NodeID == nil {
				t.Errorf("node.skipped at %d has no node_id", i)
				continue
			}
			var payload nodeSkippedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal node.skipped: %v", err)
			}
			if payload.Reason != "run_failed" {
				t.Errorf("node.skipped(%s) reason = %q, want run_failed", *event.NodeID, payload.Reason)
			}
			if event.Attempt != nil {
				t.Errorf("node.skipped(%s) attempt = %d, want null", *event.NodeID, *event.Attempt)
			}
			skips[*event.NodeID]++
			lastSkippedAt = i
		case "run.failed":
			runFailedAt = i
		case "session.ended":
			endedAt = i
			var payload sessionEndedPayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal session.ended: %v", err)
			}
			if payload.FinishReason != string(domain.FinishReasonFailed) {
				t.Errorf("session.ended finish_reason = %q, want failed", payload.FinishReason)
			}
		}
	}
	for _, nodeID := range []string{"plan_check", "interviewer", "judge", "guard", "scorer", "evidence_check"} {
		if skips[nodeID] != 1 {
			t.Errorf("node.skipped(%s) = %d, want exactly 1 before run.failed", nodeID, skips[nodeID])
		}
	}
	for _, nodeID := range []string{"planner", "recruiter_gate"} {
		if skips[nodeID] != 0 {
			t.Errorf("node.skipped(%s) = %d, want 0", nodeID, skips[nodeID])
		}
	}
	if runFailedAt < 0 || endedAt < 0 {
		t.Fatalf("run.failed=%d session.ended=%d, want both persisted without a live runner", runFailedAt, endedAt)
	}
	if !(lastSkippedAt >= 0 && lastSkippedAt < runFailedAt && runFailedAt < endedAt) {
		t.Errorf("node.skipped last=%d run.failed=%d session.ended=%d, want node.skipped... < run.failed < session.ended",
			lastSkippedAt, runFailedAt, endedAt)
	}
}

func appendNodeEvent(t *testing.T, st store.Store, runID string, nodeID *string, attempt *int, eventType string, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if _, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, NodeID: nodeID, Attempt: attempt, Type: eventType, Payload: data,
	}); err != nil {
		t.Fatalf("AppendNodeEvent %s: %v", eventType, err)
	}
}
