package graph

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func seedOrphan(t *testing.T, st *store.Memory, withRun bool, dangling ...string) (domain.Session, domain.GraphRun) {
	t.Helper()
	ctx := context.Background()
	app := domain.Application{
		ID: "app-orphan", JobID: "job-orphan", Name: "Budi", Email: "budi@example.com",
		Status: domain.ApplicationStatusInSession,
	}
	if err := st.CreateApplication(ctx, app); err != nil {
		t.Fatalf("CreateApplication: %v", err)
	}
	started := time.Now().Add(-5 * time.Minute).UTC()
	sess := domain.Session{
		ID: "sess-orphan", ApplicationID: app.ID, TokenHash: "hash-orphan",
		Status: domain.SessionStatusActive, ExpiresAt: time.Now().Add(time.Hour), StartedAt: &started,
	}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if !withRun {
		return sess, domain.GraphRun{}
	}
	run := domain.GraphRun{ID: "run-orphan", SessionID: sess.ID, Status: domain.GraphRunStatusRunning}
	if err := st.CreateGraphRun(ctx, run); err != nil {
		t.Fatalf("CreateGraphRun: %v", err)
	}
	appendEvent(t, st, run.ID, nil, nil, EventRunStarted, RunStarted{SessionID: sess.ID})
	appendEvent(t, st, run.ID, domain.StrPtr("planner"), domain.IntPtr(1), EventNodeStarted, NodeStarted{})
	appendEvent(t, st, run.ID, domain.StrPtr("planner"), domain.IntPtr(1), EventNodeCompleted, NodeCompleted{OutputSummary: "ok"})
	for _, node := range dangling {
		appendEvent(t, st, run.ID, domain.StrPtr(node), domain.IntPtr(1), EventNodeStarted, NodeStarted{})
	}
	return sess, run
}

func appendEvent(t *testing.T, st store.Store, runID string, nodeID *string, attempt *int, eventType string, payload any) domain.NodeEvent {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	event, err := st.AppendNodeEvent(context.Background(), domain.NodeEvent{
		RunID: runID, NodeID: nodeID, Attempt: attempt, Type: eventType, Payload: data,
	})
	if err != nil {
		t.Fatalf("AppendNodeEvent %s: %v", eventType, err)
	}
	return event
}

func TestReconcileClosesOrphanedRunNodesAndApplication(t *testing.T) {
	st := store.NewMemory()
	sess, run := seedOrphan(t, st, true, "interviewer")

	result, err := ReconcileOrphanedRuns(context.Background(), st)
	if err != nil {
		t.Fatalf("ReconcileOrphanedRuns: %v", err)
	}
	if result.RunsClosed != 1 || result.SessionsClosed != 1 || result.NodesClosed != 1 {
		t.Fatalf("result = %+v, want 1 run, 1 session, 1 node", result)
	}

	events, err := st.ListNodeEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var nodeFailedAt, runFailedAt, endedAt = -1, -1, -1
	for i, event := range events {
		switch event.Type {
		case EventNodeFailed:
			if event.NodeID != nil && *event.NodeID == "interviewer" {
				var payload NodeFailed
				if err := json.Unmarshal(event.Payload, &payload); err != nil {
					t.Fatalf("unmarshal node.failed: %v", err)
				}
				if payload.WillRetry {
					t.Error("orphaned node terminal event has will_retry=true")
				}
				if payload.Error != restartFailureReason {
					t.Errorf("node.failed error = %q, want %q", payload.Error, restartFailureReason)
				}
				nodeFailedAt = i
			}
		case EventRunFailed:
			var payload RunFailed
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal run.failed: %v", err)
			}
			if payload.Error != restartFailureReason {
				t.Errorf("run.failed error = %q, want %q", payload.Error, restartFailureReason)
			}
			runFailedAt = i
		case EventSessionEnded:
			var payload SessionEnded
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal session.ended: %v", err)
			}
			if payload.FinishReason != string(domain.FinishReasonFailed) {
				t.Errorf("session.ended finish_reason = %q, want failed", payload.FinishReason)
			}
			endedAt = i
		}
	}
	if nodeFailedAt < 0 {
		t.Fatal("orphaned node.started has no terminal node.failed")
	}
	if runFailedAt < 0 || endedAt < 0 {
		t.Fatalf("run.failed=%d session.ended=%d, want both", runFailedAt, endedAt)
	}
	if !(nodeFailedAt < runFailedAt && runFailedAt < endedAt) {
		t.Errorf("order node.failed=%d run.failed=%d session.ended=%d, want terminal < run.failed < session.ended", nodeFailedAt, runFailedAt, endedAt)
	}

	plannerTerminals := 0
	for _, event := range events {
		if event.NodeID != nil && *event.NodeID == "planner" && (event.Type == EventNodeCompleted || event.Type == EventNodeFailed) {
			plannerTerminals++
		}
	}
	if plannerTerminals != 1 {
		t.Errorf("planner terminal events = %d, want exactly 1", plannerTerminals)
	}

	gotRun, _ := st.GetGraphRun(context.Background(), run.ID)
	if gotRun.Status != domain.GraphRunStatusFailed || gotRun.Error == nil || *gotRun.Error != restartFailureReason {
		t.Errorf("run = %+v, want failed/%s", gotRun, restartFailureReason)
	}
	gotSess, _ := st.GetSession(context.Background(), sess.ID)
	if gotSess.Status != domain.SessionStatusCompleted || gotSess.FinishReason == nil || *gotSess.FinishReason != domain.FinishReasonFailed {
		t.Errorf("session = %+v, want completed/failed", gotSess)
	}
	app, _ := st.GetApplication(context.Background(), sess.ApplicationID)
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application = %q, want failed so resend-invite works", app.Status)
	}
}

func TestReconcileFinishesActiveSessionWithoutRun(t *testing.T) {
	st := store.NewMemory()
	sess, _ := seedOrphan(t, st, false)

	result, err := ReconcileOrphanedRuns(context.Background(), st)
	if err != nil {
		t.Fatalf("ReconcileOrphanedRuns: %v", err)
	}
	if result.RunsClosed != 0 || result.SessionsClosed != 1 {
		t.Fatalf("result = %+v, want 1 session and no run", result)
	}
	got, _ := st.GetSession(context.Background(), sess.ID)
	if got.Status != domain.SessionStatusCompleted || got.FinishReason == nil || *got.FinishReason != domain.FinishReasonFailed {
		t.Errorf("session = %+v, want completed/failed", got)
	}
	app, _ := st.GetApplication(context.Background(), sess.ApplicationID)
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application = %q, want failed", app.Status)
	}
}

func TestReconcileDoesNotDuplicateClosedNode(t *testing.T) {
	st := store.NewMemory()
	_, run := seedOrphan(t, st, true)

	if _, err := ReconcileOrphanedRuns(context.Background(), st); err != nil {
		t.Fatalf("ReconcileOrphanedRuns: %v", err)
	}
	events, _ := st.ListNodeEvents(context.Background(), run.ID, 0)
	terminals := 0
	for _, event := range events {
		if event.Type == EventNodeFailed {
			terminals++
		}
	}
	if terminals != 0 {
		t.Errorf("node.failed events = %d, want 0 when every started node already has a terminal event", terminals)
	}
}
