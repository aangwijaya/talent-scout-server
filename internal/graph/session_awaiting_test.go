package graph

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/llm"
)

func waitManagerState(t *testing.T, manager *SessionManager, sessionID, what string, pred func(*liveSession) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		manager.mu.Lock()
		st := manager.states[sessionID]
		ok := st != nil && pred(st)
		manager.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestAwaitingFalseWhileReplyInFlightThenTrue drives the real manager state, not
// a stub: the upload check must be false until the opening reply completes.
func TestAwaitingFalseWhileReplyInFlightThenTrue(t *testing.T) {
	base, sess := seedGraphSession(t)
	gate := newEventGate(EventNodeStarted, "interviewer", 1)
	st := &gatedGraphStore{Store: base, gates: []*eventGate{gate}}
	manager, _ := newTestManager(t, st, &stubClient{})

	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitGateEntered(t, gate.entered)
	if manager.Awaiting(sess.ID) {
		t.Error("Awaiting = true while the opening reply is still in flight")
	}

	close(gate.release)
	waitIdle(t, manager, sess.ID)
	if !manager.Awaiting(sess.ID) {
		t.Error("Awaiting = false after the opening reply completed")
	}
}

// TestAwaitingFalseWhileRetryPendingAndInFlight proves a manual retry keeps the
// upload check closed until the retried reply actually completes.
func TestAwaitingFalseWhileRetryPendingAndInFlight(t *testing.T) {
	base, sess := seedGraphSession(t)
	gate := newEventGate(EventNodeStarted, "interviewer", 2)
	st := &gatedGraphStore{Store: base, gates: []*eventGate{gate}}
	var calls atomic.Int32
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if calls.Add(1) == 1 {
			return llm.Response{}, llm.ErrUnavailable
		}
		content := FakeReplies[req.Node]
		if onDelta != nil {
			if err := onDelta(content); err != nil {
				return llm.Response{}, err
			}
		}
		return llm.Response{Content: content}, nil
	}}
	manager, _ := newTestManager(t, st, client)

	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitManagerState(t, manager, sess.ID, "the interviewer to offer a retry", func(st *liveSession) bool {
		return st.waitingRetry && !st.busy
	})
	if manager.Awaiting(sess.ID) {
		t.Error("Awaiting = true while a retry is pending")
	}

	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitGateEntered(t, gate.entered)
	if manager.Awaiting(sess.ID) {
		t.Error("Awaiting = true while the retried reply is in flight")
	}

	close(gate.release)
	waitIdle(t, manager, sess.ID)
	if !manager.Awaiting(sess.ID) {
		t.Error("Awaiting = false after the retried reply completed")
	}
}
