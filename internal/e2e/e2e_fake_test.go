package e2e

import (
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
