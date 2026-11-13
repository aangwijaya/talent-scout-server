package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
)

// fakeAnswers match the deterministic scorer fixture in graph.FakeReplies, so
// the fake run ends with four valid scores.
var fakeAnswers = []string{
	"I moved the state into a separate store so each page stayed small and easy to test.",
	"Props were crossing four layers, and I explained the tradeoff to the team before we changed it.",
	"I moved the state into a separate store so each page stayed small and easy to test.",
	"Props were crossing four layers, and I explained the tradeoff to the team before we changed it.",
	"I moved the state into a separate store so each page stayed small and easy to test.",
	"Props were crossing four layers, and I explained the tradeoff to the team before we changed it.",
	"I moved the state into a separate store so each page stayed small and easy to test.",
	"Props were crossing four layers, and I explained the tradeoff to the team before we changed it.",
	"I moved the state into a separate store so each page stayed small and easy to test.",
}

func TestApplyToDecisionWithFakeModels(t *testing.T) {
	h := NewHarness(t, HarnessOptions{})
	result := h.Run(t, "testdata/cvs/ayu-prameswari.pdf", fakeAnswers, 10*time.Second)

	for _, score := range result.Scores {
		if score.Status != domain.ScoreStatusValid {
			t.Errorf("%s status = %q, want valid", score.RubricKey, score.Status)
		}
		if score.Score == nil || len(score.Evidence) == 0 {
			t.Errorf("%s = %+v, want a score and evidence", score.RubricKey, score)
		}
	}

	types := make([]string, 0, len(result.Events))
	for _, event := range result.Events {
		if strings.HasPrefix(event.Type, "session.") || event.Type == graph.EventMessageDelta {
			continue
		}
		types = append(types, event.Type)
	}
	wantPrefix := []string{
		graph.EventRunStarted,
		graph.EventNodeStarted, graph.EventNodeCompleted,
		graph.EventEdgeTraversed,
		graph.EventNodeStarted, graph.EventNodeCompleted,
		graph.EventCheckPassed,
		graph.EventTopicChanged,
		graph.EventEdgeTraversed,
		graph.EventNodeStarted, graph.EventMessageCompleted, graph.EventNodeCompleted,
	}
	if len(types) < len(wantPrefix) {
		t.Fatalf("run events = %v, want prefix %v", types, wantPrefix)
	}
	for i, want := range wantPrefix {
		if types[i] != want {
			t.Errorf("run event[%d] = %q, want %q", i, types[i], want)
		}
	}
	if got := types[len(types)-1]; got != graph.EventRunCompleted {
		t.Errorf("last run event = %q, want run.completed", got)
	}
}

func TestMessageCompletedCarriesStoredCreatedAt(t *testing.T) {
	h := NewHarness(t, HarnessOptions{})
	result := h.Run(t, "testdata/cvs/ayu-prameswari.pdf", fakeAnswers, 10*time.Second)

	stored := make(map[string]domain.Message, len(result.Messages))
	for _, message := range result.Messages {
		stored[message.ID] = message
	}

	seen := 0
	for _, event := range result.Events {
		if event.Type != graph.EventMessageCompleted {
			continue
		}
		var payload graph.MessageCompleted
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode %s: %v", event.Type, err)
		}
		message := payload.Message
		seen++
		if message.CreatedAt.IsZero() {
			t.Errorf("message.completed %s with role %q has a zero created_at", message.ID, message.Role)
			continue
		}
		storedMessage, ok := stored[message.ID]
		if !ok {
			t.Errorf("message.completed %s is not in the store", message.ID)
			continue
		}
		if !message.CreatedAt.Equal(storedMessage.CreatedAt) {
			t.Errorf("message.completed %s created_at = %s, stored = %s", message.ID, message.CreatedAt, storedMessage.CreatedAt)
		}
	}
	if seen == 0 {
		t.Fatal("run emitted no message.completed events")
	}
}
