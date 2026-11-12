package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/email"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/session"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	defaultListLimit = 50
	maxListLimit     = 100
	maxDecisionNote  = 1000
)

type Applications struct {
	cfg     config.Config
	store   store.Store
	storage storage.Storage
	mailer  email.Mailer
}

func NewApplications(cfg config.Config, st store.Store, s storage.Storage, m email.Mailer) *Applications {
	return &Applications{cfg: cfg, store: st, storage: s, mailer: m}
}

type applicationSummary struct {
	ID            string                   `json:"id"`
	JobID         string                   `json:"job_id"`
	Name          string                   `json:"name"`
	Email         string                   `json:"email"`
	Status        domain.ApplicationStatus `json:"status"`
	SessionStatus *domain.SessionStatus    `json:"session_status"`
	OverallScore  *float64                 `json:"overall_score"`
	CreatedAt     time.Time                `json:"created_at"`
}

type applicationListResponse struct {
	Items      []applicationSummary `json:"items"`
	NextCursor *string              `json:"next_cursor"`
}

type applicationJob struct {
	ID     string                   `json:"id"`
	Title  string                   `json:"title"`
	Rubric []domain.RubricCriterion `json:"rubric"`
}

type decisionResponse struct {
	Value     string    `json:"value"`
	Note      string    `json:"note"`
	DecidedAt time.Time `json:"decided_at"`
}

type sessionSummary struct {
	ID            string               `json:"id"`
	Status        domain.SessionStatus `json:"status"`
	ExpiresAt     time.Time            `json:"expires_at"`
	StartedAt     *time.Time           `json:"started_at"`
	EndedAt       *time.Time           `json:"ended_at"`
	FinishReason  *domain.FinishReason `json:"finish_reason"`
	QuestionCount int                  `json:"question_count"`
}

type graphRunSummary struct {
	ID      string                `json:"id"`
	Status  domain.GraphRunStatus `json:"status"`
	CostUSD float64               `json:"cost_usd"`
}

type applicationDetail struct {
	Application applicationSummary `json:"application"`
	Job         applicationJob     `json:"job"`
	CVText      string             `json:"cv_text"`
	Decision    *decisionResponse  `json:"decision"`
	Session     *sessionSummary    `json:"session"`
	GraphRun    *graphRunSummary   `json:"graph_run"`
	Topics      []domain.Topic     `json:"topics"`
	Messages    []domain.Message   `json:"messages"`
	Scores      []domain.Score     `json:"scores"`
}

type decisionRequest struct {
	Decision string `json:"decision"`
	Note     string `json:"note"`
}

type resendInviteResponse struct {
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
	InviteURL *string   `json:"invite_url"`
}

// ListByJob returns the applicant summaries for one job, optionally filtered by
// application status.
func (h *Applications) ListByJob(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}
	jobID := chi.URLParam(r, "jobId")
	if !uuidPattern.MatchString(jobID) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "job not found", nil)
		return
	}
	status, ok := parseApplicationStatus(r.URL.Query().Get("status"))
	if !ok {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "status is invalid", map[string]string{"field": "status"})
		return
	}
	start, limit, ok := listWindow(w, r)
	if !ok {
		return
	}

	apps, err := h.store.ListApplicationsByJob(r.Context(), jobID, status)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load applications", nil)
		return
	}
	job, err := h.store.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.JSON(w, http.StatusOK, applicationListResponse{Items: []applicationSummary{}})
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job", nil)
		return
	}

	if start > len(apps) {
		start = len(apps)
	}
	end := start + limit
	if end > len(apps) {
		end = len(apps)
	}
	items := make([]applicationSummary, 0, end-start)
	for _, app := range apps[start:end] {
		item, err := h.summary(r.Context(), app, job.Rubric)
		if err != nil {
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load applications", nil)
			return
		}
		items = append(items, item)
	}
	var next *string
	if end < len(apps) {
		cursor := strconv.Itoa(end)
		next = &cursor
	}
	apierror.JSON(w, http.StatusOK, applicationListResponse{Items: items, NextCursor: next})
}

func (h *Applications) Get(w http.ResponseWriter, r *http.Request) {
	app, ok := h.getApplication(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	job, err := h.store.GetJob(ctx, app.JobID)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job", nil)
		return
	}

	detail := applicationDetail{
		Job:      applicationJob{ID: job.ID, Title: job.Title, Rubric: append([]domain.RubricCriterion{}, job.Rubric...)},
		CVText:   app.CVText,
		Topics:   []domain.Topic{},
		Messages: []domain.Message{},
		Scores:   []domain.Score{},
	}

	scores, err := h.store.ListScoresByApplication(ctx, app.ID)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load scores", nil)
		return
	}
	for i := range scores {
		if scores[i].Evidence == nil {
			scores[i].Evidence = []domain.ScoreEvidence{}
		}
	}
	detail.Scores = scores

	sess, err := h.store.GetLatestSessionByApplication(ctx, app.ID)
	switch {
	case err == nil:
		status := session.EffectiveStatus(sess, time.Now().UTC())
		detail.Session = &sessionSummary{
			ID:            sess.ID,
			Status:        status,
			ExpiresAt:     sess.ExpiresAt,
			StartedAt:     sess.StartedAt,
			EndedAt:       sess.EndedAt,
			FinishReason:  sess.FinishReason,
			QuestionCount: sess.QuestionCount,
		}
		topics, err := h.store.ListTopicsBySession(ctx, sess.ID)
		if err != nil {
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load topics", nil)
			return
		}
		detail.Topics = topics
		messages, err := h.store.ListMessagesBySession(ctx, sess.ID, false)
		if err != nil {
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load transcript", nil)
			return
		}
		detail.Messages = messages
		run, err := h.store.GetGraphRunBySession(ctx, sess.ID)
		switch {
		case err == nil:
			detail.GraphRun = &graphRunSummary{ID: run.ID, Status: run.Status, CostUSD: run.CostUSD}
		case !errors.Is(err, store.ErrNotFound):
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load graph run", nil)
			return
		}
	case !errors.Is(err, store.ErrNotFound):
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load session", nil)
		return
	}

	summary, err := h.summary(ctx, app, job.Rubric)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load application", nil)
		return
	}
	detail.Application = summary

	if app.DecidedAt != nil {
		if value := decisionValue(app.Status); value != "" {
			detail.Decision = &decisionResponse{Value: value, Note: app.DecisionNote, DecidedAt: *app.DecidedAt}
		}
	}
	apierror.JSON(w, http.StatusOK, detail)
}

func (h *Applications) DownloadCV(w http.ResponseWriter, r *http.Request) {
	app, ok := h.getApplication(w, r)
	if !ok {
		return
	}
	if app.CVObjectPath == "" {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "CV not found", nil)
		return
	}
	if h.storage == nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "storage is unavailable", nil)
		return
	}
	data, err := h.storage.Download(r.Context(), app.CVObjectPath)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "CV not found", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load CV", nil)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+app.ID+`.pdf"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Applications) Decide(w http.ResponseWriter, r *http.Request) {
	app, ok := h.getApplication(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	var req decisionRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid JSON body", nil)
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != "shortlist" && decision != "reject" {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "decision must be shortlist or reject", map[string]string{"field": "decision"})
		return
	}
	if utf8.RuneCountInString(req.Note) > maxDecisionNote {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "note must be at most 1000 characters", map[string]string{"field": "note"})
		return
	}
	switch app.Status {
	case domain.ApplicationStatusReview, domain.ApplicationStatusShortlisted, domain.ApplicationStatusRejected:
	default:
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "application is not ready for a decision", nil)
		return
	}

	status := domain.ApplicationStatusShortlisted
	if decision == "reject" {
		status = domain.ApplicationStatusRejected
	}
	updated, err := h.store.SetApplicationDecision(r.Context(), app.ID, status, req.Note, time.Now().UTC())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "application not found", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not store the decision", nil)
		return
	}
	job, err := h.store.GetJob(r.Context(), updated.JobID)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job", nil)
		return
	}
	summary, err := h.summary(r.Context(), updated, job.Rubric)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load application", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, summary)
}

func (h *Applications) ResendInvite(w http.ResponseWriter, r *http.Request) {
	app, ok := h.getApplication(w, r)
	if !ok {
		return
	}

	now := time.Now().UTC()
	var revokeID *string
	allowed := app.Status == domain.ApplicationStatusFailed
	sess, err := h.store.GetLatestSessionByApplication(r.Context(), app.ID)
	switch {
	case err == nil:
		if sess.Status == domain.SessionStatusInvited {
			id := sess.ID
			revokeID = &id
		}
		if session.EffectiveStatus(sess, now) == domain.SessionStatusInvited {
			allowed = true
		}
	case !errors.Is(err, store.ErrNotFound):
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load session", nil)
		return
	}
	if !allowed {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "application has no invite to resend", nil)
		return
	}

	token, tokenHash, err := session.NewToken()
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not create session", nil)
		return
	}
	next := domain.Session{
		ID:            domain.NewID(),
		ApplicationID: app.ID,
		TokenHash:     tokenHash,
		Status:        domain.SessionStatusInvited,
		ExpiresAt:     now.Add(time.Duration(h.cfg.SessionTTLHours) * time.Hour),
	}
	if err := h.store.ResendInvite(r.Context(), revokeID, next); err != nil {
		switch {
		case errors.Is(err, store.ErrConflict):
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "application has no invite to resend", nil)
		case errors.Is(err, store.ErrNotFound):
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "application not found", nil)
		default:
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not create session", nil)
		}
		return
	}

	link := session.InviteURL(h.cfg.PublicAppURL, token)
	if h.mailer != nil {
		if err := h.mailer.SendInvite(r.Context(), app.Email, app.Name, link); err != nil {
			slog.Error("send screening invite failed", "error", err)
		}
	}
	resp := resendInviteResponse{SessionID: next.ID, ExpiresAt: next.ExpiresAt}
	if h.cfg.EmailMode != "gmail" {
		resp.InviteURL = &link
	}
	apierror.JSON(w, http.StatusOK, resp)
}

func (h *Applications) getApplication(w http.ResponseWriter, r *http.Request) (domain.Application, bool) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return domain.Application{}, false
	}
	id := chi.URLParam(r, "applicationId")
	if !uuidPattern.MatchString(id) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "application not found", nil)
		return domain.Application{}, false
	}
	app, err := h.store.GetApplication(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "application not found", nil)
			return domain.Application{}, false
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load application", nil)
		return domain.Application{}, false
	}
	return app, true
}

func (h *Applications) summary(ctx context.Context, app domain.Application, rubric []domain.RubricCriterion) (applicationSummary, error) {
	summary := applicationSummary{
		ID:        app.ID,
		JobID:     app.JobID,
		Name:      app.Name,
		Email:     app.Email,
		Status:    app.Status,
		CreatedAt: app.CreatedAt,
	}
	sess, err := h.store.GetLatestSessionByApplication(ctx, app.ID)
	switch {
	case err == nil:
		status := session.EffectiveStatus(sess, time.Now().UTC())
		summary.SessionStatus = &status
	case !errors.Is(err, store.ErrNotFound):
		return applicationSummary{}, err
	}
	scores, err := h.store.ListScoresByApplication(ctx, app.ID)
	if err != nil {
		return applicationSummary{}, err
	}
	summary.OverallScore = overallScore(scores, rubric)
	return summary, nil
}

func overallScore(scores []domain.Score, rubric []domain.RubricCriterion) *float64 {
	weights := make(map[string]int, len(rubric))
	for _, criterion := range rubric {
		weights[criterion.Key] = criterion.Weight
	}
	sum := 0
	totalWeight := 0
	for _, score := range scores {
		if score.Status != domain.ScoreStatusValid || score.Score == nil {
			continue
		}
		weight, ok := weights[score.RubricKey]
		if !ok || weight <= 0 {
			continue
		}
		sum += *score.Score * weight
		totalWeight += weight
	}
	if totalWeight == 0 {
		return nil
	}
	value := float64(sum) / float64(totalWeight)
	return &value
}

func decisionValue(status domain.ApplicationStatus) string {
	switch status {
	case domain.ApplicationStatusShortlisted:
		return "shortlist"
	case domain.ApplicationStatusRejected:
		return "reject"
	default:
		return ""
	}
}

func parseApplicationStatus(raw string) (*domain.ApplicationStatus, bool) {
	if raw == "" {
		return nil, true
	}
	status := domain.ApplicationStatus(raw)
	switch status {
	case domain.ApplicationStatusApplied,
		domain.ApplicationStatusInvited,
		domain.ApplicationStatusInSession,
		domain.ApplicationStatusScoring,
		domain.ApplicationStatusReview,
		domain.ApplicationStatusShortlisted,
		domain.ApplicationStatusRejected,
		domain.ApplicationStatusFailed:
		return &status, true
	default:
		return nil, false
	}
}

// listWindow parses ?limit= and ?cursor=. The cursor is the opaque offset the
// previous page returned as next_cursor.
func listWindow(w http.ResponseWriter, r *http.Request) (start, limit int, ok bool) {
	limit = defaultListLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxListLimit {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "limit must be between 1 and 100", map[string]string{"field": "limit"})
			return 0, 0, false
		}
		limit = n
	}
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "cursor is invalid", map[string]string{"field": "cursor"})
			return 0, 0, false
		}
		start = n
	}
	return start, limit, true
}
