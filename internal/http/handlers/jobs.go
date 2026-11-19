package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/http/middleware"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	maxJobs                     = 20
	maxJobBodyBytes       int64 = 128 << 10
	maxSuggestionAttempts       = 2
)

type Jobs struct {
	cfg               config.Config
	store             store.Store
	llm               llm.Client
	models            config.ModelsConfig
	suggestionLimiter *middleware.RateLimiter
}

func NewJobs(cfg config.Config, st store.Store, client llm.Client, models config.ModelsConfig) *Jobs {
	return &Jobs{
		cfg:               cfg,
		store:             st,
		llm:               client,
		models:            models,
		suggestionLimiter: middleware.NewRateLimiter(middleware.RubricSuggestionRateLimit, middleware.RubricSuggestionRateWindow),
	}
}

type jobResponse struct {
	ID               string                   `json:"id"`
	Title            string                   `json:"title"`
	Description      string                   `json:"description"`
	Rubric           []domain.RubricCriterion `json:"rubric"`
	Status           domain.JobStatus         `json:"status"`
	ApplyURL         string                   `json:"apply_url"`
	ApplicationCount int                      `json:"application_count"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
}

type publicJobResponse struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	Status      domain.JobStatus `json:"status"`
}

type jobListResponse struct {
	Items      []jobResponse `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type createJobRequest struct {
	Title       string                   `json:"title"`
	Description string                   `json:"description"`
	Rubric      []domain.RubricCriterion `json:"rubric"`
	Status      *domain.JobStatus        `json:"status"`
}

type updateJobRequest struct {
	Title       *string                   `json:"title"`
	Description *string                   `json:"description"`
	Rubric      *[]domain.RubricCriterion `json:"rubric"`
	Status      *domain.JobStatus         `json:"status"`
}

type rubricSuggestionRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type rubricSuggestionResponse struct {
	Rubric []domain.RubricCriterion `json:"rubric"`
}

func (h *Jobs) List(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}

	jobs, err := h.store.ListJobs(r.Context())
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load jobs", nil)
		return
	}

	items := make([]jobResponse, 0, len(jobs))
	for _, job := range jobs {
		item, err := h.jobResponse(r.Context(), job)
		if err != nil {
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job applications", nil)
			return
		}
		items = append(items, item)
	}
	apierror.JSON(w, http.StatusOK, jobListResponse{Items: items})
}

func (h *Jobs) Get(w http.ResponseWriter, r *http.Request) {
	job, ok := h.getJob(w, r)
	if !ok {
		return
	}
	item, err := h.jobResponse(r.Context(), job)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job applications", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, item)
}

func (h *Jobs) Public(w http.ResponseWriter, r *http.Request) {
	job, ok := h.getJob(w, r)
	if !ok {
		return
	}
	apierror.JSON(w, http.StatusOK, publicJobResponse{
		ID:          job.ID,
		Title:       job.Title,
		Description: job.Description,
		Status:      job.Status,
	})
}

// Create stores a recruiter-authored job. The server caps the total number of
// jobs (open and closed, including the seed) so the public demo password cannot
// be used to flood the database.
func (h *Jobs) Create(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}

	var req createJobRequest
	if !decodeJobBody(w, r, &req) {
		return
	}
	status := domain.JobStatusOpen
	if req.Status != nil {
		status = *req.Status
	}
	now := time.Now().UTC()
	job := domain.Job{
		ID:          domain.NewID(),
		Title:       req.Title,
		Description: req.Description,
		Rubric:      req.Rubric,
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if verr := domain.ValidateJob(job); verr != nil {
		writeValidationError(w, verr)
		return
	}

	if err := h.store.CreateJobWithinLimit(r.Context(), job, maxJobs); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the job limit has been reached", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not create job", nil)
		return
	}

	item, err := h.jobResponse(r.Context(), job)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job applications", nil)
		return
	}
	apierror.JSON(w, http.StatusCreated, item)
}

// Update changes any subset of a job. The rubric is immutable once the job has
// an application because stored scores reference its keys; the check and the
// update are one atomic store operation, so an application that arrives during
// the request still conflicts. Title, description and status stay editable.
func (h *Jobs) Update(w http.ResponseWriter, r *http.Request) {
	job, ok := h.getJob(w, r)
	if !ok {
		return
	}

	var req updateJobRequest
	if !decodeJobBody(w, r, &req) {
		return
	}

	if req.Title != nil {
		if verr := domain.ValidateJobTitle(*req.Title); verr != nil {
			writeValidationError(w, verr)
			return
		}
		job.Title = *req.Title
	}
	if req.Description != nil {
		if verr := domain.ValidateJobDescription(*req.Description); verr != nil {
			writeValidationError(w, verr)
			return
		}
		job.Description = *req.Description
	}
	if req.Rubric != nil {
		if verr := domain.ValidateRubric(*req.Rubric); verr != nil {
			writeValidationError(w, verr)
			return
		}
		job.Rubric = *req.Rubric
	}
	if req.Status != nil {
		if verr := domain.ValidateJobStatus(*req.Status); verr != nil {
			writeValidationError(w, verr)
			return
		}
		job.Status = *req.Status
	}

	job.UpdatedAt = time.Now().UTC()
	update := h.store.UpdateJob
	if req.Rubric != nil {
		update = h.store.UpdateJobRubricIfNoApplications
	}
	if err := update(r.Context(), job); err != nil {
		switch {
		case errors.Is(err, store.ErrNotFound):
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "job not found", nil)
		case errors.Is(err, store.ErrConflict):
			apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "the rubric cannot change after an application", nil)
		default:
			apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not update job", nil)
		}
		return
	}

	item, err := h.jobResponse(r.Context(), job)
	if err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job applications", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, item)
}

// SuggestRubric asks the model for a draft rubric. The recruiter edits and
// saves it through create/update; nothing is stored here. The slot is global
// because the shared demo password carries no recruiter identity.
func (h *Jobs) SuggestRubric(w http.ResponseWriter, r *http.Request) {
	var req rubricSuggestionRequest
	if !decodeJobBody(w, r, &req) {
		return
	}
	if verr := domain.ValidateJobTitle(req.Title); verr != nil {
		writeValidationError(w, verr)
		return
	}
	if verr := domain.ValidateJobDescription(req.Description); verr != nil {
		writeValidationError(w, verr)
		return
	}
	if h.llm == nil {
		apierror.Write(w, http.StatusBadGateway, apierror.CodeLLMUnavailable, "rubric suggestion is unavailable", nil)
		return
	}

	if ok, retryAfter := h.suggestionLimiter.AllowWithRetry(middleware.RubricSuggestionRateKey); !ok {
		apierror.WriteRateLimited(w, retryAfter)
		return
	}

	rubric, err := h.suggestRubric(r.Context(), req.Title, req.Description)
	if err != nil {
		if !errors.Is(err, errInvalidSuggestion) {
			h.suggestionLimiter.Refund(middleware.RubricSuggestionRateKey)
		}
		apierror.Write(w, http.StatusBadGateway, apierror.CodeLLMUnavailable, "rubric suggestion is unavailable", nil)
		return
	}
	apierror.JSON(w, http.StatusOK, rubricSuggestionResponse{Rubric: rubric})
}

// errInvalidSuggestion means the model answered twice with output that failed
// validation. Unlike a provider error, it keeps its rate-limit slot, so a
// description written to make the model fail can't bypass the hourly cap.
var errInvalidSuggestion = errors.New("rubric suggestion failed validation")

func (h *Jobs) suggestRubric(ctx context.Context, title, description string) ([]domain.RubricCriterion, error) {
	node, err := h.models.Node("rubric_suggester")
	if err != nil {
		return nil, err
	}
	req := llm.Request{
		Node:           "rubric_suggester",
		Model:          node.Model,
		Messages:       prompts.RubricSuggesterMessages(title, description),
		Temperature:    h.models.Temperature("rubric_suggester"),
		MaxTokens:      node.MaxTokens,
		Timeout:        h.models.Timeout(),
		MaxRetries:     h.models.MaxRetries(),
		ResponseFormat: rubricSuggestionResponseFormat(),
	}

	for attempt := 0; attempt < maxSuggestionAttempts; attempt++ {
		resp, err := h.llm.Chat(ctx, req)
		if err != nil {
			return nil, err
		}
		rubric, err := parseRubricSuggestion(resp.Content)
		if err == nil {
			return rubric, nil
		}
	}
	return nil, errInvalidSuggestion
}

func parseRubricSuggestion(content string) ([]domain.RubricCriterion, error) {
	var raw rubricSuggestionResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &raw); err != nil {
		return nil, err
	}
	if verr := domain.ValidateRubric(raw.Rubric); verr != nil {
		return nil, verr
	}
	return raw.Rubric, nil
}

func rubricSuggestionResponseFormat() *llm.ResponseFormat {
	stringProperty := map[string]any{"type": "string"}
	criterion := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"key":         stringProperty,
			"label":       stringProperty,
			"description": stringProperty,
			"weight":      map[string]any{"type": "integer", "minimum": 1, "maximum": 3},
		},
		"required":             []string{"key", "label", "description", "weight"},
		"additionalProperties": false,
	}
	return &llm.ResponseFormat{
		Name: "rubric_suggestion",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"rubric": map[string]any{
					"type":     "array",
					"items":    criterion,
					"minItems": 3,
					"maxItems": 6,
				},
			},
			"required":             []string{"rubric"},
			"additionalProperties": false,
		},
	}
}

func decodeJobBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJobBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid JSON body", nil)
		return false
	}
	return true
}

func writeValidationError(w http.ResponseWriter, err *domain.ValidationError) {
	apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, err.Message, map[string]string{"field": err.Field})
}

func (h *Jobs) getJob(w http.ResponseWriter, r *http.Request) (domain.Job, bool) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return domain.Job{}, false
	}

	id := chi.URLParam(r, "jobId")
	if !uuidPattern.MatchString(id) {
		apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "job not found", nil)
		return domain.Job{}, false
	}
	job, err := h.store.GetJob(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "job not found", nil)
			return domain.Job{}, false
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job", nil)
		return domain.Job{}, false
	}
	return job, true
}

func (h *Jobs) jobResponse(ctx context.Context, job domain.Job) (jobResponse, error) {
	applicationCount, err := h.store.CountApplicationsByJob(ctx, job.ID)
	if err != nil {
		return jobResponse{}, err
	}
	return jobResponse{
		ID:               job.ID,
		Title:            job.Title,
		Description:      job.Description,
		Rubric:           append([]domain.RubricCriterion{}, job.Rubric...),
		Status:           job.Status,
		ApplyURL:         strings.TrimRight(h.cfg.PublicAppURL, "/") + "/apply/" + job.ID,
		ApplicationCount: applicationCount,
		CreatedAt:        job.CreatedAt,
		UpdatedAt:        job.UpdatedAt,
	}, nil
}
