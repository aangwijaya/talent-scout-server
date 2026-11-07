package storage_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/storage"
)

func TestSupabaseUpload(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"Key":"cvs/abc.pdf"}`))
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "cvs", "secret-key")
	err := s.Upload(context.Background(), "abc.pdf", []byte("%PDF-1.4"), "application/pdf")
	if err != nil {
		t.Fatalf("Upload() error: %v", err)
	}

	if gotPath != "/storage/v1/object/cvs/abc.pdf" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotContentType != "application/pdf" {
		t.Errorf("content type = %q", gotContentType)
	}
	if string(gotBody) != "%PDF-1.4" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestSupabaseUploadError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "cvs", "secret-key")
	err := s.Upload(context.Background(), "abc.pdf", []byte("%PDF-1.4"), "application/pdf")
	if err == nil {
		t.Fatal("Upload() expected an error for a non-200 response")
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Errorf("error must not leak the service role key: %v", err)
	}
}

func TestFakeStoresAndReturns(t *testing.T) {
	f := storage.NewFake()
	if err := f.Upload(context.Background(), "abc.pdf", []byte("data"), "application/pdf"); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	got, ok := f.Get("abc.pdf")
	if !ok {
		t.Fatal("Get() did not find uploaded object")
	}
	if string(got) != "data" {
		t.Errorf("Get() = %q, want data", got)
	}
}
