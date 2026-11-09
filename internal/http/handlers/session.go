package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const maxAnswerChars = 2000

type Sessions struct {
	cfg     config.Config
	store   store.Store
	manager *graph.SessionManager
	bus     *graph.Bus
}

func NewSessions(cfg config.Config, st store.Store) *Sessions {
	return &Sessions{cfg: cfg, store: st}
}

// SetChat attaches the live graph manager and event bus. Without them the chat
// endpoints report invalid state (the runner is not available) but the session
// info/start endpoints still work.
func (h *Sessions) SetChat(manager *graph.SessionManager, bus *graph.Bus) {
	h.manager = manager
	h.bus = bus
}

type currentTopic struct {
	Position int    `json:"position"`
	Title    string `json:"title"`
	Total    int    `json:"total"`
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

func (h *Sessions) Start(w http.ResponseWriter, r *http.Request) {
	sess, ok := h.resolve(w, r, chi.URLParam(r, "token"))
	if !ok {
		return
	}

	started, err := h.store.StartSession(r.Context(), sess.ID, time.Now().UTC())
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
	apierror.JSON(w, http.StatusOK, map[string]any{"items": messages})
}

type sendMessageRequest struct {
	Content string `json:"content"`
}

type sendMessageResponse struct {
	Message  domain.Message `json:"message"`
	Awaiting string         `json:"awaiting"`
}

// SendMessage stores one applicant answer and starts the next graph step. The
// AI reply streams on the session SSE.
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
	content := strings.TrimSpace(req.Content)
	if n := utf8.RuneCountInString(content); n < 1 || n > maxAnswerChars {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "content must be 1-2000 characters", map[string]string{"field": "content"})
		return
	}

	message, err := h.manager.SubmitAnswer(r.Context(), sess, content)
	if err != nil {
		if errors.Is(err, graph.ErrInvalidState) {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the assistant is still replying", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not store the answer", nil)
		return
	}
	apierror.JSON(w, http.StatusAccepted, sendMessageResponse{Message: message, Awaiting: "assistant"})
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
			if err := writer.WriteEvent(event); err != nil {
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
			if err := writer.WriteEvent(event); err != nil {
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
