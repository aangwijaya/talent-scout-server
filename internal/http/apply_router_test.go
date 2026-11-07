package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/conformance"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	httpapi "github.com/aangwijaya/talent-scout-server/internal/http"
	"github.com/aangwijaya/talent-scout-server/internal/storage"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

type fakeExtractor struct{ text string }

func (f fakeExtractor) Extract([]byte) (string, error) { return f.text, nil }

const applyJobID = "7c9e6679-0000-4000-8000-000000000002"

func applyRouter(t *testing.T, emailMode string) http.Handler {
	t.Helper()
	st := store.NewMemory()
	if err := st.CreateJob(context.Background(), domain.Job{
		ID:          applyJobID,
		Title:       "Senior Frontend Engineer",
		Description: "We are hiring a senior frontend engineer with deep Svelte and React experience.",
		Status:      domain.JobStatusOpen,
	}); err != nil {
		t.Fatalf("seed job: %v", err)
	}
	cfg := config.Config{Version: "0.1.0", LLMMode: "fake", EmailMode: emailMode}
	return httpapi.NewRouter(cfg, httpapi.Deps{
		Store:     st,
		Storage:   storage.NewFake(),
		Extractor: fakeExtractor{text: "Senior engineer"},
	})
}

func applyRequest(t *testing.T, email string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("name", "Budi"); err != nil {
		t.Fatalf("name field: %v", err)
	}
	if err := mw.WriteField("email", email); err != nil {
		t.Fatalf("email field: %v", err)
	}
	fw, err := mw.CreateFormFile("cv", "cv.pdf")
	if err != nil {
		t.Fatalf("cv field: %v", err)
	}
	if _, err := fw.Write([]byte("%PDF-1.4 fake")); err != nil {
		t.Fatalf("cv content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs/"+applyJobID+"/applications", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.RemoteAddr = "1.2.3.4:1234"
	return req
}

func TestApplyMatchesContract(t *testing.T) {
	router := applyRouter(t, "log")
	validator := conformance.New(t)

	req := applyRequest(t, "budi@example.com")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	validator.ValidateResponse(t, req, rec.Result())
}

func TestApplyRateLimit(t *testing.T) {
	router := applyRouter(t, "log")

	for i := 0; i < 5; i++ {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, applyRequest(t, fmt.Sprintf("budi%d@example.com", i)))
		if rec.Code != http.StatusCreated {
			t.Fatalf("request %d status = %d, want 201", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, applyRequest(t, "budi-z@example.com"))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th request status = %d, want 429", rec.Code)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code != "rate_limited" {
		t.Errorf("error.code = %q, want rate_limited", got.Error.Code)
	}
}
