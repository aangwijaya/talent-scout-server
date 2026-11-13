package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// manualRetryEvent is the exact shape asserted for one step of the §6.4
// manual-retry sequences.
type manualRetryEvent struct {
	typ     string
	node    string
	attempt int
	failed  *NodeFailed
	retry   *LoopRetry
	edge    *EdgeTraversed
}

func manualRetryRunEvents(t *testing.T, st store.Store, sessionID string) []domain.NodeEvent {
	t.Helper()
	run, err := st.GetGraphRunBySession(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetGraphRunBySession: %v", err)
	}
	all, err := st.ListNodeEvents(context.Background(), run.ID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	out := make([]domain.NodeEvent, 0, len(all))
	for _, event := range filteredRunEvents(all) {
		// The retryable error is a session-stream event (EVENTS.md §1/§2); the
		// §6.4 run sequence shows the applicant's POST /retry instead.
		if event.Type == EventError {
			continue
		}
		out = append(out, event)
	}
	return out
}

func assertManualRetrySequence(t *testing.T, events []domain.NodeEvent, from string, want []manualRetryEvent) {
	t.Helper()
	start := -1
	for i, e := range events {
		if e.Type == EventNodeStarted && e.NodeID != nil && *e.NodeID == from {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no node.started(%s) in events", from)
	}
	got := events[start:]
	if len(got) < len(want) {
		t.Fatalf("events from %s = %d, want at least %d", from, len(got), len(want))
	}
	for i, wantStep := range want {
		e := got[i]
		if e.Type != wantStep.typ {
			t.Fatalf("event[%d] type = %q, want %q", i, e.Type, wantStep.typ)
		}
		if wantStep.node != "" {
			if e.NodeID == nil || *e.NodeID != wantStep.node {
				t.Errorf("event[%d] %s node = %v, want %s", i, e.Type, e.NodeID, wantStep.node)
			}
		}
		if wantStep.attempt != 0 {
			if e.Attempt == nil || *e.Attempt != wantStep.attempt {
				t.Errorf("event[%d] %s attempt = %v, want %d", i, e.Type, e.Attempt, wantStep.attempt)
			}
		}
		switch {
		case wantStep.failed != nil:
			var payload NodeFailed
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("event[%d] unmarshal node.failed: %v", i, err)
			}
			if payload.WillRetry != wantStep.failed.WillRetry {
				t.Errorf("event[%d] node.failed will_retry = %v, want %v", i, payload.WillRetry, wantStep.failed.WillRetry)
			}
		case wantStep.retry != nil:
			var payload LoopRetry
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("event[%d] unmarshal loop.retry: %v", i, err)
			}
			if payload.Attempt != wantStep.retry.Attempt ||
				payload.MaxAttempts != wantStep.retry.MaxAttempts ||
				payload.Reason != wantStep.retry.Reason {
				t.Errorf("event[%d] loop.retry = %+v, want %+v", i, payload, *wantStep.retry)
			}
		case wantStep.edge != nil:
			var payload EdgeTraversed
			if err := json.Unmarshal(e.Payload, &payload); err != nil {
				t.Fatalf("event[%d] unmarshal edge.traversed: %v", i, err)
			}
			if payload != *wantStep.edge {
				t.Errorf("event[%d] edge.traversed = %+v, want %+v", i, payload, *wantStep.edge)
			}
		}
	}
}

// TestManualRetryInterviewerSequence matches the §6.4 interviewer sequence,
// including the 1× HTTP retry that precedes the applicant's manual retry.
func TestManualRetryInterviewerSequence(t *testing.T) {
	st, sess := seedGraphSession(t)
	var streamCalls int
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if req.Node != "interviewer" {
			content := FakeReplies[req.Node]
			if onDelta != nil {
				_ = onDelta(content)
			}
			return llm.Response{Content: content}, nil
		}
		streamCalls++
		if streamCalls == 1 {
			if req.OnRetry != nil {
				req.OnRetry(1, llm.ErrUnavailable)
			}
			return llm.Response{}, llm.ErrUnavailable
		}
		content := FakeReplies["interviewer"]
		if onDelta != nil {
			_ = onDelta(content)
		}
		return llm.Response{Content: content}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	httpRetries := testModels().MaxRetries()
	want := []manualRetryEvent{
		{typ: EventNodeStarted, node: "interviewer", attempt: 1},
		{typ: EventNodeFailed, node: "interviewer", attempt: 1, failed: &NodeFailed{WillRetry: true}},
		{typ: EventNodeStarted, node: "interviewer", attempt: 2},
		{typ: EventNodeFailed, node: "interviewer", attempt: 2, failed: &NodeFailed{WillRetry: false}},
		{typ: EventLoopRetry, node: "interviewer", attempt: 3, retry: &LoopRetry{Attempt: 3, MaxAttempts: 3 + httpRetries, Reason: "llm_unavailable"}},
		{typ: EventEdgeTraversed, edge: &EdgeTraversed{From: "interviewer", To: "interviewer", Label: "retry"}},
		{typ: EventNodeStarted, node: "interviewer", attempt: 3},
		{typ: EventMessageCompleted, node: "interviewer", attempt: 3},
		{typ: EventNodeCompleted, node: "interviewer", attempt: 3},
	}
	events := manualRetryRunEvents(t, st, sess.ID)
	assertManualRetrySequence(t, events, "interviewer", want)

	// The message from the manual retry carries the same non-zero created_at
	// that was stored (EVENTS.md: message.completed = { "message": Message }).
	stored, err := st.ListMessagesBySession(context.Background(), sess.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	var checked bool
	for _, e := range events {
		if e.Type != EventMessageCompleted {
			continue
		}
		var payload MessageCompleted
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("unmarshal message.completed: %v", err)
		}
		if payload.Message.CreatedAt.IsZero() {
			t.Fatalf("manual-retry message.completed has a zero created_at")
		}
		// Postgres timestamptz keeps microseconds; a finer emitted value would
		// differ from the one read back later.
		if payload.Message.CreatedAt.Nanosecond()%1000 != 0 {
			t.Errorf("created_at %v has sub-microsecond precision", payload.Message.CreatedAt)
		}
		for _, m := range stored {
			if m.ID == payload.Message.ID {
				checked = true
				if !m.CreatedAt.Equal(payload.Message.CreatedAt) {
					t.Errorf("emitted created_at %v != stored %v", payload.Message.CreatedAt, m.CreatedAt)
				}
			}
		}
	}
	if !checked {
		t.Fatal("no stored message matched the manual-retry message.completed")
	}
}

// TestManualRetryPlannerSequence matches the §6.4 planner sequence and the
// transitions that follow a recovered planner, including plan_check reusing the
// retry attempt number.
func TestManualRetryPlannerSequence(t *testing.T) {
	st, sess := seedGraphSession(t)
	var plannerCalls int
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node != "planner" {
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
		plannerCalls++
		if plannerCalls == 1 {
			if req.OnRetry != nil {
				req.OnRetry(1, llm.ErrUnavailable)
			}
			return llm.Response{}, llm.ErrUnavailable
		}
		return llm.Response{Content: FakeReplies["planner"]}, nil
	}}
	manager, _ := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	httpRetries := testModels().MaxRetries()
	want := []manualRetryEvent{
		{typ: EventNodeStarted, node: "planner", attempt: 1},
		{typ: EventNodeFailed, node: "planner", attempt: 1, failed: &NodeFailed{WillRetry: true}},
		{typ: EventNodeStarted, node: "planner", attempt: 2},
		{typ: EventNodeFailed, node: "planner", attempt: 2, failed: &NodeFailed{WillRetry: false}},
		{typ: EventLoopRetry, node: "planner", attempt: 3, retry: &LoopRetry{Attempt: 3, MaxAttempts: 3 + httpRetries, Reason: "llm_unavailable"}},
		{typ: EventEdgeTraversed, edge: &EdgeTraversed{From: "planner", To: "planner", Label: "retry"}},
		{typ: EventNodeStarted, node: "planner", attempt: 3},
		{typ: EventNodeCompleted, node: "planner", attempt: 3},
		{typ: EventEdgeTraversed, edge: &EdgeTraversed{From: "planner", To: "plan_check", Label: "planned"}},
		{typ: EventNodeStarted, node: "plan_check", attempt: 3},
		{typ: EventNodeCompleted, node: "plan_check", attempt: 3},
		{typ: EventCheckPassed, node: "plan_check", attempt: 3},
		{typ: EventTopicChanged, node: "plan_check", attempt: 3},
		{typ: EventEdgeTraversed, edge: &EdgeTraversed{From: "plan_check", To: "interviewer", Label: "valid"}},
		{typ: EventNodeStarted, node: "interviewer", attempt: 1},
		{typ: EventMessageCompleted, node: "interviewer", attempt: 1},
		{typ: EventNodeCompleted, node: "interviewer", attempt: 1},
	}
	assertManualRetrySequence(t, manualRetryRunEvents(t, st, sess.ID), "planner", want)
}
