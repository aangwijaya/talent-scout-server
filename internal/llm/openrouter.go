package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenRouter is the OpenAI-compatible client backed by OpenRouter.
type OpenRouter struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// NewOpenRouter returns a client using the given base URL (e.g.
// https://openrouter.ai/api/v1) and API key. A nil http.Client defaults to one
// with a 60s timeout; per-request timeouts still come from Request.Timeout.
func NewOpenRouter(baseURL, apiKey string, client *http.Client) *OpenRouter {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenRouter{baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey, client: client}
}

type chatRequest struct {
	Model          string           `json:"model"`
	Messages       []Message        `json:"messages"`
	Temperature    float64          `json:"temperature,omitempty"`
	MaxTokens      int              `json:"max_tokens,omitempty"`
	Stream         bool             `json:"stream,omitempty"`
	ResponseFormat *responseFormat  `json:"response_format,omitempty"`
	Provider       *provider        `json:"provider,omitempty"`
	Reasoning      *ReasoningConfig `json:"reasoning,omitempty"`
}

type responseFormat struct {
	Type       string     `json:"type"`
	JSONSchema jsonSchema `json:"json_schema"`
}

type jsonSchema struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type provider struct {
	RequireParameters bool `json:"require_parameters"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (c *OpenRouter) buildRequest(req Request) chatRequest {
	body := chatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
	}
	if req.ResponseFormat != nil {
		body.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchema{
				Name:   req.ResponseFormat.Name,
				Strict: true,
				Schema: req.ResponseFormat.Schema,
			},
		}
		body.Provider = &provider{RequireParameters: true}
	}
	if req.Reasoning != nil {
		body.Reasoning = req.Reasoning
	}
	return body
}

func (c *OpenRouter) Chat(ctx context.Context, req Request) (Response, error) {
	return c.call(ctx, req, nil)
}

func (c *OpenRouter) Stream(ctx context.Context, req Request, onDelta func(string) error) (Response, error) {
	if onDelta == nil {
		onDelta = func(string) error { return nil }
	}
	req.Stream = true
	return c.call(ctx, req, onDelta)
}

// call runs the retry loop: one attempt, then up to MaxRetries retries on
// retryable HTTP errors, on the same model (no fallback). OnRetry is invoked
// before each retry.
func (c *OpenRouter) call(ctx context.Context, req Request, onDelta func(string) error) (Response, error) {
	maxRetries := req.MaxRetries
	if maxRetries < 0 {
		maxRetries = 0
	}
	for attempt := 1; ; attempt++ {
		resp, err := c.attempt(ctx, req, onDelta)
		if err == nil {
			return resp, nil
		}
		if !isRetryable(err) {
			return Response{}, err
		}
		if attempt > maxRetries {
			return Response{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if req.OnRetry != nil {
			req.OnRetry(attempt, err)
		}
	}
}

func (c *OpenRouter) attempt(ctx context.Context, req Request, onDelta func(string) error) (Response, error) {
	body, err := json.Marshal(c.buildRequest(req))
	if err != nil {
		return Response{}, err
	}

	timeout := req.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(callCtx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return Response{}, &RetryableError{Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return Response{}, &RetryableError{Err: fmt.Errorf("upstream status %d", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Response{}, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	if onDelta != nil {
		return parseStream(resp.Body, onDelta)
	}
	return parseChat(resp.Body)
}

func parseChat(r io.Reader) (Response, error) {
	var cr chatResponse
	if err := json.NewDecoder(r).Decode(&cr); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if len(cr.Choices) == 0 {
		return Response{}, errors.New("empty choices")
	}
	return Response{
		Content: cr.Choices[0].Message.Content,
		Usage: Usage{
			PromptTokens:     cr.Usage.PromptTokens,
			CompletionTokens: cr.Usage.CompletionTokens,
		},
	}, nil
}

func parseStream(r io.Reader, onDelta func(string) error) (Response, error) {
	var full strings.Builder
	var usage Usage
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = Usage{PromptTokens: chunk.Usage.PromptTokens, CompletionTokens: chunk.Usage.CompletionTokens}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				full.WriteString(choice.Delta.Content)
				if err := onDelta(choice.Delta.Content); err != nil {
					return Response{}, err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Response{}, &RetryableError{Err: err}
	}
	return Response{Content: full.String(), Usage: usage}, nil
}

func isRetryable(err error) bool {
	var re *RetryableError
	return errors.As(err, &re)
}
