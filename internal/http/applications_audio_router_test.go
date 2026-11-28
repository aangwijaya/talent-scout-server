package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

func seedPlaybackAnswer(t *testing.T, f *applicationsFixture, sess domain.Session, contentType string, data []byte, mutate func(*store.AudioUpload)) (domain.Message, store.AudioUpload) {
	t.Helper()
	ctx := context.Background()
	message := domain.Message{
		ID:        domain.NewID(),
		SessionID: sess.ID,
		Role:      domain.MessageRoleApplicant,
		Kind:      domain.MessageKindAnswer,
		Content:   "I built a small state store.",
	}
	upload := store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   sess.ID,
		ObjectPath:  "sessions/" + sess.ID + "/" + domain.NewID() + ".webm",
		ContentType: contentType,
		SizeBytes:   len(data),
		DurationMS:  18000,
		Transcript:  message.Content,
		Language:    "en",
		Stored:      true,
		Counted:     true,
		UploadedAt:  time.Now().UTC(),
	}
	if mutate != nil {
		mutate(&upload)
	}
	if err := f.store.CreateAudioUpload(ctx, upload); err != nil {
		t.Fatalf("seed audio upload: %v", err)
	}
	if err := f.store.CreateMessageWithAudioUpload(ctx, message, upload.ID); err != nil {
		t.Fatalf("attach audio upload: %v", err)
	}
	return message, upload
}

func (f *applicationsFixture) downloadAudio(t *testing.T, appID, messageID, byteRange string, withToken bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+appID+"/messages/"+messageID+"/audio", nil)
	if withToken {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}
	if byteRange != "" {
		req.Header.Set("Range", byteRange)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func assertNoObjectPathLeak(t *testing.T, rec *httptest.ResponseRecorder, objectPath string) {
	t.Helper()
	if strings.Contains(rec.Body.String(), objectPath) {
		t.Errorf("body contains the object path %q", objectPath)
	}
	for name, values := range rec.Header() {
		for _, value := range values {
			if strings.Contains(value, objectPath) {
				t.Errorf("header %s contains the object path %q: %q", name, objectPath, value)
			}
		}
	}
	for _, fragment := range []string{"storage/v1", "supabase.co"} {
		if strings.Contains(rec.Body.String(), fragment) {
			t.Errorf("body contains storage detail %q", fragment)
		}
		for name, values := range rec.Header() {
			for _, value := range values {
				if strings.Contains(value, fragment) {
					t.Errorf("header %s contains storage detail %q: %q", name, fragment, value)
				}
			}
		}
	}
}

func TestDownloadMessageAudioServesUploadedType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
	}{
		{name: "webm", contentType: "audio/webm"},
		{name: "ogg", contentType: "audio/ogg"},
		{name: "mp4", contentType: "audio/mp4"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newApplicationsFixture(t, "log")
			validator := conformance.New(t)
			app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
			sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
			data := []byte("recording bytes for " + tc.contentType)
			message, upload := seedPlaybackAnswer(t, f, sess, tc.contentType, data, nil)
			if err := f.audioStorage.Upload(context.Background(), upload.ObjectPath, data, tc.contentType); err != nil {
				t.Fatalf("seed object: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID+"/messages/"+message.ID+"/audio", nil)
			req.Header.Set("Authorization", "Bearer "+f.token)
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
			}
			if got := rec.Header().Get("Content-Type"); got != tc.contentType {
				t.Errorf("content-type = %q, want %q", got, tc.contentType)
			}
			if got := rec.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Errorf("cache-control = %q, want private, no-store", got)
			}
			if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("accept-ranges = %q, want bytes", got)
			}
			if got := rec.Body.Bytes(); string(got) != string(data) {
				t.Errorf("body = %q, want %q", got, data)
			}
			assertNoObjectPathLeak(t, rec, upload.ObjectPath)
			validator.ValidateResponse(t, req, rec.Result())
		})
	}
}

func TestDownloadMessageAudioServesByteRanges(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)
	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	data := []byte("0123456789")
	message, upload := seedPlaybackAnswer(t, f, sess, "audio/webm", data, nil)
	if err := f.audioStorage.Upload(context.Background(), upload.ObjectPath, data, "audio/webm"); err != nil {
		t.Fatalf("seed object: %v", err)
	}

	tests := []struct {
		name      string
		byteRange string
		start     int
		end       int
	}{
		{name: "closed", byteRange: "bytes=0-1", start: 0, end: 1},
		{name: "open", byteRange: "bytes=4-", start: 4, end: 9},
		{name: "suffix", byteRange: "bytes=-3", start: 7, end: 9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID+"/messages/"+message.ID+"/audio", nil)
			req.Header.Set("Authorization", "Bearer "+f.token)
			req.Header.Set("Range", tc.byteRange)
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)

			if rec.Code != http.StatusPartialContent {
				t.Fatalf("status = %d, want 206 (body: %s)", rec.Code, rec.Body.String())
			}
			wantRange := fmt.Sprintf("bytes %d-%d/%d", tc.start, tc.end, len(data))
			if got := rec.Header().Get("Content-Range"); got != wantRange {
				t.Errorf("content-range = %q, want %q", got, wantRange)
			}
			if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
				t.Errorf("accept-ranges = %q, want bytes", got)
			}
			if got := rec.Body.String(); got != string(data[tc.start:tc.end+1]) {
				t.Errorf("body = %q, want %q", got, data[tc.start:tc.end+1])
			}
			assertNoObjectPathLeak(t, rec, upload.ObjectPath)
			validator.ValidateResponse(t, req, rec.Result())
		})
	}
}

func TestDownloadMessageAudioIgnoresConditionalRequests(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	validator := conformance.New(t)
	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	data := []byte("0123456789")
	message, upload := seedPlaybackAnswer(t, f, sess, "audio/webm", data, nil)
	if err := f.audioStorage.Upload(context.Background(), upload.ObjectPath, data, "audio/webm"); err != nil {
		t.Fatalf("seed object: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID+"/messages/"+message.ID+"/audio", nil)
	req.Header.Set("Authorization", "Bearer "+f.token)
	req.Header.Set("If-Modified-Since", time.Now().Add(24*time.Hour).UTC().Format(http.TimeFormat))
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Last-Modified"); got != "" {
		t.Errorf("last-modified = %q, want empty", got)
	}
	if got := rec.Body.String(); got != string(data) {
		t.Errorf("body = %q, want %q", got, data)
	}
	assertNoObjectPathLeak(t, rec, upload.ObjectPath)
	validator.ValidateResponse(t, req, rec.Result())
}

func TestDownloadMessageAudioNotFound(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	ctx := context.Background()

	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	message, _ := seedPlaybackAnswer(t, f, sess, "audio/webm", []byte("audio"), nil)

	other := f.seedApplication(t, "other@example.com", domain.ApplicationStatusReview)
	otherSess, _ := f.seedSession(t, other, domain.SessionStatusCompleted)
	otherMessage, _ := seedPlaybackAnswer(t, f, otherSess, "audio/webm", []byte("audio"), nil)

	interviewer := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "How do you manage state?"}
	if err := f.store.CreateMessage(ctx, interviewer); err != nil {
		t.Fatalf("seed interviewer message: %v", err)
	}
	typed := domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "A typed answer."}
	if err := f.store.CreateMessage(ctx, typed); err != nil {
		t.Fatalf("seed typed message: %v", err)
	}

	tests := []struct {
		name      string
		appID     string
		messageID string
	}{
		{name: "unknown application", appID: domain.NewID(), messageID: message.ID},
		{name: "unknown message", appID: app.ID, messageID: domain.NewID()},
		{name: "another application's message", appID: app.ID, messageID: otherMessage.ID},
		{name: "interviewer message", appID: app.ID, messageID: interviewer.ID},
		{name: "typed answer", appID: app.ID, messageID: typed.ID},
		{name: "malformed message id", appID: app.ID, messageID: "not-a-uuid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.downloadAudio(t, tc.appID, tc.messageID, "", true)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec); code != "not_found" {
				t.Errorf("code = %q, want not_found", code)
			}
		})
	}
}

func TestDownloadMessageAudioRecordingGone(t *testing.T) {
	deletedAt := time.Now().UTC()

	tests := []struct {
		name       string
		mutate     func(*store.AudioUpload)
		storeBytes bool
	}{
		{name: "retention deleted the row", mutate: func(upload *store.AudioUpload) { upload.DeletedAt = &deletedAt }, storeBytes: true},
		{name: "recording was never stored", mutate: func(upload *store.AudioUpload) { upload.Stored = false }, storeBytes: true},
		{name: "available row but storage object missing", storeBytes: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newApplicationsFixture(t, "log")
			app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
			sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
			data := []byte("audio")
			message, upload := seedPlaybackAnswer(t, f, sess, "audio/webm", data, tc.mutate)
			if tc.storeBytes {
				if err := f.audioStorage.Upload(context.Background(), upload.ObjectPath, data, "audio/webm"); err != nil {
					t.Fatalf("seed object: %v", err)
				}
			}

			rec := f.downloadAudio(t, app.ID, message.ID, "", true)
			if rec.Code != http.StatusGone {
				t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
			}
			if code := decodeErrorCode(t, rec); code != "recording_deleted" {
				t.Errorf("code = %q, want recording_deleted", code)
			}
			assertNoObjectPathLeak(t, rec, upload.ObjectPath)
		})
	}
}

func TestDownloadMessageAudioRequiresBearerWithoutStorageCall(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	data := []byte("audio")
	message, upload := seedPlaybackAnswer(t, f, sess, "audio/webm", data, nil)
	if err := f.audioStorage.Upload(context.Background(), upload.ObjectPath, data, "audio/webm"); err != nil {
		t.Fatalf("seed object: %v", err)
	}

	before := f.audioStorage.DownloadCount()
	rec := f.downloadAudio(t, app.ID, message.ID, "", false)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec); code != "unauthorized" {
		t.Errorf("code = %q, want unauthorized", code)
	}
	if got := f.audioStorage.DownloadCount(); got != before {
		t.Errorf("storage downloads = %d, want %d (no storage call without auth)", got, before)
	}
}

func TestRecruiterMessagesAlwaysCarryAudio(t *testing.T) {
	f := newApplicationsFixture(t, "log")
	ctx := context.Background()
	app := f.seedApplication(t, "budi@example.com", domain.ApplicationStatusReview)
	sess, _ := f.seedSession(t, app, domain.SessionStatusCompleted)
	if err := f.store.CreateMessage(ctx, domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleAssistant, Kind: domain.MessageKindQuestion, Content: "How do you manage state?"}); err != nil {
		t.Fatalf("seed interviewer message: %v", err)
	}
	if err := f.store.CreateMessage(ctx, domain.Message{ID: domain.NewID(), SessionID: sess.ID, Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer, Content: "A typed answer."}); err != nil {
		t.Fatalf("seed typed message: %v", err)
	}

	rec := f.do(t, http.MethodGet, "/api/v1/applications/"+app.ID, "", true)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Messages []map[string]json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}
	for i, message := range got.Messages {
		if _, ok := message["audio"]; !ok {
			t.Errorf("message %d is missing audio", i)
		}
		if _, ok := message["entry"]; !ok {
			t.Errorf("message %d is missing entry", i)
		}
	}
}
