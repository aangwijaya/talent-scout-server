package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type Sessions struct {
	cfg   config.Config
	store store.Store
}

func NewSessions(cfg config.Config, st store.Store) *Sessions {
	return &Sessions{cfg: cfg, store: st}
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
