package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
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
		Counted:     true,
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

type failingAudioUsageStore struct {
	store.Store
}

func (s failingAudioUsageStore) GetAudioUsage(context.Context, string) (store.AudioUsage, error) {
	return store.AudioUsage{}, errors.New("database is down")
}

func TestTranscriptionCostFailureIsLoggedAndDoesNotFailCompletion(t *testing.T) {
	base := store.NewMemory()
	runner := NewRunner(failingAudioUsageStore{Store: base}, NewBus(base), nil, testModels(), "run-1", "session-1")

	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	if got := runner.transcriptionCost(context.Background()); got != 0 {
		t.Errorf("transcriptionCost = %v, want 0 when the store fails", got)
	}
	if !strings.Contains(logs.String(), "load transcription cost failed") {
		t.Errorf("log = %q, want the database error logged", logs.String())
	}
}
