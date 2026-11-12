package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
)

type fixtureEnvelope struct {
	RunID   string          `json:"run_id"`
	Seq     int64           `json:"seq"`
	TS      string          `json:"ts"`
	NodeID  *string         `json:"node_id"`
	Attempt *int            `json:"attempt"`
	Data    json.RawMessage `json:"data"`
}

type fixtureEvent struct {
	ID    int64           `json:"id"`
	Event string          `json:"event"`
	Data  fixtureEnvelope `json:"data"`
}

// TestRecordedLoopRetryFixture keeps the demo fixture usable without a live
// model: it proves the recorded OpenRouter run contains a real loop.retry and
// that every frame keeps the EVENTS.md envelope shape.
func TestRecordedLoopRetryFixture(t *testing.T) {
	path := filepath.Join(repoRoot(), "testdata", "recorded-run-with-loop-retry.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var events []fixtureEvent
	if err := json.Unmarshal(raw, &events); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("fixture has no events")
	}

	var retries int
	var completed bool
	for i, event := range events {
		if event.Event == "" {
			t.Fatalf("event[%d] has no event name", i)
		}
		if !sse.IsRunEvent(event.Event) {
			t.Fatalf("event[%d] = %q is not a run-stream event", i, event.Event)
		}
		if event.ID != event.Data.Seq {
			t.Fatalf("event[%d] id = %d, want data.seq %d", i, event.ID, event.Data.Seq)
		}
		if i > 0 && event.Data.Seq <= events[i-1].Data.Seq {
			t.Fatalf("event[%d] seq = %d, want greater than %d", i, event.Data.Seq, events[i-1].Data.Seq)
		}
		if event.Data.RunID == "" {
			t.Fatalf("event[%d] has no run_id", i)
		}
		if _, err := time.Parse(time.RFC3339, event.Data.TS); err != nil {
			t.Fatalf("event[%d] ts = %q, want RFC3339", i, event.Data.TS)
		}
		if len(event.Data.Data) == 0 || string(event.Data.Data) == "null" {
			t.Fatalf("event[%d] %s has no data payload", i, event.Event)
		}

		switch event.Event {
		case graph.EventLoopRetry:
			retries++
			var payload graph.LoopRetry
			if err := json.Unmarshal(event.Data.Data, &payload); err != nil {
				t.Fatalf("event[%d] loop.retry payload: %v", i, err)
			}
			if payload.Attempt < 2 || payload.MaxAttempts < payload.Attempt {
				t.Errorf("loop.retry = %+v, want attempt >= 2 and max_attempts >= attempt", payload)
			}
			switch payload.Reason {
			case "quote_not_found", "invalid_plan", "llm_unavailable":
			default:
				t.Errorf("loop.retry reason = %q, want a contract reason", payload.Reason)
			}
		case graph.EventRunCompleted:
			completed = true
			var payload graph.RunCompleted
			if err := json.Unmarshal(event.Data.Data, &payload); err != nil {
				t.Fatalf("event[%d] run.completed payload: %v", i, err)
			}
			if payload.Status != "succeeded" {
				t.Errorf("run.completed status = %q, want succeeded", payload.Status)
			}
			if payload.CostUSD <= 0 || payload.TotalTokensIn <= 0 || payload.TotalTokensOut <= 0 {
				t.Errorf("run.completed totals = %+v, want real token counts and cost", payload)
			}
		}
	}
	if retries < 1 {
		t.Error("fixture contains no loop.retry")
	}
	if !completed {
		t.Error("fixture contains no run.completed")
	}
}
