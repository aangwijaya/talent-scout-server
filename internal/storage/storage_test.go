package storage_test

import (
	"context"
	"errors"
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

func TestSupabaseDownload(t *testing.T) {
	var gotPath, gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 body"))
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "cvs", "secret-key")
	data, err := s.Download(context.Background(), "abc.pdf")
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}

	if gotPath != "/storage/v1/object/cvs/abc.pdf" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if string(data) != "%PDF-1.4 body" {
		t.Errorf("body = %q", data)
	}
}

func TestSupabaseDownloadNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "cvs", "secret-key")
	_, err := s.Download(context.Background(), "missing.pdf")
	if !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Download() error = %v, want ErrNotFound", err)
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Errorf("error must not leak the service role key: %v", err)
	}
}

func TestFakeDownload(t *testing.T) {
	f := storage.NewFake()
	if err := f.Upload(context.Background(), "abc.pdf", []byte("data"), "application/pdf"); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	got, err := f.Download(context.Background(), "abc.pdf")
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	if string(got) != "data" {
		t.Errorf("Download() = %q, want data", got)
	}
	if _, err := f.Download(context.Background(), "missing.pdf"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Download() missing error = %v, want ErrNotFound", err)
	}
}

func TestSupabaseDelete(t *testing.T) {
	var gotMethod, gotPath, gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "audio", "secret-key")
	if err := s.Delete(context.Background(), "sessions/s1/upload.webm"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/storage/v1/object/audio/sessions/s1/upload.webm" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("auth header = %q", gotAuth)
	}
}

func TestSupabaseDeleteMissingIsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "audio", "secret-key")
	if err := s.Delete(context.Background(), "missing.webm"); err != nil {
		t.Fatalf("Delete() of a missing object = %v, want nil", err)
	}
}

func TestSupabaseDeleteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer server.Close()

	s := storage.NewSupabase(server.URL, "audio", "secret-key")
	err := s.Delete(context.Background(), "upload.webm")
	if err == nil {
		t.Fatal("Delete() expected an error for a non-200 response")
	}
	if strings.Contains(err.Error(), "secret-key") {
		t.Errorf("error must not leak the service role key: %v", err)
	}
}

func TestFakeDelete(t *testing.T) {
	f := storage.NewFake()
	if err := f.Upload(context.Background(), "upload.webm", []byte("data"), "audio/webm"); err != nil {
		t.Fatalf("Upload() error: %v", err)
	}
	if err := f.Delete(context.Background(), "upload.webm"); err != nil {
		t.Fatalf("Delete() error: %v", err)
	}
	if _, ok := f.Get("upload.webm"); ok {
		t.Error("object still present after Delete()")
	}
	if err := f.Delete(context.Background(), "missing.webm"); err != nil {
		t.Fatalf("Delete() of a missing object = %v, want nil", err)
	}
}
