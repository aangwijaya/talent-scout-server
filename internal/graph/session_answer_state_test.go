package graph

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// eventGate pauses one matching node event until release is closed.
type eventGate struct {
	eventType  string
	nodeID     string
	occurrence int

	mu      sync.Mutex
	seen    int
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func newEventGate(eventType, nodeID string, occurrence int) *eventGate {
	return &eventGate{
		eventType:  eventType,
		nodeID:     nodeID,
		occurrence: occurrence,
		entered:    make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (g *eventGate) matches(event domain.NodeEvent) bool {
	return event.Type == g.eventType && event.NodeID != nil && *event.NodeID == g.nodeID
}

type gatedGraphStore struct {
	store.Store
	gates []*eventGate
}

func (s *gatedGraphStore) AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	for _, gate := range s.gates {
		if !gate.matches(event) {
			continue
		}
		gate.mu.Lock()
		gate.seen++
		block := gate.seen == gate.occurrence
		gate.mu.Unlock()
		if block {
			gate.once.Do(func() { close(gate.entered) })
			<-gate.release
		}
	}
	return s.Store.AppendNodeEvent(ctx, event)
}

func waitGateEntered(t *testing.T, entered <-chan struct{}) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the run to reach the gated event")
	}
}

// waitAwaitingClaimed waits until an in-flight SubmitAnswer has passed its
// initial state check (which flips awaiting to false) and is waiting on stepMu.
func waitAwaitingClaimed(t *testing.T, manager *SessionManager, sessionID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		manager.mu.Lock()
		st := manager.states[sessionID]
		claimed := st != nil && !st.awaiting
		manager.mu.Unlock()
		if claimed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("SubmitAnswer never claimed the awaiting slot")
		}
		time.Sleep(time.Millisecond)
	}
}

// TestSubmitAnswerKeepsRegistryMarkForContinuation reproduces the window the
// awaiting fix opened: the turn tail removes the registry mark when it goes
// idle, so the answer must claim it again before continueAfterAnswer runs or
// the sweeper may finish the session mid-judge.
func TestSubmitAnswerKeepsRegistryMarkForContinuation(t *testing.T) {
	base, sess := seedGraphSession(t)
	tail := newEventGate(EventNodeCompleted, "interviewer", 1)
	judge := newEventGate(EventNodeStarted, "judge", 1)
	st := &gatedGraphStore{Store: base, gates: []*eventGate{tail, judge}}
	manager, registry := newTestManager(t, st, &stubClient{})

	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitGateEntered(t, tail.entered)

	errCh := make(chan error, 1)
	go func() {
		_, err := manager.SubmitAnswer(context.Background(), sess, "I optimized rendering.")
		errCh <- err
	}()
	waitAwaitingClaimed(t, manager, sess.ID)

	close(tail.release)
	waitGateEntered(t, judge.entered)

	if !registry.Has(sess.ID) {
		t.Error("registry has no mark while continueAfterAnswer runs; the sweeper could finish the session mid-turn")
	}

	close(judge.release)
	if err := <-errCh; err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)
}

// TestSubmitAnswerReturnsEndedWhenSessionFinishesWhileWaiting proves a request
// that waited on stepMu and then found the session finished returns the ended
// error (410), not invalid_state (409).
func TestSubmitAnswerReturnsEndedWhenSessionFinishesWhileWaiting(t *testing.T) {
	st, sess := seedGraphSession(t)
	manager, _ := newTestManager(t, st, &stubClient{})
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	manager.mu.Lock()
	live := manager.states[sess.ID]
	manager.mu.Unlock()
	live.stepMu.Lock()

	errCh := make(chan error, 1)
	go func() {
		_, err := manager.SubmitAnswer(context.Background(), sess, "I optimized rendering.")
		errCh <- err
	}()
	waitAwaitingClaimed(t, manager, sess.ID)

	manager.mu.Lock()
	live.done = true
	manager.mu.Unlock()
	live.stepMu.Unlock()

	select {
	case err := <-errCh:
		if !errors.Is(err, ErrSessionEnded) {
			t.Fatalf("SubmitAnswer = %v, want ErrSessionEnded", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitAnswer did not return after the session ended")
	}
}

// TestSubmitAnswerWaitHonoursCancellation proves a cancelled request does not
// block for a whole foreground step and leaves the awaiting state intact.
func TestSubmitAnswerWaitHonoursCancellation(t *testing.T) {
	st, sess := seedGraphSession(t)
	manager, registry := newTestManager(t, st, &stubClient{})
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	manager.mu.Lock()
	live := manager.states[sess.ID]
	manager.mu.Unlock()
	live.stepMu.Lock()

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, err := manager.SubmitAnswer(ctx, sess, "I optimized rendering.")
		errCh <- err
	}()
	waitAwaitingClaimed(t, manager, sess.ID)
	cancel()

	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SubmitAnswer = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SubmitAnswer ignored the cancelled request")
	}

	manager.mu.Lock()
	awaiting := live.awaiting
	manager.mu.Unlock()
	if !awaiting {
		t.Error("awaiting was not restored after the cancelled request")
	}
	if registry.Has(sess.ID) {
		t.Error("cancelled answer left an idle session marked in flight; the sweeper would never finish it")
	}
	live.stepMu.Unlock()
}
