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

func TestReconcileSkipsMachineNodesThatNeverExecuted(t *testing.T) {
	st := store.NewMemory()
	_, run := seedOrphan(t, st, true, "interviewer")

	if _, err := ReconcileOrphanedRuns(context.Background(), st); err != nil {
		t.Fatalf("ReconcileOrphanedRuns: %v", err)
	}
	events, err := st.ListNodeEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}

	skips := make(map[string]int)
	var failedAt, firstSkippedAt, lastSkippedAt, runFailedAt = -1, -1, -1, -1
	for i, event := range events {
		switch event.Type {
		case EventNodeFailed:
			if event.NodeID != nil && *event.NodeID == "interviewer" {
				failedAt = i
			}
		case EventNodeSkipped:
			if event.NodeID == nil {
				t.Errorf("node.skipped at %d has no node_id", i)
				continue
			}
			var payload NodeSkipped
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
			if firstSkippedAt < 0 {
				firstSkippedAt = i
			}
			lastSkippedAt = i
		case EventRunFailed:
			runFailedAt = i
		}
	}

	for _, nodeID := range []string{"plan_check", "judge", "guard", "scorer", "evidence_check"} {
		if skips[nodeID] != 1 {
			t.Errorf("node.skipped(%s) = %d, want exactly 1 (never executed)", nodeID, skips[nodeID])
		}
	}
	for _, nodeID := range []string{"planner", "interviewer", "recruiter_gate"} {
		if skips[nodeID] != 0 {
			t.Errorf("node.skipped(%s) = %d, want 0", nodeID, skips[nodeID])
		}
	}
	if !(failedAt >= 0 && firstSkippedAt > failedAt && lastSkippedAt < runFailedAt) {
		t.Errorf("order node.failed=%d first skipped=%d last skipped=%d run.failed=%d, want node.failed < node.skipped... < run.failed",
			failedAt, firstSkippedAt, lastSkippedAt, runFailedAt)
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

func TestReconcileFailsScoringApplicationAfterAbandonedSession(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	sess, run := seedOrphan(t, st, true)
	for _, node := range []string{"plan_check", "interviewer", "judge", "guard"} {
		appendEvent(t, st, run.ID, domain.StrPtr(node), domain.IntPtr(1), EventNodeStarted, NodeStarted{})
		appendEvent(t, st, run.ID, domain.StrPtr(node), domain.IntPtr(1), EventNodeCompleted, NodeCompleted{OutputSummary: "ok"})
	}
	appendEvent(t, st, run.ID, domain.StrPtr("scorer"), domain.IntPtr(1), EventNodeStarted, NodeStarted{})
	if changed, err := st.AbandonSession(ctx, sess.ID, time.Now().UTC()); err != nil || !changed {
		t.Fatalf("AbandonSession = %v, %v; want true, nil", changed, err)
	}
	if app, _ := st.GetApplication(ctx, sess.ApplicationID); app.Status != domain.ApplicationStatusScoring {
		t.Fatalf("precondition: application = %q, want scoring", app.Status)
	}

	result, err := ReconcileOrphanedRuns(ctx, st)
	if err != nil {
		t.Fatalf("ReconcileOrphanedRuns: %v", err)
	}
	if result.RunsClosed != 1 || result.NodesClosed != 1 || result.SessionsClosed != 0 {
		t.Fatalf("result = %+v, want 1 run, 1 node, 0 sessions (already completed)", result)
	}

	app, _ := st.GetApplication(ctx, sess.ApplicationID)
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application = %q, want failed (was scoring)", app.Status)
	}
	gotRun, _ := st.GetGraphRun(ctx, run.ID)
	if gotRun.Status != domain.GraphRunStatusFailed || gotRun.Error == nil || *gotRun.Error != restartFailureReason {
		t.Errorf("run = %+v, want failed/%s", gotRun, restartFailureReason)
	}

	events, err := st.ListNodeEvents(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	// Boot-appended tail: node.failed(scorer) → node.skipped(evidence_check) → run.failed.
	var tail []string
	for _, event := range events[len(events)-3:] {
		node := "-"
		if event.NodeID != nil {
			node = *event.NodeID
		}
		tail = append(tail, event.Type+":"+node)
	}
	want := []string{EventNodeFailed + ":scorer", EventNodeSkipped + ":evidence_check", EventRunFailed + ":-"}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("boot events = %v, want %v", tail, want)
		}
	}
	var failed NodeFailed
	if err := json.Unmarshal(events[len(events)-3].Payload, &failed); err != nil {
		t.Fatalf("unmarshal node.failed: %v", err)
	}
	if failed.Error != restartFailureReason || failed.WillRetry {
		t.Errorf("node.failed = %+v, want server_restart/will_retry=false", failed)
	}
	if a := events[len(events)-3].Attempt; a == nil || *a != 1 {
		t.Errorf("node.failed attempt = %v, want 1", a)
	}
	var skipped NodeSkipped
	if err := json.Unmarshal(events[len(events)-2].Payload, &skipped); err != nil || skipped.Reason != "run_failed" {
		t.Errorf("node.skipped = %+v, %v; want run_failed", skipped, err)
	}
	var runFailed RunFailed
	if err := json.Unmarshal(events[len(events)-1].Payload, &runFailed); err != nil || runFailed.Error != restartFailureReason {
		t.Errorf("run.failed = %+v, %v; want server_restart", runFailed, err)
	}
	for _, event := range events {
		if event.Type == EventNodeSkipped && event.NodeID != nil && *event.NodeID == "recruiter_gate" {
			t.Error("recruiter_gate got node.skipped, want none")
		}
		if event.Type == EventSessionEnded {
			t.Error("session.ended emitted for a session that was already completed")
		}
	}
}

func TestReconcileIsIdempotentAcrossBoots(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	_, run := seedOrphan(t, st, true, "interviewer", "judge")

	first, err := ReconcileOrphanedRuns(ctx, st)
	if err != nil {
		t.Fatalf("first ReconcileOrphanedRuns: %v", err)
	}
	if first.RunsClosed != 1 || first.SessionsClosed != 1 || first.NodesClosed != 2 {
		t.Fatalf("first result = %+v, want 1 run, 1 session, 2 nodes", first)
	}
	afterFirst, err := st.ListNodeEvents(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}

	var failedNodes []string
	for _, event := range afterFirst {
		if event.Type != EventNodeFailed {
			continue
		}
		var payload NodeFailed
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal node.failed: %v", err)
		}
		if payload.Error != restartFailureReason || payload.WillRetry {
			t.Errorf("node.failed(%s) = %+v, want server_restart/will_retry=false", *event.NodeID, payload)
		}
		if event.Attempt == nil || *event.Attempt != 1 {
			t.Errorf("node.failed(%s) attempt = %v, want 1", *event.NodeID, event.Attempt)
		}
		failedNodes = append(failedNodes, *event.NodeID)
	}
	if len(failedNodes) != 2 || failedNodes[0] != "interviewer" || failedNodes[1] != "judge" {
		t.Fatalf("node.failed order = %v, want [interviewer judge] (start order)", failedNodes)
	}

	second, err := ReconcileOrphanedRuns(ctx, st)
	if err != nil {
		t.Fatalf("second ReconcileOrphanedRuns: %v", err)
	}
	if second != (ReconcileResult{}) {
		t.Errorf("second result = %+v, want zero", second)
	}
	afterSecond, err := st.ListNodeEvents(ctx, run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	if len(afterSecond) != len(afterFirst) {
		t.Errorf("node_events after second boot = %d, want unchanged %d", len(afterSecond), len(afterFirst))
	}
}
