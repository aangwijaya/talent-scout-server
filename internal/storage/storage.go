package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Storage uploads and downloads CV files in the private bucket `cvs`.
// The backend is the only caller; the client never touches Supabase Storage.
type Storage interface {
	Upload(ctx context.Context, path string, data []byte, contentType string) error
}

// Supabase uploads objects to Supabase Storage using the service role key.
type Supabase struct {
	baseURL string
	bucket  string
	apiKey  string
	client  *http.Client
}

func NewSupabase(baseURL, bucket, serviceRoleKey string) *Supabase {
	return &Supabase{
		baseURL: strings.TrimRight(baseURL, "/"),
		bucket:  bucket,
		apiKey:  serviceRoleKey,
		client:  &http.Client{},
	}
}

func (s *Supabase) Upload(ctx context.Context, path string, data []byte, contentType string) error {
	url := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, s.bucket, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("build storage upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("storage upload: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("storage upload: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
