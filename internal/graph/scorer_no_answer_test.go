package graph

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// switchableLLM answers normally until down is set, then every call reports
// llm.ErrUnavailable. It lets a test drive the session setup with fake replies
// and then prove the scoring path survives an unavailable provider.
type switchableLLM struct {
	inner llm.Client

	mu   sync.Mutex
	down bool
}

func (c *switchableLLM) Chat(ctx context.Context, req llm.Request) (llm.Response, error) {
	if c.isDown() {
		return llm.Response{}, llm.ErrUnavailable
	}
	return c.inner.Chat(ctx, req)
}

func (c *switchableLLM) Stream(ctx context.Context, req llm.Request, onDelta func(string) error) (llm.Response, error) {
	if c.isDown() {
		return llm.Response{}, llm.ErrUnavailable
	}
	return c.inner.Stream(ctx, req, onDelta)
}

func (c *switchableLLM) isDown() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.down
}

func (c *switchableLLM) goDown() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.down = true
}

// abandonBeforeFirstAnswer starts the session with the seeded fake replies and
// then lets the sweeper abandon it while no applicant answer exists.
func abandonBeforeFirstAnswer(t *testing.T, st *store.Memory, registry *session.Registry, sess domain.Session) {
	t.Helper()
	fresh, err := st.GetSession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	past := time.Now().Add(-11 * time.Minute)
	fresh.StartedAt = &past
	if err := st.UpdateSession(context.Background(), fresh); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}
	sweeper := session.NewSweeper(st, registry, 15*time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, abandoned, _, err := sweeper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if abandoned != 1 {
		t.Fatalf("sweeper abandoned = %d, want 1", abandoned)
	}
}

// TestNoAnswerSessionScoresNeedsManualWithoutScorerCall proves a session ended
// before the first applicant answer never calls the scorer, stores every
// criterion needs_manual at attempt 1, and still reaches recruiter review with
// the documented node lifecycle and no check on an empty set.
func TestNoAnswerSessionScoresNeedsManualWithoutScorerCall(t *testing.T) {
	st, sess := seedGraphSession(t)
	fake := llm.NewFake(FakeReplies)
	manager, registry := newTestManager(t, st, fake)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	abandonBeforeFirstAnswer(t, st, registry, sess)
	waitApplicationStatus(t, st, sess.ApplicationID, domain.ApplicationStatusReview)

	for _, call := range fake.Calls() {
		if call.Node == "scorer" {
			t.Errorf("scorer was called with %d messages, want no scorer call", len(call.Messages))
		}
	}

	scores := scorerScores(t, st, sess.ApplicationID)
	if len(scores) != 4 {
		t.Fatalf("scores = %d, want 4", len(scores))
	}
	for key, score := range scores {
		if score.Status != domain.ScoreStatusNeedsManual {
			t.Errorf("%s status = %q, want needs_manual", key, score.Status)
		}
		if score.Score != nil {
			t.Errorf("%s score = %d, want nil", key, *score.Score)
		}
		if score.Attempts != 1 {
			t.Errorf("%s attempts = %d, want 1", key, score.Attempts)
		}
	}

	events := sessionEvents(t, st, sess.ID)
	expected := []string{
		"edge.guard-scorer",
		"node.started.scorer",
		"node.completed.scorer",
		"edge.scorer-evidence_check",
		"node.started.evidence_check",
		"node.completed.evidence_check",
		"run.completed",
	}
	next := 0
	scorerStartedAt := -1
	for i, event := range events {
		if scorerStartedAt >= 0 && (event.Type == EventCheckPassed || event.Type == EventCheckFailed) {
			t.Errorf("check event after node.started(scorer): %+v", event)
		}
		name := ""
		switch event.Type {
		case EventNodeStarted:
			name = "node.started." + derefNodeID(event.NodeID)
		case EventNodeCompleted:
			name = "node.completed." + derefNodeID(event.NodeID)
		case EventRunCompleted:
			name = "run.completed"
		case EventEdgeTraversed:
			var payload EdgeTraversed
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("unmarshal edge: %v", err)
			}
			switch {
			case payload.From == "guard" && payload.To == "scorer" && payload.Label == "finish":
				name = "edge.guard-scorer"
			case payload.From == "scorer" && payload.To == "evidence_check" && payload.Label == "scored":
				name = "edge.scorer-evidence_check"
			}
		}
		if name == "" {
			continue
		}
		if event.Type == EventNodeStarted && derefNodeID(event.NodeID) == "scorer" {
			scorerStartedAt = i
		}
		if next < len(expected) && name == expected[next] {
			next++
		}
	}
	if next != len(expected) {
		t.Fatalf("event order matched %d/%d steps (%v), events: %v", next, len(expected), expected, graphEventTypes(events))
	}

	for _, event := range events {
		if event.Type != EventNodeCompleted || derefNodeID(event.NodeID) != "scorer" {
			continue
		}
		var payload NodeCompleted
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("unmarshal node.completed: %v", err)
		}
		if payload.TokensIn != 0 || payload.TokensOut != 0 || payload.CostUSD != 0 {
			t.Errorf("scorer node.completed = %+v, want zero tokens and cost", payload)
		}
	}

	completed := runCompletedPayload(t, events)
	if completed.NeedsManualCount != 4 {
		t.Errorf("needs_manual_count = %d, want 4", completed.NeedsManualCount)
	}
}

// TestNoAnswerScoringMarksReviewWhenLLMUnavailable proves the same abandoned
// session reaches review even when every LLM call would fail: scoring must not
// call the provider at all when no criterion can be scored.
func TestNoAnswerScoringMarksReviewWhenLLMUnavailable(t *testing.T) {
	st, sess := seedGraphSession(t)
	client := &switchableLLM{inner: llm.NewFake(FakeReplies)}
	manager, registry := newTestManager(t, st, client)
	if err := manager.Start(context.Background(), sess); err != nil {
		t.Fatalf("Start: %v", err)
	}
	waitIdle(t, manager, sess.ID)

	client.goDown()
	abandonBeforeFirstAnswer(t, st, registry, sess)
	waitApplicationStatus(t, st, sess.ApplicationID, domain.ApplicationStatusReview)

	app, err := st.GetApplication(context.Background(), sess.ApplicationID)
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	if app.Status != domain.ApplicationStatusReview {
		t.Errorf("application status = %q, want review", app.Status)
	}
	scores := scorerScores(t, st, sess.ApplicationID)
	for key, score := range scores {
		if score.Status != domain.ScoreStatusNeedsManual {
			t.Errorf("%s status = %q, want needs_manual", key, score.Status)
		}
	}
	run, err := st.GetGraphRunBySession(context.Background(), sess.ID)
	if err != nil {
		t.Fatalf("GetGraphRunBySession: %v", err)
	}
	if run.Status != domain.GraphRunStatusSucceeded {
		t.Errorf("graph run status = %q, want succeeded", run.Status)
	}
}

func derefNodeID(id *string) string {
	if id == nil {
		return ""
	}
	return *id
}
