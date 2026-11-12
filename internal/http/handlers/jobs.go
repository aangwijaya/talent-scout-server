package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type Jobs struct {
	cfg   config.Config
	store store.Store
}

func NewJobs(cfg config.Config, st store.Store) *Jobs {
	return &Jobs{cfg: cfg, store: st}
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
