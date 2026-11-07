package handlers

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const (
	maxCVBytes        = 2 << 20 // 2 MB
	multipartOverhead = 256 << 10
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type Apply struct {
	cfg     config.Config
	store   store.Store
	storage storage.Storage
	extract cv.Extractor
}

func NewApply(cfg config.Config, st store.Store, s storage.Storage, e cv.Extractor) *Apply {
	return &Apply{cfg: cfg, store: st, storage: s, extract: e}
}

type applyResponse struct {
	ApplicationID string                   `json:"application_id"`
	Status        domain.ApplicationStatus `json:"status"`
	Message       string                   `json:"message"`
}

func (h *Apply) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		apierror.Write(w, http.StatusServiceUnavailable, apierror.CodeDBUnavailable, "database is unavailable", nil)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxCVBytes+multipartOverhead)
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			apierror.Write(w, http.StatusRequestEntityTooLarge, apierror.CodeFileTooLarge, "CV exceeds the 2 MB limit", nil)
			return
		}
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "invalid multipart form", nil)
		return
	}
	defer r.MultipartForm.RemoveAll()

	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))

	if n := utf8.RuneCountInString(name); n < 2 || n > 100 {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "name must be 2-100 characters", map[string]string{"field": "name"})
		return
	}
	if !emailPattern.MatchString(email) {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "email is invalid", map[string]string{"field": "email"})
		return
	}

	file, header, err := r.FormFile("cv")
	if err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeValidationFailed, "cv is required", map[string]string{"field": "cv"})
		return
	}
	defer file.Close()

	if header.Size > maxCVBytes {
		apierror.Write(w, http.StatusRequestEntityTooLarge, apierror.CodeFileTooLarge, "CV exceeds the 2 MB limit", nil)
		return
	}

	jobID := chi.URLParam(r, "jobId")
	job, err := h.store.GetJob(r.Context(), jobID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			apierror.Write(w, http.StatusNotFound, apierror.CodeNotFound, "job not found", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not load job", nil)
		return
	}
	if job.Status == domain.JobStatusClosed {
		apierror.Write(w, http.StatusConflict, apierror.CodeInvalidState, "job is closed", nil)
		return
	}

	if _, err := h.store.GetApplicationByJobEmail(r.Context(), jobID, email); err == nil {
		apierror.Write(w, http.StatusConflict, apierror.CodeAlreadyApplied, "you have already applied to this job", nil)
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not check for duplicate application", nil)
		return
	}

	data, err := io.ReadAll(io.LimitReader(file, maxCVBytes+1))
	if err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeInvalidFile, "could not read CV", nil)
		return
	}
	if len(data) > maxCVBytes {
		apierror.Write(w, http.StatusRequestEntityTooLarge, apierror.CodeFileTooLarge, "CV exceeds the 2 MB limit", nil)
		return
	}

	cvText, err := h.extract.Extract(data)
	if err != nil {
		apierror.Write(w, http.StatusBadRequest, apierror.CodeInvalidFile, "not a PDF or text cannot be extracted", nil)
		return
	}

	app := domain.Application{
		ID:     domain.NewID(),
		JobID:  jobID,
		Name:   name,
		Email:  email,
		CVText: cvText,
	}
	app.CVObjectPath = app.ID + ".pdf"

	if err := h.storage.Upload(r.Context(), app.CVObjectPath, data, "application/pdf"); err != nil {
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not store CV", nil)
		return
	}

	if err := h.store.CreateApplication(r.Context(), app); err != nil {
		if errors.Is(err, store.ErrConflict) {
			apierror.Write(w, http.StatusConflict, apierror.CodeAlreadyApplied, "you have already applied to this job", nil)
			return
		}
		apierror.Write(w, http.StatusInternalServerError, apierror.CodeInternalError, "could not create application", nil)
		return
	}

	apierror.JSON(w, http.StatusCreated, applyResponse{
		ApplicationID: app.ID,
		Status:        domain.ApplicationStatusApplied,
		Message:       applyMessage(h.cfg.EmailMode),
	})
}

func applyMessage(emailMode string) string {
	if emailMode == "gmail" {
		return "Thanks! Your screening link will be sent to you shortly."
	}
	return "Thanks! Your application was received. The recruiter will send your screening link."
}
