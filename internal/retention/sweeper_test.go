package retention_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/retention"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type countingStorage struct {
	*storage.Fake
	mu      sync.Mutex
	deleted []string
}

func (c *countingStorage) Delete(ctx context.Context, path string) error {
	c.mu.Lock()
	c.deleted = append(c.deleted, path)
	c.mu.Unlock()
	return c.Fake.Delete(ctx, path)
}

func (c *countingStorage) DeletedPaths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.deleted...)
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newSweeper(st store.Store, audio storage.Storage) *retention.Sweeper {
	return retention.NewSweeper(st, audio, testLogger())
}

func seedUpload(t *testing.T, st store.Store, upload store.AudioUpload) {
	t.Helper()
	if err := st.CreateAudioUpload(context.Background(), upload); err != nil {
		t.Fatalf("CreateAudioUpload(%s): %v", upload.ID, err)
	}
}

func upload(id string, uploadedAt time.Time, messageID *string) store.AudioUpload {
	return store.AudioUpload{
		ID:          id,
		SessionID:   "session-1",
		ObjectPath:  "sessions/session-1/" + id + ".webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  3000,
		MessageID:   messageID,
		UploadedAt:  uploadedAt,
	}
}

func assertDeletedAt(t *testing.T, st store.Store, id string, want bool) {
	t.Helper()
	got, err := st.GetAudioUpload(context.Background(), id)
	if err != nil {
		t.Fatalf("GetAudioUpload(%s): %v", id, err)
	}
	if want && got.DeletedAt == nil {
		t.Errorf("%s deleted_at = nil, want set", id)
	}
	if !want && got.DeletedAt != nil {
		t.Errorf("%s deleted_at = %s, want nil", id, got.DeletedAt)
	}
}

func TestSweepDeletesSentAtThirtyDaysAndUnsentAtTwentyFourHours(t *testing.T) {
	st := store.NewMemory()
	audio := &countingStorage{Fake: storage.NewFake()}
	now := time.Now().UTC()
	sentMessage := "message-sent"

	seedUpload(t, st, upload("sent-30-days", now.Add(-retention.RecordingRetention), &sentMessage))
	seedUpload(t, st, upload("unsent-24-hours", now.Add(-retention.UnsentUploadRetention), nil))
	seedUpload(t, st, upload("sent-29-days", now.Add(-29*24*time.Hour), &sentMessage))
	seedUpload(t, st, upload("unsent-23-hours", now.Add(-23*time.Hour), nil))

	deleted, err := newSweeper(st, audio).SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2", deleted)
	}

	assertDeletedAt(t, st, "sent-30-days", true)
	assertDeletedAt(t, st, "unsent-24-hours", true)
	assertDeletedAt(t, st, "sent-29-days", false)
	assertDeletedAt(t, st, "unsent-23-hours", false)

	paths := audio.DeletedPaths()
	if len(paths) != 2 {
		t.Fatalf("deleted paths = %v, want 2", paths)
	}
	seen := map[string]bool{paths[0]: true, paths[1]: true}
	if !seen["sessions/session-1/sent-30-days.webm"] || !seen["sessions/session-1/unsent-24-hours.webm"] {
		t.Errorf("deleted paths = %v, want the two due objects", paths)
	}
}

func TestSweepStorageFailureLeavesRowForNextRun(t *testing.T) {
	st := store.NewMemory()
	audio := storage.NewFake()
	audio.SetErr(errors.New("storage unavailable"))
	now := time.Now().UTC()
	sentMessage := "message-sent"
	seedUpload(t, st, upload("sent-30-days", now.Add(-31*24*time.Hour), &sentMessage))

	sweeper := newSweeper(st, audio)
	deleted, err := sweeper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce with a storage failure: %v", err)
	}
	if deleted != 0 {
		t.Errorf("deleted = %d, want 0", deleted)
	}
	assertDeletedAt(t, st, "sent-30-days", false)

	audio.SetErr(nil)
	deleted, err = sweeper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	if deleted != 1 {
		t.Errorf("deleted on the next run = %d, want 1", deleted)
	}
	assertDeletedAt(t, st, "sent-30-days", true)
}

func TestSecondSweepDeletesNothingNew(t *testing.T) {
	st := store.NewMemory()
	audio := &countingStorage{Fake: storage.NewFake()}
	now := time.Now().UTC()
	sentMessage := "message-sent"
	seedUpload(t, st, upload("sent-30-days", now.Add(-31*24*time.Hour), &sentMessage))

	sweeper := newSweeper(st, audio)
	first, err := sweeper.SweepOnce(context.Background())
	if err != nil || first != 1 {
		t.Fatalf("first SweepOnce = %d, %v; want 1, nil", first, err)
	}
	got, err := st.GetAudioUpload(context.Background(), "sent-30-days")
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	firstDeletedAt := got.DeletedAt

	second, err := sweeper.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("second SweepOnce: %v", err)
	}
	if second != 0 {
		t.Errorf("second SweepOnce deleted = %d, want 0", second)
	}
	if len(audio.DeletedPaths()) != 1 {
		t.Errorf("storage Delete calls = %d, want 1", len(audio.DeletedPaths()))
	}
	got, err = st.GetAudioUpload(context.Background(), "sent-30-days")
	if err != nil {
		t.Fatalf("GetAudioUpload after second sweep: %v", err)
	}
	if got.DeletedAt == nil || !got.DeletedAt.Equal(*firstDeletedAt) {
		t.Errorf("deleted_at changed on the second run: %v, want %v", got.DeletedAt, firstDeletedAt)
	}
}

func TestSweepFailureLogOmitsObjectPath(t *testing.T) {
	st := store.NewMemory()
	audio := storage.NewFake()
	audio.SetErr(errors.New("storage unavailable"))
	now := time.Now().UTC()
	sentMessage := "message-sent"
	objectPath := "sessions/session-1/secret-recording-id.webm"
	seedUpload(t, st, store.AudioUpload{
		ID: "upload-1", SessionID: "session-1", ObjectPath: objectPath,
		ContentType: "audio/webm", SizeBytes: 1024, DurationMS: 3000,
		MessageID: &sentMessage, UploadedAt: now.Add(-31 * 24 * time.Hour),
	})

	var logs bytes.Buffer
	sweeper := retention.NewSweeper(st, audio, slog.New(slog.NewTextHandler(&logs, nil)))
	if _, err := sweeper.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if strings.Contains(logs.String(), objectPath) {
		t.Errorf("log contains the object path: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "upload-1") {
		t.Errorf("log is missing the upload id: %s", logs.String())
	}
}

func TestSweepLeavesMessageContentUnchanged(t *testing.T) {
	st := store.NewMemory()
	audio := &countingStorage{Fake: storage.NewFake()}
	ctx := context.Background()
	now := time.Now().UTC()

	message := domain.Message{
		ID:        "message-1",
		SessionID: "session-1",
		Role:      domain.MessageRoleApplicant,
		Kind:      domain.MessageKindAnswer,
		Content:   "I moved the state into a separate store.",
	}
	if err := st.CreateMessage(ctx, message); err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	seedUpload(t, st, upload("sent-30-days", now.Add(-31*24*time.Hour), &message.ID))

	if _, err := newSweeper(st, audio).SweepOnce(ctx); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	messages, err := st.ListMessagesBySession(ctx, message.SessionID, true)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(messages) != 1 || messages[0].Content != message.Content {
		t.Fatalf("messages = %+v, want the original content", messages)
	}
}
