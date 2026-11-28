package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func audioUpload(id string, uploadedAt time.Time, messageID *string) store.AudioUpload {
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

func TestMemoryListAudioUploadsDue(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	messageID := "message-1"
	deletedAt := now.Add(-time.Hour)

	uploads := []store.AudioUpload{
		audioUpload("sent-old", now.Add(-31*24*time.Hour), &messageID),
		audioUpload("unsent-old", now.Add(-25*time.Hour), nil),
		audioUpload("sent-new", now.Add(-29*24*time.Hour), &messageID),
		audioUpload("unsent-new", now.Add(-23*time.Hour), nil),
		audioUpload("already-deleted", now.Add(-48*time.Hour), nil),
	}
	uploads[4].DeletedAt = &deletedAt
	for _, upload := range uploads {
		if err := st.CreateAudioUpload(ctx, upload); err != nil {
			t.Fatalf("CreateAudioUpload(%s): %v", upload.ID, err)
		}
	}

	due, err := st.ListAudioUploadsDue(ctx, now.Add(-30*24*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListAudioUploadsDue: %v", err)
	}
	if len(due) != 2 || due[0].ID != "sent-old" || due[1].ID != "unsent-old" {
		t.Fatalf("due = %+v, want sent-old and unsent-old", due)
	}

	if err := st.MarkAudioUploadDeleted(ctx, "sent-old", now); err != nil {
		t.Fatalf("MarkAudioUploadDeleted: %v", err)
	}
	due, err = st.ListAudioUploadsDue(ctx, now.Add(-30*24*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("ListAudioUploadsDue after mark: %v", err)
	}
	if len(due) != 1 || due[0].ID != "unsent-old" {
		t.Fatalf("due after mark = %+v, want only unsent-old", due)
	}
}

func TestMemoryAttachAudioUpload(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateAudioUpload(ctx, audioUpload("upload-1", time.Now().UTC(), nil)); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}

	if err := st.AttachAudioUpload(ctx, "upload-1", "message-1"); err != nil {
		t.Fatalf("AttachAudioUpload: %v", err)
	}
	got, err := st.GetAudioUpload(ctx, "upload-1")
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if got.MessageID == nil || *got.MessageID != "message-1" {
		t.Fatalf("message_id = %v, want message-1", got.MessageID)
	}
	if err := st.AttachAudioUpload(ctx, "upload-1", "message-2"); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second AttachAudioUpload = %v, want ErrConflict", err)
	}
	if err := st.AttachAudioUpload(ctx, "missing", "message-1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("AttachAudioUpload unknown = %v, want ErrNotFound", err)
	}
}

func TestMemoryCreateMessageWithAudioUploadIsAtomic(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	upload := audioUpload("upload-1", time.Now().UTC(), nil)
	upload.Transcript = "a spoken answer"
	upload.Stored = true
	upload.Counted = true
	if err := st.CreateAudioUpload(ctx, upload); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}

	message := domain.Message{
		ID:        "message-1",
		SessionID: upload.SessionID,
		Role:      domain.MessageRoleApplicant,
		Kind:      domain.MessageKindAnswer,
		Content:   upload.Transcript,
	}
	if err := st.CreateMessageWithAudioUpload(ctx, message, upload.ID); err != nil {
		t.Fatalf("CreateMessageWithAudioUpload: %v", err)
	}
	got, err := st.GetAudioUpload(ctx, upload.ID)
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if got.MessageID == nil || *got.MessageID != message.ID {
		t.Fatalf("message_id = %v, want %s", got.MessageID, message.ID)
	}

	second := message
	second.ID = "message-2"
	if err := st.CreateMessageWithAudioUpload(ctx, second, upload.ID); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("second CreateMessageWithAudioUpload = %v, want ErrConflict", err)
	}
	messages, err := st.ListMessagesBySession(ctx, upload.SessionID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != message.ID {
		t.Fatalf("messages = %+v, want only the first answer", messages)
	}

	rejected := audioUpload("upload-2", time.Now().UTC(), nil)
	rejected.Counted = false
	if err := st.CreateAudioUpload(ctx, rejected); err != nil {
		t.Fatalf("CreateAudioUpload(rejected): %v", err)
	}
	if err := st.CreateMessageWithAudioUpload(ctx, second, rejected.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("rejected CreateMessageWithAudioUpload = %v, want ErrNotFound", err)
	}
}

func TestMemoryMarkAudioUploadDeletedIsIdempotent(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	if err := st.CreateAudioUpload(ctx, audioUpload("upload-1", time.Now().UTC(), nil)); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}

	now := time.Now().UTC()
	if err := st.MarkAudioUploadDeleted(ctx, "upload-1", now); err != nil {
		t.Fatalf("MarkAudioUploadDeleted: %v", err)
	}
	got, err := st.GetAudioUpload(ctx, "upload-1")
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if got.DeletedAt == nil || !got.DeletedAt.Equal(now) {
		t.Fatalf("deleted_at = %v, want %s", got.DeletedAt, now)
	}
	if err := st.MarkAudioUploadDeleted(ctx, "upload-1", now.Add(time.Hour)); err != nil {
		t.Fatalf("second MarkAudioUploadDeleted: %v", err)
	}
	got, err = st.GetAudioUpload(ctx, "upload-1")
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if got.DeletedAt == nil || !got.DeletedAt.Equal(now) {
		t.Fatalf("deleted_at = %v, want the first timestamp", got.DeletedAt)
	}
}

func TestMemoryAudioUploadWordTimingsRoundTrip(t *testing.T) {
	st := store.NewMemory()
	ctx := context.Background()
	upload := audioUpload("upload-words", time.Now().UTC(), nil)
	upload.Words = []domain.TranscriptWord{
		{Word: "I", Start: 0, End: 0.3},
		{Word: "built", Start: 0.4, End: 0.9},
	}
	upload.Confidence = 0.87
	if err := st.CreateAudioUpload(ctx, upload); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}

	got, err := st.GetAudioUpload(ctx, upload.ID)
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if len(got.Words) != 2 || got.Words[1].Word != "built" || got.Words[1].Start != 0.4 || got.Words[1].End != 0.9 {
		t.Fatalf("words = %+v, want the stored timings", got.Words)
	}
	if got.Confidence != 0.87 {
		t.Fatalf("confidence = %v, want 0.87", got.Confidence)
	}

	got.Words[0].Word = "mutated"
	fresh, err := st.GetAudioUpload(ctx, upload.ID)
	if err != nil {
		t.Fatalf("second GetAudioUpload: %v", err)
	}
	if fresh.Words[0].Word != "I" {
		t.Errorf("stored words were mutated through the returned slice: %+v", fresh.Words)
	}
}
