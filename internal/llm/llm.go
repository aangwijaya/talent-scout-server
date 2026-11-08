// Package llm is the OpenAI-compatible client used by graph nodes. All node
// access to models goes through the Client interface so tests can inject the
// deterministic Fake.
package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Role is a chat message role.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is a single chat turn.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// ResponseFormat requests structured JSON output via response_format
// json_schema. Strict mode is always enabled for JSON nodes.
type ResponseFormat struct {
	Name   string         `json:"name"`
	Schema map[string]any `json:"schema"`
}

// ReasoningConfig configures the model's reasoning effort (passed through as
// the `reasoning` request field for models that accept it).
type ReasoningConfig struct {
	Effort string `json:"effort,omitempty"`
}

// Request is one model call. Model, temperature, max_tokens, stream, timeout,
// and max_retries are normally filled by the runner from config/models.yaml;
// the caller supplies messages and, for JSON nodes, ResponseFormat.
type Request struct {
	Model          string
	Messages       []Message
	Temperature    float64
	MaxTokens      int
	Stream         bool
	ResponseFormat *ResponseFormat
	Reasoning      *ReasoningConfig
	Timeout        time.Duration
	MaxRetries     int
	// OnRetry is called before each retry with the failed attempt number
	// (1-based). The runner uses it to emit node.failed(will_retry=true) and a
	// fresh node.started for the next attempt.
	OnRetry func(failedAttempt int, err error)
}

// Usage is the token usage of one call.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
}

// Response is the result of one call.
type Response struct {
	Content string
	Usage   Usage
}

// ErrUnavailable is returned when a call fails after retries (or immediately
// for a non-retryable error). The graph translates it into an `error` event
// with code `llm_unavailable`.
var ErrUnavailable = errors.New("llm unavailable")

// RetryableError marks an HTTP/transport failure that may be retried on the
// same model.
type RetryableError struct {
	Err error
}

func (e *RetryableError) Error() string { return e.Err.Error() }
func (e *RetryableError) Unwrap() error { return e.Err }

// Client is the interface nodes call. Both OpenRouter and Fake implement it.
type Client interface {
	// Chat performs a single non-streaming completion.
	Chat(ctx context.Context, req Request) (Response, error)
	// Stream performs a streaming completion, calling onDelta for each text
	// chunk and returning the full content in the final Response.
	Stream(ctx context.Context, req Request, onDelta func(delta string) error) (Response, error)
}

// New builds the client for a given mode: "fake" (deterministic, no network)
// or "openrouter" (uses the given base URL and API key). An empty mode defaults
// to openrouter.
func New(mode, baseURL, apiKey string) (Client, error) {
	switch mode {
	case "fake":
		return NewFake(nil), nil
	case "openrouter", "":
		return NewOpenRouter(baseURL, apiKey, nil), nil
	default:
		return nil, fmt.Errorf("unknown LLM mode %q (want fake or openrouter)", mode)
	}
}
