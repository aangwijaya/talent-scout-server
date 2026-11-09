package graph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// endOrderStore records whether session.ended was appended before the session
// and application rows reached their failed state.
type endOrderStore struct {
	*store.Memory
	sessionID string
	appID     string
	badOrder  bool
}

func (s *endOrderStore) AppendNodeEvent(ctx context.Context, event domain.NodeEvent) (domain.NodeEvent, error) {
	if event.Type == EventSessionEnded {
		sess, sessErr := s.Memory.GetSession(ctx, s.sessionID)
		app, appErr := s.Memory.GetApplication(ctx, s.appID)
		if sessErr != nil || appErr != nil ||
			sess.Status != domain.SessionStatusCompleted ||
			sess.FinishReason == nil || *sess.FinishReason != domain.FinishReasonFailed ||
			app.Status != domain.ApplicationStatusFailed {
			s.badOrder = true
		}
	}
	return s.Memory.AppendNodeEvent(ctx, event)
}

func TestFailRunUpdatesRowsBeforeSessionEnded(t *testing.T) {
	base, sess := seedGraphSession(t)
	st := &endOrderStore{Memory: base, sessionID: sess.ID, appID: sess.ApplicationID}
	client := &stubClient{stream: func(req llm.Request, onDelta func(string) error) (llm.Response, error) {
		if req.Node == "interviewer" {
			return llm.Response{}, llm.ErrUnavailable
		}
		content := FakeReplies[req.Node]
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

	if st.badOrder {
		t.Error("session.ended was emitted before the session and application rows were updated")
	}
}

// graphRunFailStore fails the first write of a run so manager.Start cannot
// create the graph run.
type graphRunFailStore struct{ *store.Memory }

func (graphRunFailStore) CreateGraphRun(context.Context, domain.GraphRun) error {
	return errors.New("boom")
}

func TestStartFailureFinishesSessionAndApplication(t *testing.T) {
	base, sess := seedGraphSession(t)
	st := graphRunFailStore{Memory: base}
	manager, _ := newTestManager(t, st, &stubClient{})
	if err := manager.Start(context.Background(), sess); err == nil {
		t.Fatal("Start = nil, want an error when the graph run cannot be created")
	}

	fresh, _ := base.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusCompleted || fresh.FinishReason == nil || *fresh.FinishReason != domain.FinishReasonFailed {
		t.Errorf("session = %+v, want completed/failed so the applicant is not stranded active", fresh)
	}
	app, _ := base.GetApplication(context.Background(), sess.ApplicationID)
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application status = %q, want failed so resend-invite works", app.Status)
	}
}

func TestManualRetryContinuesInterviewerAttempts(t *testing.T) {
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
			// Simulate one transport retry that also fails: the client invokes
			// OnRetry before its second HTTP attempt.
			if req.OnRetry != nil {
				req.OnRetry(1, llm.ErrUnavailable)
			}
			return llm.Response{}, llm.ErrUnavailable
		}
		content := FakeReplies["interviewer"]
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
	waitIdle(t, manager, sess.ID)
	if err := manager.Retry(context.Background(), sess); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	var attempts []int
	var retryAttempt int
	for _, event := range sessionEvents(t, st, sess.ID) {
		switch {
		case event.Type == EventNodeStarted && event.NodeID != nil && *event.NodeID == "interviewer" && event.Attempt != nil:
			attempts = append(attempts, *event.Attempt)
		case event.Type == EventLoopRetry && event.Attempt != nil:
			retryAttempt = *event.Attempt
		}
	}
	if want := []int{1, 2, 3}; !slices.Equal(attempts, want) {
		t.Errorf("interviewer node.started attempts = %v, want %v", attempts, want)
	}
	if retryAttempt != 3 {
		t.Errorf("loop.retry attempt = %d, want 3", retryAttempt)
	}
}

func TestSweepEndsIdleLiveSessionThroughManager(t *testing.T) {
	st, sess := seedGraphSession(t)
	manager, registry := newTestManager(t, st, &stubClient{})
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	// Move the session past its time limit while its runner is idle (between
	// answers). The sweeper must finish it and emit session.ended through the
	// live manager instead of persisting a duplicate.
	fresh, err := st.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	past := time.Now().Add(-20 * time.Minute)
	fresh.StartedAt = &past
	if err := st.UpdateSession(context.Background(), fresh); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	sweeper := session.NewSweeper(st, registry, 15*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, _, timedOut, err := sweeper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if timedOut != 1 {
		t.Fatalf("timedOut = %d, want 1", timedOut)
	}

	var ended bool
	for _, event := range sessionEvents(t, st, sess.ID) {
		if event.Type == EventSessionEnded {
			ended = true
		}
	}
	if !ended {
		t.Error("no session.ended event persisted by the live manager")
	}
	fresh, _ = st.GetSession(context.Background(), sess.ID)
	if fresh.Status != domain.SessionStatusCompleted || fresh.FinishReason == nil || *fresh.FinishReason != domain.FinishReasonTimeLimit {
		t.Errorf("session = %+v, want completed/time_limit", fresh)
	}
	if err := manager.Retry(context.Background(), sess); !errors.Is(err, ErrInvalidState) {
		t.Errorf("Retry after sweep = %v, want ErrInvalidState", err)
	}
}

// TestSweepScoresAsyncAndEmitsGuardEdge proves the sweeper scoring path runs in
// its own goroutine (ARCHITECTURE §3 step 8: "The scorer runs async") instead
// of blocking the sweep loop, and emits the same guard→scorer transition as
// the in-process path before node.started(scorer).
func TestSweepScoresAsyncAndEmitsGuardEdge(t *testing.T) {
	st, sess := seedGraphSession(t)
	scorerEntered := make(chan struct{})
	release := make(chan struct{}, 1)
	entered := false
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		switch req.Node {
		case "judge":
			return sufficientJudgeReply()
		case "scorer":
			if !entered {
				entered = true
				close(scorerEntered)
			}
			<-release
			return llm.Response{Content: `{"scores":[
				{"rubric_key":"frontend_depth","score":4,"rationale":"Svelte work.","evidence":[{"message_id":"m1","quote":"I built the frontend with Svelte"}]}
			]}`}, nil
		default:
			return llm.Response{Content: FakeReplies[req.Node]}, nil
		}
	}}
	manager, registry := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	if _, err := manager.SubmitAnswer(context.Background(), sess, "I built the frontend with Svelte."); err != nil {
		t.Fatalf("SubmitAnswer: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	fresh, err := st.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	past := time.Now().Add(-20 * time.Minute)
	fresh.StartedAt = &past
	if err := st.UpdateSession(context.Background(), fresh); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	sweeper := session.NewSweeper(st, registry, 15*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sweepErr := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		_, _, _, err := sweeper.SweepOnce(context.Background())
		sweepErr <- err
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		release <- struct{}{}
		<-done
		t.Fatal("SweepOnce blocked while the scorer was still running; scoring must be async")
	}
	if err := <-sweepErr; err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	select {
	case <-scorerEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("the scorer never started after the sweep finished the session")
	}
	app, err := st.GetApplication(context.Background(), sess.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusScoring {
		t.Errorf("application status while the scorer is blocked = %q, want scoring", app.Status)
	}

	release <- struct{}{}

	deadline := time.Now().Add(3 * time.Second)
	for {
		app, err = st.GetApplication(context.Background(), sess.ApplicationID)
		if err != nil {
			t.Fatalf("GetApplication: %v", err)
		}
		if app.Status == domain.ApplicationStatusReview {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("application status = %q, want review after async scoring", app.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}

	events := sessionEvents(t, st, sess.ID)
	edgeAt, scorerAt := -1, -1
	for i, event := range events {
		switch event.Type {
		case EventEdgeTraversed:
			var payload EdgeTraversed
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal edge: %v", err)
			}
			if payload.From == "guard" && payload.To == "scorer" && payload.Label == "finish" {
				edgeAt = i
			}
		case EventNodeStarted:
			if event.NodeID != nil && *event.NodeID == "scorer" {
				scorerAt = i
			}
		}
	}
	if edgeAt < 0 {
		t.Fatal("no edge.traversed(guard→scorer, finish) on the sweeper scoring path")
	}
	if scorerAt < 0 {
		t.Fatal("no node.started(scorer) on the sweeper scoring path")
	}
	if edgeAt > scorerAt {
		t.Errorf("edge.traversed(guard→scorer) at %d after node.started(scorer) at %d, want before", edgeAt, scorerAt)
	}
}

// TestScoringFailureSkipsEvidenceCheckBeforeRunFailed proves a scorer transport
// failure on the final attempt marks evidence_check skipped before run.failed
// (docs/EVENTS.md §6.3) and leaves the application failed.
func TestScoringFailureSkipsEvidenceCheckBeforeRunFailed(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &stubClient{chat: func(req llm.Request) (llm.Response, error) {
		if req.Node == "scorer" {
			return llm.Response{}, llm.ErrUnavailable
		}
		if req.Node == "judge" {
			return sufficientJudgeReply()
		}
		return llm.Response{Content: FakeReplies[req.Node]}, nil
	}}
	manager := scoringManager(t, st, client, 15)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)
	finishWithAnswers(t, manager, sess,
		"I built the frontend with Svelte.",
		"I designed the system boundaries.",
		"I tested accessibility carefully and explained tradeoffs.",
	)

	events := sessionEvents(t, st, sess.ID)
	var lastScorerFailed, skippedAt, runFailedAt int = -1, -1, -1
	for i, event := range events {
		switch event.Type {
		case EventNodeFailed:
			if event.NodeID != nil && *event.NodeID == "scorer" {
				lastScorerFailed = i
			}
		case EventNodeSkipped:
			if event.NodeID != nil && *event.NodeID == "evidence_check" {
				skippedAt = i
			}
		case EventRunFailed:
			runFailedAt = i
		}
	}
	if lastScorerFailed < 0 {
		t.Fatal("no node.failed(scorer) event")
	}
	if skippedAt < 0 {
		t.Fatal("no node.skipped(evidence_check) before run.failed")
	}
	if runFailedAt < 0 {
		t.Fatal("no run.failed event")
	}
	if !(lastScorerFailed < skippedAt && skippedAt < runFailedAt) {
		t.Errorf("event order scorer.failed=%d skipped=%d run.failed=%d, want scorer.failed < skipped < run.failed",
			lastScorerFailed, skippedAt, runFailedAt)
	}
	app, err := st.GetApplication(context.Background(), sess.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusFailed {
		t.Errorf("application status = %q, want failed", app.Status)
	}
}
