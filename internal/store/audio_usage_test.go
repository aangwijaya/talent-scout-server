package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func TestMemoryGetAudioUsageSumsEveryUploadInTheSession(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, upload := range []store.AudioUpload{
		{ID: "one", SessionID: "session-1", ObjectPath: "one.webm", ContentType: "audio/webm", DurationMS: 5000, CostUSD: 0.01, Counted: true, UploadedAt: now},
		{ID: "two", SessionID: "session-1", ObjectPath: "two.webm", ContentType: "audio/webm", DurationMS: 7000, CostUSD: 0.02, Counted: true, UploadedAt: now},
		{ID: "other", SessionID: "session-2", ObjectPath: "other.webm", ContentType: "audio/webm", DurationMS: 9000, CostUSD: 0.03, Counted: true, UploadedAt: now},
	} {
		if err := st.CreateAudioUpload(ctx, upload); err != nil {
			t.Fatalf("CreateAudioUpload(%s): %v", upload.ID, err)
		}
	}

	usage, err := st.GetAudioUsage(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetAudioUsage: %v", err)
	}
	if usage.DurationMS != 12000 || usage.CostUSD != 0.03 {
		t.Errorf("usage = %+v, want 12000ms/$0.03", usage)
	}

	empty, err := st.GetAudioUsage(ctx, "no-uploads")
	if err != nil {
		t.Fatalf("GetAudioUsage(no uploads): %v", err)
	}
	if empty.DurationMS != 0 || empty.CostUSD != 0 {
		t.Errorf("empty usage = %+v, want zero", empty)
	}
}

func TestMemoryGetAudioUsageCountsOnlyBudgetedDuration(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	for _, upload := range []store.AudioUpload{
		{ID: "counted", SessionID: "session-1", ObjectPath: "counted.webm", ContentType: "audio/webm", DurationMS: 4000, CostUSD: 0.01, Counted: true, UploadedAt: now},
		{ID: "rejected", SessionID: "session-1", ContentType: "audio/webm", DurationMS: 9000, CostUSD: 0.02, Counted: false, UploadedAt: now},
	} {
		if err := st.CreateAudioUpload(ctx, upload); err != nil {
			t.Fatalf("CreateAudioUpload(%s): %v", upload.ID, err)
		}
	}

	usage, err := st.GetAudioUsage(ctx, "session-1")
	if err != nil {
		t.Fatalf("GetAudioUsage: %v", err)
	}
	if usage.DurationMS != 4000 {
		t.Errorf("duration = %d, want only the counted 4000ms", usage.DurationMS)
	}
	if usage.CostUSD != 0.03 {
		t.Errorf("cost = %v, want every transcription's cost (0.03)", usage.CostUSD)
	}
}
