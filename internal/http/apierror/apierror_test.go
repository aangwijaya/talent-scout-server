package apierror_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/http/apierror"
)

func TestWrite(t *testing.T) {
	rec := httptest.NewRecorder()

	apierror.Write(rec, http.StatusBadRequest, apierror.CodeValidationFailed, "email is invalid", map[string]any{"field": "email"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}

	var got struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Error.Code != "validation_failed" {
		t.Errorf("code = %q", got.Error.Code)
	}
	if got.Error.Message != "email is invalid" {
		t.Errorf("message = %q", got.Error.Message)
	}
	if got.Error.Details["field"] != "email" {
		t.Errorf("details.field = %v", got.Error.Details["field"])
	}
}

func TestWriteWithoutDetailsIncludesNull(t *testing.T) {
	rec := httptest.NewRecorder()

	apierror.Write(rec, http.StatusInternalServerError, apierror.CodeInternalError, "something broke", nil)

	var got map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	details, ok := got["error"]["details"]
	if !ok {
		t.Fatal("details field missing")
	}
	if details != nil {
		t.Errorf("details = %v, want null", details)
	}
}
