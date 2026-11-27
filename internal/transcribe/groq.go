package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

const defaultGroqBaseURL = "https://api.groq.com/openai/v1"

// Groq calls the OpenAI-compatible Groq transcription endpoint with
// response_format=verbose_json. The language is not sent, so Groq detects it.
type Groq struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// NewGroq builds a Groq client. baseURL is the API root
// (https://api.groq.com/openai/v1); a nil client gets the default HTTP client.
func NewGroq(baseURL, apiKey, model string, client *http.Client) *Groq {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGroqBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Groq{
		endpoint: strings.TrimRight(baseURL, "/") + "/audio/transcriptions",
		apiKey:   apiKey,
		model:    model,
		client:   client,
	}
}

func (g *Groq) Transcribe(ctx context.Context, audio []byte, contentType string) (Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := "answer.webm"
	switch {
	case strings.HasPrefix(contentType, "audio/ogg"):
		filename = "answer.ogg"
	case strings.HasPrefix(contentType, "audio/mp4"):
		filename = "answer.mp4"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return Result{}, fmt.Errorf("%w: build upload: %v", ErrUnavailable, err)
	}
	if _, err := part.Write(audio); err != nil {
		return Result{}, fmt.Errorf("%w: write audio: %v", ErrUnavailable, err)
	}
	if err := writer.WriteField("model", g.model); err != nil {
		return Result{}, fmt.Errorf("%w: write model: %v", ErrUnavailable, err)
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return Result{}, fmt.Errorf("%w: write response_format: %v", ErrUnavailable, err)
	}
	if err := writer.Close(); err != nil {
		return Result{}, fmt.Errorf("%w: close upload: %v", ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, &body)
	if err != nil {
		return Result{}, fmt.Errorf("%w: build request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := g.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: request failed: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The provider body may echo request data; never include it (or the key)
		// in an error that reaches the log.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		return Result{}, fmt.Errorf("%w: provider status %d", ErrUnavailable, resp.StatusCode)
	}

	var payload struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("%w: decode response: %v", ErrUnavailable, err)
	}
	return Result{
		Transcript: strings.TrimSpace(payload.Text),
		Language:   payload.Language,
		DurationMS: int(math.Round(payload.Duration * 1000)),
	}, nil
}
