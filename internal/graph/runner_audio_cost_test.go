package graph

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestRunCompletedCostIncludesTranscription(t *testing.T) {
	runner, st, ctx := newRunner(t)
	if err := st.CreateAudioUpload(ctx, store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   runner.sessionID,
		ObjectPath:  "sessions/answer.webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  18000,
		CostUSD:     0.002,
		UploadedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}
	if err := runner.Complete(ctx, string(domain.FinishReasonCompleted), 0); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	events, err := st.ListNodeEvents(ctx, runner.runID, 0)
	if err != nil {
		t.Fatalf("ListNodeEvents: %v", err)
	}
	var completed RunCompleted
	found := false
	for _, event := range events {
		if event.Type == EventRunCompleted {
			if err := json.Unmarshal(event.Payload, &completed); err != nil {
				t.Fatalf("decode run.completed: %v", err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("run.completed was not emitted")
	}
	if completed.CostUSD != 0.002 {
		t.Errorf("run.completed cost_usd = %v, want 0.002", completed.CostUSD)
	}
}
