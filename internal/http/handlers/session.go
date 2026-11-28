package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
	"github.com/aangwijaya/talent-scout-server/internal/transcribe"
)

const (
	maxAnswerChars = 2000

	maxAudioUploadBytes = 1 << 20
	audioUploadLimit    = 60
	audioUploadWindow   = 10 * time.Minute
)

type Sessions struct {
	cfg            config.Config
	store          store.Store
	manager        *graph.SessionManager
	bus            *graph.Bus
	messageLimiter *middleware.RateLimiter
	audioLimiter   *middleware.RateLimiter
	audioStorage   storage.Storage
	transcriber    transcribe.Transcriber
	models         config.ModelsConfig
	awaiting       func(sessionID string) bool

	// audioLocks serializes the budget read and the row insert for one session,
	// so two concurrent uploads cannot both pass the check without counting.
	audioLocksMu sync.Mutex
	audioLocks   map[string]*sync.Mutex
}

func NewSessions(cfg config.Config, st store.Store) *Sessions {
	return &Sessions{
		cfg:            cfg,
		store:          st,
		messageLimiter: middleware.NewRateLimiter(cfg.MessageRateLimit(), cfg.MessageRateWindow()),
		audioLimiter:   middleware.NewRateLimiter(audioUploadLimit, audioUploadWindow),
		audioLocks:     make(map[string]*sync.Mutex),
	}
}

// audioLock returns the per-session mutex guarding the audio budget.
func (h *Sessions) audioLock(sessionID string) *sync.Mutex {
	h.audioLocksMu.Lock()
	defer h.audioLocksMu.Unlock()
	mu, ok := h.audioLocks[sessionID]
	if !ok {
		mu = &sync.Mutex{}
		h.audioLocks[sessionID] = mu
	}
	return mu
}

// SetChat attaches the live graph manager and event bus. Without them the chat
// endpoints report invalid state (the runner is not available) but the session
// info/start endpoints still work.
func (h *Sessions) SetChat(manager *graph.SessionManager, bus *graph.Bus) {
	h.manager = manager
	h.bus = bus
	if manager != nil {
		h.awaiting = manager.Awaiting
	}
}

// SetAudio attaches the private audio bucket, speech-to-text client and model
// price. Without them the audio endpoint cannot accept recordings.
func (h *Sessions) SetAudio(audioStorage storage.Storage, transcriber transcribe.Transcriber, models config.ModelsConfig) {
	h.audioStorage = audioStorage
	h.transcriber = transcriber
	h.models = models
}

// SetAwaiting supplies the live check for "the AI finished replying". The
// router uses the session manager; tests can supply a deterministic stub.
func (h *Sessions) SetAwaiting(fn func(sessionID string) bool) {
	h.awaiting = fn
}

type currentTopic struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Total    int    `json:"total"`
}

const (
	voiceMaxAnswerSeconds = 120
	voiceMaxTotalSeconds  = 1200
)

type voiceSession struct {
	MaxAnswerSeconds int   `json:"max_answer_seconds"`
	MaxTotalSeconds  int   `json:"max_total_seconds"`
	UsedSeconds      int   `json:"used_seconds"`
	RecordingConsent *bool `json:"recording_consent"`
}

type sessionDetail struct {
	Status        domain.SessionStatus `json:"status"`
	JobTitle      string               `json:"job_title"`
	ApplicantName string               `json:"applicant_name"`
	ExpiresAt     time.Time            `json:"expires_at"`
	StartedAt     *time.Time           `json:"started_at"`
	DeadlineAt    *time.Time           `json:"deadline_at"`
	MaxMinutes    int                  `json:"max_minutes"`
	TopicCount    *int                 `json:"topic_count"`
	QuestionCount int                  `json:"question_count"`
	MaxQuestions  int                  `json:"max_questions"`
	CurrentTopic  *currentTopic        `json:"current_topic"`
	Voice         *voiceSession        `json:"voice"`
}

func (h *Sessions) Get(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}

	detail, err := h.buildDetail(r.Context(), sess)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load session", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, detail)
}

type startSessionRequest struct {
	RecordingConsent *bool `json:"recording_consent"`
}

func (h *Sessions) Start(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req startSessionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid JSON body", nil)
		return
	}
	var consent *bool
	if sess.Voice && sess.Status == domain.SessionStatusInvited {
		if req.RecordingConsent == nil {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "recording_consent is required for a voice session", map[string]string{"field": "recording_consent"})
			return
		}
		consent = req.RecordingConsent
	}

	started, err := h.store.StartSessionWithConsent(r.Context(), sess.ID, time.Now().UTC(), consent)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			apierror.Write(w, http.StatusConflict, apierror.CodeSessionAlreadyStarted, "session already started", nil)
		case errors.Is(err, store.ErrNotFound):
			apierror.Write(w, http.StatusNotFound, apierror.CodeSessionNotFound, "session not found", nil)
		default:
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not start session", nil)
		}
		return
	}

	if h.manager != nil {
		if err := h.manager.Start(r.Context(), started); err != nil {
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not start the screening run", nil)
			return
		}
	}

	detail, err := h.buildDetail(r.Context(), started)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load session", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, detail)
}

type audioUploadResponse struct {
	AudioID    string `json:"audio_id"`
	Transcript string `json:"transcript"`
	Language   string `json:"language"`
	DurationMS int    `json:"duration_ms"`
	Stored     bool   `json:"stored"`
}

// UploadAudio transcribes one spoken answer and stores the recording. It does
// not send the answer; the client sends the returned audio_id with POST
// /sessions/{token}/messages (S-V4).
func (h *Sessions) UploadAudio(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	if !sess.Voice || sess.RecordingConsent == nil || !*sess.RecordingConsent {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the session is not recording answers", nil)
		return
	}
	if !h.requireActive(w, sess) {
		return
	}
	if h.awaiting == nil || !h.awaiting(sess.ID) {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the assistant is still replying", nil)
		return
	}
	if h.transcriber == nil || h.audioStorage == nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "audio transcription is unavailable", nil)
		return
	}
	if ok, retryAfter := h.audioLimiter.AllowWithRetry(sess.ID); !ok {
		apierror.WriteRateLimited(w, retryAfter)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxAudioUploadBytes+64<<10)
	if err := r.ParseMultipartForm(maxAudioUploadBytes + 64<<10); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			apierror.Write(w, http.StatusRequestEntityTooLarge, apierror.CodeFileTooLarge, "audio exceeds 1 MB", nil)
			return
		}
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid multipart body", nil)
		return
	}
	file, header, err := r.FormFile("audio")
	if err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio field is required", map[string]string{"field": "audio"})
		return
	}
	defer file.Close()

	audio, err := io.ReadAll(io.LimitReader(file, maxAudioUploadBytes+1))
	if err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "could not read the audio", nil)
		return
	}
	if len(audio) > maxAudioUploadBytes {
		apierror.Write(w, http.StatusRequestEntityTooLarge, apierror.CodeFileTooLarge, "audio exceeds 1 MB", nil)
		return
	}
	if len(audio) == 0 {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio is empty", map[string]string{"field": "audio"})
		return
	}
	mediaType, _, err := mime.ParseMediaType(header.Header.Get("Content-Type"))
	if err != nil || !allowedAudioType(mediaType) {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio must be audio/webm, audio/ogg or audio/mp4", map[string]string{"field": "audio"})
		return
	}

	result, err := h.transcriber.Transcribe(r.Context(), audio, mediaType)
	if err != nil {
		slog.Default().Error("transcription failed", "session_id", sess.ID, "error", err)
		apierror.Write(w, http.StatusBadGateway, apierror.CodeTranscriptionUnavailable, "transcription is temporarily unavailable", nil)
		return
	}
	transcript := strings.TrimSpace(result.Transcript)
	if transcript == "" {
		h.recordRejectedAudio(r.Context(), sess.ID, mediaType, len(audio), result)
		apierror.Write(w, http.StatusUnprocessableEntity, apierror.CodeNoSpeech, "no speech was detected", nil)
		return
	}
	if result.DurationMS > voiceMaxAnswerSeconds*1000 {
		h.recordRejectedAudio(r.Context(), sess.ID, mediaType, len(audio), result)
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "the recording is longer than the per-answer limit", map[string]string{"field": "audio"})
		return
	}

	audioID := domain.NewID()
	objectPath := fmt.Sprintf("sessions/%s/%s.%s", sess.ID, audioID, audioObjectExtension(mediaType))
	upload := store.AudioUpload{
		ID:          audioID,
		SessionID:   sess.ID,
		ObjectPath:  objectPath,
		ContentType: mediaType,
		SizeBytes:   len(audio),
		DurationMS:  result.DurationMS,
		Transcript:  transcript,
		Language:    result.Language,
		Words:       result.Words,
		Confidence:  result.Confidence,
		CostUSD:     h.models.TranscriptionCostUSD(result.DurationMS),
		Counted:     true,
		UploadedAt:  time.Now().UTC(),
	}

	// The recording took time to transcribe; serialize the budget read and the
	// counted insert so two uploads cannot both spend the same remaining audio.
	lock := h.audioLock(sess.ID)
	lock.Lock()
	usage, err := h.store.GetAudioUsage(r.Context(), sess.ID)
	if err != nil {
		lock.Unlock()
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load the audio budget", nil)
		return
	}
	if usage.DurationMS+result.DurationMS > voiceMaxTotalSeconds*1000 {
		h.recordRejectedAudio(r.Context(), sess.ID, mediaType, len(audio), result)
		lock.Unlock()
		apierror.Write(w, http.StatusConflict, apierror.CodeAudioBudgetExceeded, "the session audio budget is exhausted", nil)
		return
	}
	if err := h.store.CreateAudioUpload(r.Context(), upload); err != nil {
		lock.Unlock()
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not store the audio", nil)
		return
	}
	lock.Unlock()

	stored := true
	if err := h.audioStorage.Upload(r.Context(), objectPath, audio, mediaType); err != nil {
		stored = false
	}
	if stored {
		if err := h.store.SetAudioUploadStored(r.Context(), audioID, true); err != nil {
			slog.Default().Error("mark audio upload stored failed", "session_id", sess.ID, "error", err)
			stored = false
		}
	}

	apierror.JSON(w, http.StatusOK, audioUploadResponse{
		AudioID:    audioID,
		Transcript: transcript,
		Language:   result.Language,
		DurationMS: result.DurationMS,
		Stored:     stored,
	})
}

// recordRejectedAudio keeps the cost of a transcription the provider billed but
// the session cannot use. The row has no stored object and Counted=false, so it
// raises the run's cost without consuming the audio budget.
func (h *Sessions) recordRejectedAudio(ctx context.Context, sessionID, mediaType string, size int, result transcribe.Result) {
	id := domain.NewID()
	upload := store.AudioUpload{
		ID:          id,
		SessionID:   sessionID,
		ObjectPath:  fmt.Sprintf("sessions/%s/rejected-%s.%s", sessionID, id, audioObjectExtension(mediaType)),
		ContentType: mediaType,
		SizeBytes:   size,
		DurationMS:  result.DurationMS,
		Transcript:  strings.TrimSpace(result.Transcript),
		Language:    result.Language,
		Words:       result.Words,
		Confidence:  result.Confidence,
		CostUSD:     h.models.TranscriptionCostUSD(result.DurationMS),
		Counted:     false,
		UploadedAt:  time.Now().UTC(),
	}
	if err := h.store.CreateAudioUpload(ctx, upload); err != nil {
		slog.Default().Error("store rejected audio upload failed", "session_id", sessionID, "error", err)
	}
}

func allowedAudioType(mediaType string) bool {
	switch mediaType {
	case "audio/webm", "audio/ogg", "audio/mp4":
		return true
	default:
		return false
	}
}

func audioObjectExtension(mediaType string) string {
	switch mediaType {
	case "audio/ogg":
		return "ogg"
	case "audio/mp4":
		return "mp4"
	default:
		return "webm"
	}
}

// resolve looks up the session by token and writes the appropriate lifecycle
// error (404/410) when it is unknown, revoked, expired, or completed. It
// returns ok=false when a response was written.
func (h *Sessions) resolve(w http.ResponseWriter, r *http.Request, token string) (domain.Session, bool) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return domain.Session{}, false
	}

	sess, err := h.store.GetSessionByTokenHash(r.Context(), session.HashToken(token))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeSessionNotFound, "session not found", nil)
			return domain.Session{}, false
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load session", nil)
		return domain.Session{}, false
	}

	switch session.EffectiveStatus(sess, time.Now().UTC()) {
	case domain.SessionStatusRevoked:
		apierror.Write(w, http.StatusNotFound, apierror.CodeSessionNotFound, "session not found", nil)
		return domain.Session{}, false
	case domain.SessionStatusExpired:
		apierror.Write(w, http.StatusGone, apierror.CodeSessionExpired, "session expired", nil)
		return domain.Session{}, false
	case domain.SessionStatusCompleted:
		apierror.Write(w, http.StatusGone, apierror.CodeSessionCompleted, "session completed", nil)
		return domain.Session{}, false
	}
	return sess, true
}

func (h *Sessions) buildDetail(ctx context.Context, sess domain.Session) (sessionDetail, error) {
	app, err := h.store.GetApplication(ctx, sess.ApplicationID)
	if err != nil {
		return sessionDetail{}, err
	}
	job, err := h.store.GetJob(ctx, app.JobID)
	if err != nil {
		return sessionDetail{}, err
	}

	detail := sessionDetail{
		Status:        sess.Status,
		JobTitle:      job.Title,
		ApplicantName: app.Name,
		ExpiresAt:     sess.ExpiresAt,
		StartedAt:     sess.StartedAt,
		MaxMinutes:    h.cfg.SessionMaxMinutes,
		QuestionCount: sess.QuestionCount,
		MaxQuestions:  h.cfg.SessionMaxQuestions,
	}
	if sess.Voice {
		usage, err := h.store.GetAudioUsage(ctx, sess.ID)
		if err != nil {
			return sessionDetail{}, err
		}
		detail.Voice = &voiceSession{
			MaxAnswerSeconds: voiceMaxAnswerSeconds,
			MaxTotalSeconds:  voiceMaxTotalSeconds,
			UsedSeconds:      (usage.DurationMS + 999) / 1000,
			RecordingConsent: sess.RecordingConsent,
		}
	}

	if sess.Status == domain.SessionStatusActive && sess.StartedAt != nil {
		deadline := sess.StartedAt.Add(time.Duration(h.cfg.SessionMaxMinutes) * time.Minute)
		detail.DeadlineAt = &deadline

		topics, err := h.store.ListTopicsBySession(ctx, sess.ID)
		if err != nil {
			return sessionDetail{}, err
		}
		if len(topics) > 0 {
			total := len(topics)
			detail.TopicCount = &total
			for _, topic := range topics {
				if topic.Status == domain.TopicStatusActive {
					detail.CurrentTopic = &currentTopic{Position: topic.Position, Title: topic.Title, Total: total}
					break
				}
			}
		}
	}

	return detail, nil
}

// Messages returns the applicant-visible transcript for resume. System
// messages are never returned.
func (h *Sessions) Messages(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	messages, err := h.store.ListMessagesBySession(r.Context(), sess.ID, false)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load messages", nil)
		return
	}
	if messages == nil {
		messages = []domain.Message{}
	}
	// Entry and audio signals are recruiter-only; the applicant's transcript
	// never carries them.
	for i := range messages {
		messages[i].Entry = nil
		messages[i].Audio = nil
	}
	apierror.JSON(w, http.StatusOK, map[string]any{"items": messages})
}

type sendMessageRequest struct {
	Content *string           `json:"content"`
	AudioID *string           `json:"audio_id"`
	Entry   *sendMessageEntry `json:"entry"`
}

// sendMessageEntry uses pointers so a present object must carry the existing
// counters; a missing counter is malformed rather than a silent zero. The voice
// fields are optional.
type sendMessageEntry struct {
	PastedChars    *int64  `json:"pasted_chars"`
	TypedChars     *int64  `json:"typed_chars"`
	DurationMS     *int64  `json:"duration_ms"`
	Mode           *string `json:"mode"`
	FallbackReason *string `json:"fallback_reason"`
	StartLatencyMS *int64  `json:"start_latency_ms"`
	SpeakingMS     *int64  `json:"speaking_ms"`
	RerecordCount  *int64  `json:"rerecord_count"`
}

const maxEntryCounter = 10_000_000

func (e *sendMessageEntry) validate() (*domain.MessageEntry, map[string]string) {
	if e == nil {
		return nil, nil
	}
	for field, value := range map[string]*int64{
		"entry.pasted_chars": e.PastedChars,
		"entry.typed_chars":  e.TypedChars,
		"entry.duration_ms":  e.DurationMS,
	} {
		if value == nil || *value < 0 || *value > maxEntryCounter {
			return nil, map[string]string{"field": field}
		}
	}
	for field, value := range map[string]*int64{
		"entry.start_latency_ms": e.StartLatencyMS,
		"entry.speaking_ms":      e.SpeakingMS,
		"entry.rerecord_count":   e.RerecordCount,
	} {
		if value != nil && (*value < 0 || *value > maxEntryCounter) {
			return nil, map[string]string{"field": field}
		}
	}
	entry := &domain.MessageEntry{
		PastedChars: int(*e.PastedChars),
		TypedChars:  int(*e.TypedChars),
		DurationMS:  int(*e.DurationMS),
	}
	if e.Mode != nil {
		mode := domain.MessageEntryMode(*e.Mode)
		switch mode {
		case domain.MessageEntryModeTyped, domain.MessageEntryModeSpoken:
			entry.Mode = mode
		default:
			return nil, map[string]string{"field": "entry.mode"}
		}
	}
	if e.FallbackReason != nil {
		reason := domain.MessageEntryFallbackReason(*e.FallbackReason)
		switch reason {
		case domain.MessageEntryFallbackChoseText,
			domain.MessageEntryFallbackDeclinedRecording,
			domain.MessageEntryFallbackMicrophoneUnavailable,
			domain.MessageEntryFallbackTranscriptionFailed:
			entry.FallbackReason = reason
		default:
			return nil, map[string]string{"field": "entry.fallback_reason"}
		}
	}
	if e.StartLatencyMS != nil {
		entry.StartLatencyMS = int(*e.StartLatencyMS)
	}
	if e.SpeakingMS != nil {
		entry.SpeakingMS = int(*e.SpeakingMS)
	}
	if e.RerecordCount != nil {
		entry.RerecordCount = int(*e.RerecordCount)
	}
	return entry, nil
}

type sendMessageResponse struct {
	Message  domain.Message `json:"message"`
	Awaiting string         `json:"awaiting"`
}

// SendMessage stores one applicant answer and starts the next graph step. The
// AI reply streams on the session SSE. A spoken answer is identified by
// audio_id; its content is the server's stored transcript.
func (h *Sessions) SendMessage(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	if !h.requireActive(w, sess) {
		return
	}
	if h.manager == nil {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the session is not running", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req sendMessageRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid JSON body", nil)
		return
	}
	hasContent := req.Content != nil
	hasAudio := req.AudioID != nil
	if hasContent == hasAudio {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "exactly one of content or audio_id is required", nil)
		return
	}
	entry, details := req.Entry.validate()
	if details != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "entry must contain valid counters and voice fields", details)
		return
	}

	var (
		content       string
		audioUploadID string
	)
	if hasAudio {
		audioID := strings.TrimSpace(*req.AudioID)
		if !uuidPattern.MatchString(audioID) {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio_id is not available", map[string]string{"field": "audio_id"})
			return
		}
		if !sess.Voice {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "spoken answers require a voice session", nil)
			return
		}
		if sess.RecordingConsent == nil || !*sess.RecordingConsent {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "recording is not permitted for this session", nil)
			return
		}
		upload, err := h.store.GetAudioUpload(r.Context(), audioID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio_id is not available", map[string]string{"field": "audio_id"})
				return
			}
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load the audio upload", nil)
			return
		}
		if upload.SessionID != sess.ID || !upload.Counted || upload.MessageID != nil {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio_id is not available", map[string]string{"field": "audio_id"})
			return
		}
		content = strings.TrimSpace(upload.Transcript)
		if content == "" {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio_id is not available", map[string]string{"field": "audio_id"})
			return
		}
		if entry == nil {
			entry = &domain.MessageEntry{}
		}
		entry.Mode = domain.MessageEntryModeSpoken
		audioUploadID = upload.ID
	} else {
		content = strings.TrimSpace(*req.Content)
		if n := utf8.RuneCountInString(content); n < 1 || n > maxAnswerChars {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "content must be 1-2000 characters", map[string]string{"field": "content"})
			return
		}
		// A typed answer is always typed, whatever the client claimed: a pasted
		// answer must not hide its paste signals by labelling itself spoken.
		if entry != nil {
			entry.Mode = domain.MessageEntryModeTyped
		}
		if sess.Voice {
			if sess.RecordingConsent != nil && !*sess.RecordingConsent {
				if entry == nil {
					entry = &domain.MessageEntry{}
				}
				entry.Mode = domain.MessageEntryModeTyped
				entry.FallbackReason = domain.MessageEntryFallbackDeclinedRecording
			} else if entry == nil || entry.FallbackReason == "" {
				apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "fallback_reason is required for a typed answer in a voice session", map[string]string{"field": "entry.fallback_reason"})
				return
			}
		}
	}

	if ok, retryAfter := h.messageLimiter.AllowWithRetry(sess.ID); !ok {
		apierror.WriteRateLimited(w, retryAfter)
		return
	}

	var (
		message domain.Message
		err     error
	)
	if audioUploadID != "" {
		message, err = h.manager.SubmitAnswerWithAudio(r.Context(), sess, content, entry, audioUploadID)
	} else {
		message, err = h.manager.SubmitAnswerWithEntry(r.Context(), sess, content, entry)
	}
	if err != nil {
		if audioUploadID != "" && (errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict)) {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "audio_id is not available", map[string]string{"field": "audio_id"})
			return
		}
		if errors.Is(err, graph.ErrInvalidState) {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the assistant is still replying", nil)
			return
		}
		if errors.Is(err, graph.ErrSessionEnded) {
			h.writeSessionEnded(w, r.Context(), sess)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not store the answer", nil)
		return
	}
	apierror.JSON(w, http.StatusAccepted, sendMessageResponse{Message: applicantMessage(message), Awaiting: "assistant"})
}

// applicantMessage hides the recruiter-only entry and audio signals from an
// applicant response. The stored message keeps them.
func applicantMessage(message domain.Message) domain.Message {
	message.Entry = nil
	message.Audio = nil
	return message
}

func sessionEventForApplicant(event domain.NodeEvent) domain.NodeEvent {
	if event.Type != graph.EventMessageCompleted {
		return event
	}
	var payload graph.MessageCompleted
	if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.Message.Entry == nil {
		return event
	}
	payload.Message = applicantMessage(payload.Message)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return event
	}
	event.Payload = encoded
	return event
}

// Retry re-runs the step waiting for a retry (after a retryable error event).
func (h *Sessions) Retry(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	if !h.requireActive(w, sess) {
		return
	}
	if h.manager == nil {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "nothing is waiting for a retry", nil)
		return
	}
	if err := h.manager.Retry(r.Context(), sess); err != nil {
		if errors.Is(err, graph.ErrInvalidState) {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "nothing is waiting for a retry", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not retry the step", nil)
		return
	}
	apierror.JSON(w, http.StatusAccepted, map[string]string{"status": "retrying"})
}

// Events streams the applicant chat: live events plus a filtered replay of the
// persisted ones. message.delta is live-only and never replayed.
func (h *Sessions) Events(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}
	if h.bus == nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "events are unavailable", nil)
		return
	}

	live, cancel := h.bus.SubscribeSession(sess.ID)
	defer cancel()

	writer := sse.NewWriter(w, sse.DefaultWriteDeadline)
	if err := writer.Prepare(); err != nil {
		return
	}

	lastSeq := eventSince(r)
	if run, err := h.store.GetGraphRunBySession(r.Context(), sess.ID); err == nil {
		events, err := h.store.ListNodeEvents(r.Context(), run.ID, lastSeq)
		if err != nil {
			return
		}
		for _, event := range events {
			if !sse.IsSessionEvent(event.Type) || event.Seq <= lastSeq {
				continue
			}
			if err := writer.WriteEvent(sessionEventForApplicant(event)); err != nil {
				return
			}
			lastSeq = event.Seq
			if event.Type == graph.EventSessionEnded {
				return
			}
		}
	} else {
		// No run yet (invited): a Last-Event-ID from an earlier run does not
		// refer to any seq this run will emit.
		lastSeq = 0
	}

	heartbeat := time.NewTicker(sse.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := writer.Heartbeat(); err != nil {
				return
			}
		case event := <-live:
			if !sse.IsSessionEvent(event.Type) && event.Type != graph.EventMessageDelta {
				continue
			}
			if event.Type != graph.EventMessageDelta && event.Seq > 0 && event.Seq <= lastSeq {
				continue
			}
			if err := writer.WriteEvent(sessionEventForApplicant(event)); err != nil {
				return
			}
			if event.Seq > lastSeq {
				lastSeq = event.Seq
			}
			if event.Type == graph.EventSessionEnded {
				return
			}
		}
	}
}

// writeSessionEnded maps a session that finished while the answer was waiting
// to the 410 code that matches its stored state.
func (h *Sessions) writeSessionEnded(w http.ResponseWriter, ctx context.Context, sess domain.Session) {
	status := sess.Status
	if fresh, err := h.store.GetSession(ctx, sess.ID); err == nil {
		status = fresh.Status
	}
	if status == domain.SessionStatusExpired {
		apierror.Write(w, http.StatusGone, apierror.CodeSessionExpired, "session expired", nil)
		return
	}
	apierror.Write(w, http.StatusGone, apierror.CodeSessionCompleted, "session completed", nil)
}

// requireActive enforces the POST message/retry preconditions only the runner
// can satisfy (the session must be running and within its time limit).
func (h *Sessions) requireActive(w http.ResponseWriter, sess domain.Session) bool {
	if sess.Status != domain.SessionStatusActive {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "session has not started", nil)
		return false
	}
	if sess.StartedAt != nil && time.Now().UTC().After(sess.StartedAt.Add(time.Duration(h.cfg.SessionMaxMinutes)*time.Minute)) {
		apierror.Write(w, http.StatusGone, apierror.CodeSessionCompleted, "session time is up", nil)
		return false
	}
	return true
}

// eventSince reads Last-Event-ID (then ?since_seq=) so a reconnect can replay
// the events it has not seen yet.
func eventSince(r *http.Request) int64 {
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	if raw := r.URL.Query().Get("since_seq"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}
