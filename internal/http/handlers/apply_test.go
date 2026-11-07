package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/cv"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/http/handlers"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

const jobID = "7c9e6679-0000-4000-8000-000000000001"

type fakeExtractor struct {
	text string
	err  error
}

func (f fakeExtractor) Extract([]byte) (string, error) { return f.text, f.err }

func seedJob(t *testing.T, st store.Store, status domain.JobStatus) {
	t.Helper()
	job := domain.Job{
		ID:          jobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      status,
	}
	if err := st.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("seed job: %v", err)
	}
}

func newApply(t *testing.T, emailMode string, extract cv.Extractor) (*handlers.Apply, *storage.Fake) {
	t.Helper()
	st := store.NewMemory()
	seedJob(t, st, domain.JobStatusOpen)
	fakeStorage := storage.NewFake()
	cfg := config.Config{EmailMode: emailMode}
	return handlers.NewApply(cfg, st, fakeStorage, extract), fakeStorage
}

func multipartBody(t *testing.T, name, email, filename string, content []byte) (*http.Request, string) {
	return multipartBodyJob(t, jobID, name, email, filename, content), ""
}

func multipartBodyJob(t *testing.T, id, name, email, filename string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if name != "" {
		if err := mw.WriteField("name", name); err != nil {
			t.Fatalf("write name field: %v", err)
		}
	}
	if email != "" {
		if err := mw.WriteField("email", email); err != nil {
			t.Fatalf("write email field: %v", err)
		}
	}
	if filename != "" {
		fw, err := mw.CreateFormFile("cv", filename)
		if err != nil {
			t.Fatalf("create cv file: %v", err)
		}
		if _, err := fw.Write(content); err != nil {
			t.Fatalf("write cv: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+id+"/applications", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobId", id)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	return req
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) (code, message string, details map[string]string) {
	t.Helper()
	var got struct {
		Error struct {
			Code    string            `json:"code"`
			Message string            `json:"message"`
			Details map[string]string `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	return got.Error.Code, got.Error.Message, got.Error.Details
}

func TestApplySuccessLogMode(t *testing.T) {
	apply, fakeStorage := newApply(t, "log", fakeExtractor{text: "Senior engineer with Svelte experience."})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4 fake"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		ApplicationID string `json:"application_id"`
		Status        string `json:"status"`
		Message       string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ApplicationID == "" {
		t.Error("application_id is empty")
	}
	if got.Status != "invited" {
		t.Errorf("status = %q, want invited", got.Status)
	}
	if got.Message != "Thanks! Your application was received. The recruiter will send your screening link." {
		t.Errorf("message = %q", got.Message)
	}

	stored, ok := fakeStorage.Get(got.ApplicationID + ".pdf")
	if !ok {
		t.Fatalf("cv not uploaded to storage")
	}
	if !bytes.Equal(stored, []byte("%PDF-1.4 fake")) {
		t.Errorf("stored cv bytes mismatch")
	}
}

func TestApplySuccessGmailMessage(t *testing.T) {
	apply, _ := newApply(t, "gmail", fakeExtractor{text: "some text"})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4 fake"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var got struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Message != "Thanks! Your screening link will be sent to you shortly." {
		t.Errorf("message = %q", got.Message)
	}
}

func TestApplyValidation(t *testing.T) {
	tests := []struct {
		name        string
		fieldsName  string
		fieldsEmail string
		filename    string
		content     []byte
		wantCode    string
		wantField   string
	}{
		{name: "short name", fieldsName: "A", fieldsEmail: "budi@example.com", filename: "cv.pdf", content: []byte("%PDF-1.4"), wantCode: "validation_failed", wantField: "name"},
		{name: "invalid email", fieldsName: "Budi", fieldsEmail: "not-an-email", filename: "cv.pdf", content: []byte("%PDF-1.4"), wantCode: "validation_failed", wantField: "email"},
		{name: "missing cv", fieldsName: "Budi", fieldsEmail: "budi@example.com", filename: "", wantCode: "validation_failed", wantField: "cv"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			apply, _ := newApply(t, "log", fakeExtractor{text: "text"})
			req, _ := multipartBody(t, tc.fieldsName, tc.fieldsEmail, tc.filename, tc.content)
			rec := httptest.NewRecorder()

			apply.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			code, _, details := decodeError(t, rec)
			if code != tc.wantCode {
				t.Errorf("code = %q, want %q", code, tc.wantCode)
			}
			if details["field"] != tc.wantField {
				t.Errorf("details.field = %q, want %q", details["field"], tc.wantField)
			}
		})
	}
}

func TestApplyInvalidFile(t *testing.T) {
	apply, _ := newApply(t, "log", fakeExtractor{err: cv.ErrNotPDF})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.txt", []byte("not a pdf"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "invalid_file" {
		t.Errorf("code = %q, want invalid_file", code)
	}
}

func TestApplyFileTooLarge(t *testing.T) {
	apply, _ := newApply(t, "log", fakeExtractor{text: "text"})
	big := bytes.Repeat([]byte("a"), 2<<20+1)
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", big)
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "file_too_large" {
		t.Errorf("code = %q, want file_too_large", code)
	}
}

func TestApplyDuplicate(t *testing.T) {
	apply, _ := newApply(t, "log", fakeExtractor{text: "text"})

	first, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	apply.ServeHTTP(httptest.NewRecorder(), first)

	second, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()
	apply.ServeHTTP(rec, second)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "already_applied" {
		t.Errorf("code = %q, want already_applied", code)
	}
}

func TestApplyEmailCaseInsensitiveDuplicate(t *testing.T) {
	apply, _ := newApply(t, "log", fakeExtractor{text: "text"})

	first, _ := multipartBody(t, "Budi", "Budi@Example.com", "cv.pdf", []byte("%PDF-1.4"))
	apply.ServeHTTP(httptest.NewRecorder(), first)

	second, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()
	apply.ServeHTTP(rec, second)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "already_applied" {
		t.Errorf("code = %q, want already_applied", code)
	}
}

type nonUUIDStore struct{ store.Store }

func (nonUUIDStore) GetJob(context.Context, string) (domain.Job, error) {
	return domain.Job{}, errors.New("invalid input syntax for type uuid")
}

func TestApplyJobIDNotUUID(t *testing.T) {
	apply := handlers.NewApply(config.Config{EmailMode: "log"}, nonUUIDStore{}, storage.NewFake(), fakeExtractor{text: "text"})
	req := multipartBodyJob(t, "abc", "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body: %s)", rec.Code, rec.Body.String())
	}
	code, _, _ := decodeError(t, rec)
	if code != "not_found" {
		t.Errorf("code = %q, want not_found", code)
	}
}

func TestApplyNilStore(t *testing.T) {
	apply := handlers.NewApply(config.Config{EmailMode: "log"}, nil, storage.NewFake(), fakeExtractor{text: "text"})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "db_unavailable" {
		t.Errorf("code = %q, want db_unavailable", code)
	}
}

func TestApplyJobNotFound(t *testing.T) {
	st := store.NewMemory()
	apply := handlers.NewApply(config.Config{EmailMode: "log"}, st, storage.NewFake(), fakeExtractor{text: "text"})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "not_found" {
		t.Errorf("code = %q, want not_found", code)
	}
}

func TestApplyJobClosed(t *testing.T) {
	st := store.NewMemory()
	seedJob(t, st, domain.JobStatusClosed)
	apply := handlers.NewApply(config.Config{EmailMode: "log"}, st, storage.NewFake(), fakeExtractor{text: "text"})
	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()

	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "invalid_state" {
		t.Errorf("code = %q, want invalid_state", code)
	}
}

func TestApplyStorageUploadFailure(t *testing.T) {
	st := store.NewMemory()
	seedJob(t, st, domain.JobStatusOpen)
	fakeStorage := storage.NewFake()
	fakeStorage.SetErr(errors.New("bucket missing"))
	apply := handlers.NewApply(config.Config{EmailMode: "log"}, st, fakeStorage, fakeExtractor{text: "text"})

	req, _ := multipartBody(t, "Budi", "budi@example.com", "cv.pdf", []byte("%PDF-1.4"))
	rec := httptest.NewRecorder()
	apply.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	code, _, _ := decodeError(t, rec)
	if code != "internal_error" {
		t.Errorf("code = %q, want internal_error", code)
	}
}
