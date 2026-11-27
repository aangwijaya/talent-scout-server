package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// newVoiceChatFixture starts a voice session through the real start handler so
// the stored recording consent matches what the send path reads.
func newVoiceChatFixture(t *testing.T, consent bool) *chatFixture {
	t.Helper()
	return newVoiceChatFixtureWrapped(t, consent, nil)
}

func newVoiceChatFixtureWrapped(t *testing.T, consent bool, wrap func(store.Store) store.Store) *chatFixture {
	t.Helper()
	f := newChatFixtureWrapped(t, false, wrap)
	ctx := context.Background()
	sess, err := f.store.GetSession(ctx, f.session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	sess.Voice = true
	sess.RecordingConsent = &consent
	if err := f.store.UpdateSession(ctx, sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, fmt.Sprintf(`{"recording_consent":%t}`, consent)))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)
	f.session = sess
	return f
}

func seedSpokenUpload(t *testing.T, st *store.Memory, sessionID, transcript string) store.AudioUpload {
	t.Helper()
	upload := store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   sessionID,
		ObjectPath:  "sessions/" + sessionID + "/" + domain.NewID() + ".webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  18000,
		Transcript:  transcript,
		Language:    "en",
		Stored:      true,
		Counted:     true,
		UploadedAt:  time.Now().UTC(),
	}
	if err := st.CreateAudioUpload(context.Background(), upload); err != nil {
		t.Fatalf("CreateAudioUpload: %v", err)
	}
	return upload
}

func lastApplicantMessage(t *testing.T, st *store.Memory, sessionID string) domain.Message {
	t.Helper()
	messages, err := st.ListMessagesBySession(context.Background(), sessionID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == domain.MessageRoleApplicant {
			return messages[i]
		}
	}
	t.Fatal("no applicant message stored")
	return domain.Message{}
}

func sendSpoken(t *testing.T, f *chatFixture, audioID string, entry map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"audio_id": audioID}
	if entry != nil {
		body["entry"] = entry
	}
	payload, _ := json.Marshal(body)
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	return rec
}

func TestSendSpokenAnswerStoresTranscriptAndAttachesUpload(t *testing.T) {
	f := newVoiceChatFixture(t, true)
	upload := seedSpokenUpload(t, f.store, f.session.ID, "the server transcript")

	rec := sendSpoken(t, f, upload.ID, map[string]any{
		"pasted_chars": 0, "typed_chars": 0, "duration_ms": 0,
		"mode": "typed", "speaking_ms": 17500, "start_latency_ms": 900, "rerecord_count": 2,
	})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	var response struct {
		Message  domain.Message `json:"message"`
		Awaiting string         `json:"awaiting"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Message.Content != "the server transcript" {
		t.Errorf("response content = %q, want the stored transcript", response.Message.Content)
	}
	if response.Awaiting != "assistant" {
		t.Errorf("awaiting = %q, want assistant", response.Awaiting)
	}

	answer := lastApplicantMessage(t, f.store, f.session.ID)
	if answer.ID != response.Message.ID {
		t.Errorf("stored message id = %q, want the response message id %q", answer.ID, response.Message.ID)
	}
	if answer.Content != "the server transcript" {
		t.Errorf("stored content = %q, want the stored transcript", answer.Content)
	}
	if answer.Entry == nil || answer.Entry.Mode != domain.MessageEntryModeSpoken {
		t.Fatalf("stored entry = %+v, want mode spoken", answer.Entry)
	}
	if answer.Entry.SpeakingMS != 17500 || answer.Entry.StartLatencyMS != 900 || answer.Entry.RerecordCount != 2 {
		t.Errorf("stored voice signals = %+v, want the client signals", *answer.Entry)
	}

	attached, err := f.store.GetAudioUpload(context.Background(), upload.ID)
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if attached.MessageID == nil || *attached.MessageID != answer.ID {
		t.Errorf("upload message_id = %v, want %q", attached.MessageID, answer.ID)
	}
}

func TestSendSpokenAnswerValidation(t *testing.T) {
	sentMessage := "7c9e6679-0000-4000-8000-000000000301"
	tests := []struct {
		name      string
		body      func(t *testing.T, f *chatFixture) map[string]any
		wantField string
	}{
		{
			name: "unknown upload",
			body: func(*testing.T, *chatFixture) map[string]any {
				return map[string]any{"audio_id": domain.NewID()}
			},
			wantField: "audio_id",
		},
		{
			name: "another session's upload",
			body: func(t *testing.T, f *chatFixture) map[string]any {
				return map[string]any{"audio_id": seedSpokenUpload(t, f.store, domain.NewID(), "elsewhere").ID}
			},
			wantField: "audio_id",
		},
		{
			name: "already sent upload",
			body: func(t *testing.T, f *chatFixture) map[string]any {
				upload := seedSpokenUpload(t, f.store, f.session.ID, "already used")
				if err := f.store.AttachAudioUpload(context.Background(), upload.ID, sentMessage); err != nil {
					t.Fatalf("AttachAudioUpload: %v", err)
				}
				return map[string]any{"audio_id": upload.ID}
			},
			wantField: "audio_id",
		},
		{
			name: "rejected upload",
			body: func(t *testing.T, f *chatFixture) map[string]any {
				upload := store.AudioUpload{
					ID:         domain.NewID(),
					SessionID:  f.session.ID,
					ObjectPath: "sessions/" + f.session.ID + "/rejected.webm",
					DurationMS: 18000,
					Transcript: "rejected",
					Language:   "en",
					Stored:     false,
					Counted:    false,
					UploadedAt: time.Now().UTC(),
				}
				if err := f.store.CreateAudioUpload(context.Background(), upload); err != nil {
					t.Fatalf("CreateAudioUpload: %v", err)
				}
				return map[string]any{"audio_id": upload.ID}
			},
			wantField: "audio_id",
		},
		{
			name: "both bodies",
			body: func(t *testing.T, f *chatFixture) map[string]any {
				return map[string]any{"content": "typed", "audio_id": seedSpokenUpload(t, f.store, f.session.ID, "both").ID}
			},
		},
		{
			name: "neither body",
			body: func(*testing.T, *chatFixture) map[string]any {
				return map[string]any{}
			},
		},
		{
			name: "typed answer without a fallback reason",
			body: func(*testing.T, *chatFixture) map[string]any {
				return map[string]any{"content": "typed answer", "entry": entry(0, 10, 1000)}
			},
			wantField: "entry.fallback_reason",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newVoiceChatFixture(t, true)
			payload, _ := json.Marshal(tc.body(t, f))
			rec := httptest.NewRecorder()
			f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			code, _, details := decodeError(t, rec)
			if code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", code)
			}
			if tc.wantField != "" && details["field"] != tc.wantField {
				t.Errorf("details.field = %v, want %q", details["field"], tc.wantField)
			}
		})
	}
}

func TestSendSpokenAnswerInTextSessionIsInvalidState(t *testing.T) {
	f := newChatFixture(t, true)
	upload := seedSpokenUpload(t, f.store, f.session.ID, "text session upload")
	rec := sendSpoken(t, f, upload.ID, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

func TestSendSameAudioTwiceConcurrentlyAcceptsExactlyOne(t *testing.T) {
	f := newVoiceChatFixture(t, true)
	upload := seedSpokenUpload(t, f.store, f.session.ID, "one concurrent answer")
	payload, _ := json.Marshal(map[string]any{"audio_id": upload.ID})

	requests := []*http.Request{
		chatRequest(http.MethodPost, f.raw, string(payload)),
		chatRequest(http.MethodPost, f.raw, string(payload)),
	}
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			f.handler.SendMessage(recorders[i], requests[i])
		}(i)
	}
	wg.Wait()

	accepted := 0
	for _, rec := range recorders {
		if rec.Code == http.StatusAccepted {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("statuses = %d and %d, want exactly one 202 (bodies: %s / %s)",
			recorders[0].Code, recorders[1].Code, recorders[0].Body.String(), recorders[1].Body.String())
	}
}

// staleUploadStore reports every upload as not yet sent, like a handler that read
// the row just before a concurrent send attached it.
type staleUploadStore struct{ store.Store }

func (s staleUploadStore) GetAudioUpload(ctx context.Context, id string) (store.AudioUpload, error) {
	upload, err := s.Store.GetAudioUpload(ctx, id)
	upload.MessageID = nil
	return upload, err
}

// The handler's "already sent" check can lose a race; the store must then refuse
// the send as a whole, so no second answer is stored for the same recording.
func TestSendSpokenAnswerLosingTheRaceStoresNothing(t *testing.T) {
	f := newVoiceChatFixtureWrapped(t, true, func(s store.Store) store.Store { return staleUploadStore{s} })
	upload := seedSpokenUpload(t, f.store, f.session.ID, "the winning send")
	if err := f.store.AttachAudioUpload(context.Background(), upload.ID, domain.NewID()); err != nil {
		t.Fatalf("AttachAudioUpload: %v", err)
	}
	before, err := f.store.ListMessagesBySession(context.Background(), f.session.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}

	rec := sendSpoken(t, f, upload.ID, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, details := decodeError(t, rec); code != "validation_failed" || details["field"] != "audio_id" {
		t.Fatalf("error = %s %v, want validation_failed on audio_id", code, details)
	}
	after, err := f.store.ListMessagesBySession(context.Background(), f.session.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("messages = %d, want %d: the losing send must store no answer", len(after), len(before))
	}
}

func TestTypedAnswerInDeclinedVoiceSessionRecordsDeclinedFallback(t *testing.T) {
	f := newVoiceChatFixture(t, false)
	payload, _ := json.Marshal(map[string]string{"content": "I typed this answer."})
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	answer := lastApplicantMessage(t, f.store, f.session.ID)
	if answer.Entry == nil {
		t.Fatal("stored applicant message has no entry")
	}
	if answer.Entry.FallbackReason != domain.MessageEntryFallbackDeclinedRecording {
		t.Errorf("fallback_reason = %q, want declined_recording", answer.Entry.FallbackReason)
	}
}

func TestTypedAnswerInVoiceSessionStoresFallbackReason(t *testing.T) {
	f := newVoiceChatFixture(t, true)
	payload, _ := json.Marshal(map[string]any{
		"content": "I could not record.",
		"entry": map[string]any{
			"pasted_chars": 0, "typed_chars": 20, "duration_ms": 4000,
			"fallback_reason": "microphone_unavailable",
		},
	})
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	answer := lastApplicantMessage(t, f.store, f.session.ID)
	if answer.Entry == nil || answer.Entry.FallbackReason != domain.MessageEntryFallbackMicrophoneUnavailable {
		t.Fatalf("stored entry = %+v, want microphone_unavailable", answer.Entry)
	}
}

// A typed answer is typed whatever the client claims. A pasted answer must not
// be able to mark itself spoken and hide its paste signals from authenticity.
func TestTypedAnswerCannotClaimSpokenMode(t *testing.T) {
	makeBody := func(extra map[string]any) map[string]any {
		entry := map[string]any{
			"pasted_chars": 400, "typed_chars": 0, "duration_ms": 1000,
			"mode": "spoken", "speaking_ms": 5000, "start_latency_ms": 10, "rerecord_count": 0,
		}
		for key, value := range extra {
			entry[key] = value
		}
		return map[string]any{"content": "I pasted this whole answer from elsewhere.", "entry": entry}
	}
	tests := []struct {
		name  string
		voice bool
		extra map[string]any
	}{
		{name: "text session"},
		{name: "voice session with a fallback reason", voice: true, extra: map[string]any{"fallback_reason": "chose_text"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var f *chatFixture
			if tc.voice {
				f = newVoiceChatFixture(t, true)
			} else {
				f = startChat(t)
			}
			payload, _ := json.Marshal(makeBody(tc.extra))
			rec := httptest.NewRecorder()
			f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
			}

			answer := lastApplicantMessage(t, f.store, f.session.ID)
			if answer.Entry == nil {
				t.Fatal("stored applicant message has no entry")
			}
			if answer.Entry.Mode != domain.MessageEntryModeTyped {
				t.Errorf("stored mode = %q, want typed: a typed answer cannot claim to be spoken", answer.Entry.Mode)
			}
		})
	}
}
