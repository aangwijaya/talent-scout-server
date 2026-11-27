package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
	"github.com/aangwijaya/talent-scout-server/internal/transcribe"
)

func audioModels() config.ModelsConfig {
	return config.ModelsConfig{
		Transcriber: config.TranscriberConfig{
			Model:            "whisper-large-v3-turbo",
			USDPerHour:       3.6,
			MinBilledSeconds: 10,
		},
	}
}

type audioFixture struct {
	store   *store.Memory
	handler *handlers.Sessions
	fake    *transcribe.Fake
	objects *storage.Fake
	raw     string
	sess    domain.Session
}

func newAudioFixture(t *testing.T, voice bool, status domain.SessionStatus, consent *bool, awaiting bool) *audioFixture {
	t.Helper()
	st, base := sessionStore(t)
	raw, hash, err := session.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	base.TokenHash = hash
	base.Voice = voice
	base.Status = status
	base.RecordingConsent = consent
	if status == domain.SessionStatusActive {
		started := time.Now().UTC().Add(-time.Minute)
		base.StartedAt = &started
	}
	if err := st.CreateSession(context.Background(), base); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	fake := transcribe.NewFake()
	fake.SetResult(transcribe.Result{Transcript: "a spoken answer", Language: "en", DurationMS: 18000})
	objects := storage.NewFake()
	h := handlers.NewSessions(sessionCfg(), st)
	h.SetAudio(objects, fake, audioModels())
	h.SetAwaiting(func(string) bool { return awaiting })
	return &audioFixture{store: st, handler: h, fake: fake, objects: objects, raw: raw, sess: base}
}

func audioRequest(t *testing.T, token string, audio []byte, contentType string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="audio"; filename="answer.webm"`)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(audio); err != nil {
		t.Fatalf("write audio part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+token+"/audio", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("token", token)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

type audioResponseBody struct {
	AudioID    string `json:"audio_id"`
	Transcript string `json:"transcript"`
	Language   string `json:"language"`
	DurationMS int    `json:"duration_ms"`
	Stored     bool   `json:"stored"`
}

func seedAudioUsage(t *testing.T, st *store.Memory, sess domain.Session, durationMS int, costUSD float64) {
	t.Helper()
	if err := st.CreateAudioUpload(context.Background(), store.AudioUpload{
		ID:          domain.NewID(),
		SessionID:   sess.ID,
		ObjectPath:  "sessions/" + sess.ID + "/old.webm",
		ContentType: "audio/webm",
		SizeBytes:   1024,
		DurationMS:  durationMS,
		CostUSD:     costUSD,
		Counted:     true,
		UploadedAt:  time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed audio upload: %v", err)
	}
}

func usedSeconds(t *testing.T, f *audioFixture) int {
	t.Helper()
	rec := httptest.NewRecorder()
	f.handler.Get(rec, sessionReq(http.MethodGet, "/api/v1/sessions/"+f.raw, f.raw))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET session = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	_, detail := decodeSessionDetail(t, rec)
	voice, ok := detail["voice"].(map[string]any)
	if !ok {
		t.Fatalf("voice = %v, want an object", detail["voice"])
	}
	seconds, ok := voice["used_seconds"].(float64)
	if !ok {
		t.Fatalf("used_seconds = %v, want a number", voice["used_seconds"])
	}
	return int(seconds)
}

func audioUsage(t *testing.T, f *audioFixture) store.AudioUsage {
	t.Helper()
	usage, err := f.store.GetAudioUsage(context.Background(), f.sess.ID)
	if err != nil {
		t.Fatalf("GetAudioUsage: %v", err)
	}
	return usage
}

func TestUploadAudioSuccessStoresRecordingAndRaisesUsedSeconds(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	audio := []byte("webm recording bytes")
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, audio, "audio/webm;codecs=opus"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got audioResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.AudioID == "" || got.Transcript != "a spoken answer" || got.Language != "en" || got.DurationMS != 18000 || !got.Stored {
		t.Fatalf("response = %+v, want contract fields and stored=true", got)
	}
	if used := usedSeconds(t, f); used != 18 {
		t.Errorf("used_seconds = %d, want 18", used)
	}

	upload, err := f.store.GetAudioUpload(context.Background(), got.AudioID)
	if err != nil {
		t.Fatalf("GetAudioUpload: %v", err)
	}
	if upload.SessionID != f.sess.ID || upload.Transcript != "a spoken answer" || upload.Language != "en" || upload.DurationMS != 18000 {
		t.Errorf("upload row = %+v, want transcript/language/duration", upload)
	}
	if !upload.Counted {
		t.Error("upload row counted = false, want true")
	}
	if !upload.Stored {
		t.Error("upload row stored = false, want true after a successful object upload")
	}
	if want := audioModels().TranscriptionCostUSD(18000); upload.CostUSD != want {
		t.Errorf("upload cost = %v, want %v", upload.CostUSD, want)
	}
	if storedBytes, ok := f.objects.Get(upload.ObjectPath); !ok || !bytes.Equal(audio, storedBytes) {
		t.Errorf("stored object = %q, %v; want the uploaded bytes", storedBytes, ok)
	}
}

func TestUploadAudioRejectsInvalidState(t *testing.T) {
	consentFalse := false
	tests := []struct {
		name     string
		voice    bool
		consent  *bool
		awaiting bool
	}{
		{name: "not a voice session", voice: false, consent: boolPtr(true), awaiting: true},
		{name: "recording declined", voice: true, consent: &consentFalse, awaiting: true},
		{name: "consent missing", voice: true, consent: nil, awaiting: true},
		{name: "AI still replying", voice: true, consent: boolPtr(true), awaiting: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAudioFixture(t, tc.voice, domain.SessionStatusActive, tc.consent, tc.awaiting)
			rec := httptest.NewRecorder()
			f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
			}
			if code, _, _ := decodeError(t, rec); code != "invalid_state" {
				t.Errorf("code = %q, want invalid_state", code)
			}
			if usage := audioUsage(t, f); usage.DurationMS != 0 {
				t.Errorf("usage = %+v, want no recording counted", usage)
			}
		})
	}
}

func TestUploadAudioRejectsInvalidAudio(t *testing.T) {
	tests := []struct {
		name        string
		audio       []byte
		contentType string
		durationMS  int
	}{
		{name: "empty audio", audio: nil, contentType: "audio/webm", durationMS: 18000},
		{name: "wrong content type", audio: []byte("audio"), contentType: "audio/wav", durationMS: 18000},
		{name: "missing content type", audio: []byte("audio"), contentType: "", durationMS: 18000},
		{name: "longer than the per-answer limit", audio: []byte("audio"), contentType: "audio/webm", durationMS: 120001},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
			f.fake.SetResult(transcribe.Result{Transcript: "a spoken answer", Language: "en", DurationMS: tc.durationMS})
			rec := httptest.NewRecorder()
			f.handler.UploadAudio(rec, audioRequest(t, f.raw, tc.audio, tc.contentType))

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			if code, _, _ := decodeError(t, rec); code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", code)
			}
			if usage := audioUsage(t, f); usage.DurationMS != 0 {
				t.Errorf("usage = %+v, want no recording counted", usage)
			}
		})
	}
}

func TestUploadAudioFileTooLarge(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, bytes.Repeat([]byte("x"), 1<<20+1), "audio/webm"))

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "file_too_large" {
		t.Errorf("code = %q, want file_too_large", code)
	}
}

func TestUploadAudioNoSpeech(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.fake.SetResult(transcribe.Result{Transcript: "   ", Language: "en", DurationMS: 18000})
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/ogg"))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "no_speech" {
		t.Errorf("code = %q, want no_speech", code)
	}
	usage := audioUsage(t, f)
	if usage.DurationMS != 0 {
		t.Errorf("usage = %+v, want the no-speech recording not counted", usage)
	}
	if want := audioModels().TranscriptionCostUSD(18000); math.Abs(usage.CostUSD-want) > 1e-12 {
		t.Errorf("cost = %v, want the billed no-speech transcription %v", usage.CostUSD, want)
	}
	if used := usedSeconds(t, f); used != 0 {
		t.Errorf("used_seconds = %d, want 0", used)
	}
}

func TestUploadAudioOverLengthIsBilledButNotCounted(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.fake.SetResult(transcribe.Result{Transcript: "a very long answer", Language: "en", DurationMS: 120001})
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "validation_failed" {
		t.Errorf("code = %q, want validation_failed", code)
	}
	usage := audioUsage(t, f)
	if usage.DurationMS != 0 {
		t.Errorf("usage = %+v, want the over-length recording not counted", usage)
	}
	if want := audioModels().TranscriptionCostUSD(120001); math.Abs(usage.CostUSD-want) > 1e-12 {
		t.Errorf("cost = %v, want the billed transcription %v", usage.CostUSD, want)
	}
	if used := usedSeconds(t, f); used != 0 {
		t.Errorf("used_seconds = %d, want 0", used)
	}
}

func TestUploadAudioProviderFailureStoresNothing(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.fake.SetErr(transcribe.ErrUnavailable)
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/mp4"))

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "transcription_unavailable" {
		t.Errorf("code = %q, want transcription_unavailable", code)
	}
	if usage := audioUsage(t, f); usage.DurationMS != 0 || usage.CostUSD != 0 {
		t.Errorf("usage = %+v, want nothing stored or counted", usage)
	}
	if used := usedSeconds(t, f); used != 0 {
		t.Errorf("used_seconds = %d, want 0", used)
	}
}

func TestUploadAudioProviderFailureLogsTheProviderError(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.fake.SetErr(transcribe.ErrUnavailable)

	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/mp4"))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if !strings.Contains(logs.String(), "transcription failed") {
		t.Errorf("log = %q, want the provider error logged on the 502 path", logs.String())
	}
}

func TestUploadAudioStorageFailureStillWritesRow(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.objects.SetErr(storage.ErrNotFound)
	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var got audioResponseBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Stored {
		t.Error("stored = true, want false when the object upload failed")
	}
	if got.AudioID == "" {
		t.Error("audio_id is empty")
	}
	if _, err := f.store.GetAudioUpload(context.Background(), got.AudioID); err != nil {
		t.Errorf("upload row was not written: %v", err)
	}
	if used := usedSeconds(t, f); used != 18 {
		t.Errorf("used_seconds = %d, want 18 even when storage failed", used)
	}
}

func TestUploadAudioBudgetRejectsCrossingAndAcceptsExactlyTheLimit(t *testing.T) {
	t.Run("would cross the limit", func(t *testing.T) {
		f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
		seedAudioUsage(t, f.store, f.sess, 1190000, 0)
		f.fake.SetResult(transcribe.Result{Transcript: "one more", Language: "en", DurationMS: 10001})
		rec := httptest.NewRecorder()
		f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
		}
		if code, _, _ := decodeError(t, rec); code != "audio_budget_exceeded" {
			t.Errorf("code = %q, want audio_budget_exceeded", code)
		}
		usage := audioUsage(t, f)
		if usage.DurationMS != 1190000 {
			t.Errorf("usage = %+v, want the rejected upload not counted", usage)
		}
		if want := audioModels().TranscriptionCostUSD(10001); math.Abs(usage.CostUSD-want) > 1e-12 {
			t.Errorf("cost = %v, want the billed over-budget transcription %v", usage.CostUSD, want)
		}
		if used := usedSeconds(t, f); used != 1190 {
			t.Errorf("used_seconds = %d, want 1190", used)
		}
	})

	t.Run("lands exactly on the limit", func(t *testing.T) {
		f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
		seedAudioUsage(t, f.store, f.sess, 1190000, 0)
		f.fake.SetResult(transcribe.Result{Transcript: "one more", Language: "en", DurationMS: 10000})
		rec := httptest.NewRecorder()
		f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
		}
		if used := usedSeconds(t, f); used != 1200 {
			t.Errorf("used_seconds = %d, want 1200", used)
		}
	})
}

// barrierTranscriber holds every call until the expected number have arrived, so
// two uploads are guaranteed to have finished transcribing before either checks
// the budget.
type barrierTranscriber struct {
	result transcribe.Result
	wg     sync.WaitGroup
}

func (b *barrierTranscriber) Transcribe(_ context.Context, _ []byte, _ string) (transcribe.Result, error) {
	b.wg.Done()
	b.wg.Wait()
	return b.result, nil
}

// usageBarrierStore makes both uploads read the usage before either can return
// it, reproducing the check-then-act window unless the handler serializes them.
type usageBarrierStore struct {
	store.Store

	mu       sync.Mutex
	reads    int
	allReads chan struct{}
}

func newUsageBarrierStore(inner store.Store) *usageBarrierStore {
	return &usageBarrierStore{Store: inner, allReads: make(chan struct{})}
}

func (s *usageBarrierStore) GetAudioUsage(ctx context.Context, sessionID string) (store.AudioUsage, error) {
	usage, err := s.Store.GetAudioUsage(ctx, sessionID)

	s.mu.Lock()
	s.reads++
	if s.reads == 2 {
		close(s.allReads)
	}
	s.mu.Unlock()

	select {
	case <-s.allReads:
	case <-time.After(200 * time.Millisecond):
	}
	return usage, err
}

func TestUploadAudioSerializesConcurrentBudgetChecks(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	seedAudioUsage(t, f.store, f.sess, 1190000, 0)
	barrier := &barrierTranscriber{result: transcribe.Result{Transcript: "a concurrent answer", Language: "en", DurationMS: 10000}}
	barrier.wg.Add(2)
	h := handlers.NewSessions(sessionCfg(), newUsageBarrierStore(f.store))
	h.SetAudio(f.objects, barrier, audioModels())
	h.SetAwaiting(func(string) bool { return true })

	requests := []*http.Request{
		audioRequest(t, f.raw, []byte("audio"), "audio/webm"),
		audioRequest(t, f.raw, []byte("audio"), "audio/webm"),
	}
	recorders := []*httptest.ResponseRecorder{httptest.NewRecorder(), httptest.NewRecorder()}
	var wg sync.WaitGroup
	for i := range requests {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.UploadAudio(recorders[i], requests[i])
		}(i)
	}
	wg.Wait()

	codes := []int{recorders[0].Code, recorders[1].Code}
	okCount, conflictCount := 0, 0
	for _, code := range codes {
		switch code {
		case http.StatusOK:
			okCount++
		case http.StatusConflict:
			conflictCount++
		}
	}
	if okCount != 1 || conflictCount != 1 {
		t.Fatalf("statuses = %v, want exactly one 200 and one 409 (bodies: %s / %s)", codes, recorders[0].Body.String(), recorders[1].Body.String())
	}
	for i, code := range codes {
		if code == http.StatusConflict {
			if c, _, _ := decodeError(t, recorders[i]); c != "audio_budget_exceeded" {
				t.Errorf("conflict code = %q, want audio_budget_exceeded", c)
			}
		}
	}
	if used := usedSeconds(t, f); used != 1200 {
		t.Errorf("used_seconds = %d, want 1200 (one counted upload)", used)
	}
}

func TestUploadAudioTimeUpReturnsGone(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	started := time.Now().UTC().Add(-time.Duration(sessionCfg().SessionMaxMinutes+1) * time.Minute)
	sess := f.sess
	sess.StartedAt = &started
	if err := f.store.UpdateSession(context.Background(), sess); err != nil {
		t.Fatalf("UpdateSession: %v", err)
	}

	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))
	if rec.Code != http.StatusGone {
		t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
	}
	if usage := audioUsage(t, f); usage.DurationMS != 0 {
		t.Errorf("usage = %+v, want no recording counted after time is up", usage)
	}
}

func TestUploadAudioRateLimit(t *testing.T) {
	f := newAudioFixture(t, true, domain.SessionStatusActive, boolPtr(true), true)
	f.fake.SetResult(transcribe.Result{Transcript: "short", Language: "en", DurationMS: 1000})
	for i := 0; i < 60; i++ {
		rec := httptest.NewRecorder()
		f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))
		if rec.Code != http.StatusOK {
			t.Fatalf("upload %d = %d, want 200 (body: %s)", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := httptest.NewRecorder()
	f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("61st upload = %d, want 429 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _, _ := decodeError(t, rec); code != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("Retry-After header is missing")
	}
}

func TestUploadAudioFinishedSessionReturnsGone(t *testing.T) {
	for _, status := range []domain.SessionStatus{domain.SessionStatusCompleted, domain.SessionStatusExpired} {
		t.Run(string(status), func(t *testing.T) {
			f := newAudioFixture(t, true, status, boolPtr(true), true)
			rec := httptest.NewRecorder()
			f.handler.UploadAudio(rec, audioRequest(t, f.raw, []byte("audio"), "audio/webm"))

			if rec.Code != http.StatusGone {
				t.Fatalf("status = %d, want 410 (body: %s)", rec.Code, rec.Body.String())
			}
		})
	}
}

func boolPtr(v bool) *bool { return &v }
