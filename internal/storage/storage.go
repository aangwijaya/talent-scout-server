package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ErrNotFound means the object does not exist in the bucket.
var ErrNotFound = errors.New("storage object not found")

// Storage stores and retrieves files in a private Supabase Storage bucket.
// The backend is the only caller; the client never touches Supabase Storage.
type Storage interface {
	Upload(ctx context.Context, path string, data []byte, contentType string) error
	Download(ctx context.Context, path string) ([]byte, error)
	Delete(ctx context.Context, path string) error
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

func (s *Supabase) Download(ctx context.Context, path string) ([]byte, error) {
	url := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, s.bucket, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build storage download request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("storage download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("storage download: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return io.ReadAll(resp.Body)
}

// Delete removes an object from the bucket. A missing object is not an error,
// so deleting the same path twice is safe.
func (s *Supabase) Delete(ctx context.Context, path string) error {
	endpoint := fmt.Sprintf("%s/storage/v1/object/%s/%s", s.baseURL, s.bucket, path)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build storage delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.apiKey)

	resp, err := s.client.Do(req)
	if err != nil {
		// A *url.Error's text carries the full request URL, object path included;
		// the retention sweeper logs this error, so keep only the cause.
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		return fmt.Errorf("storage delete: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return fmt.Errorf("storage delete: status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
