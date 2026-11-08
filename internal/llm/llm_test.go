package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenRouterChatParsesResponse(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("Authorization = %q, want Bearer test-key", r.Header.Get("Authorization"))
		}
		gotBody = make([]byte, r.ContentLength)
		r.Body.Read(gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`)
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "test-key", nil)
	resp, err := c.Chat(context.Background(), Request{
		Model:     "m",
		Messages:  []Message{{Role: RoleUser, Content: "hi"}},
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("Content = %q, want hello", resp.Content)
	}
	if resp.Usage.PromptTokens != 10 || resp.Usage.CompletionTokens != 2 {
		t.Errorf("Usage = %+v, want {10 2}", resp.Usage)
	}

	var body chatRequest
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body.ResponseFormat != nil || body.Provider != nil {
		t.Errorf("ResponseFormat/Provider set for a text node: %+v %+v", body.ResponseFormat, body.Provider)
	}
	if body.Model != "m" || body.MaxTokens != 100 {
		t.Errorf("body = %+v, want model m, max_tokens 100", body)
	}
}

func TestOpenRouterJSONNodeSendsJSONSchemaAndRequireParameters(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody = make([]byte, r.ContentLength)
		r.Body.Read(gotBody)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}],"usage":{}}`)
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "k", nil)
	_, err := c.Chat(context.Background(), Request{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "x"}},
		ResponseFormat: &ResponseFormat{
			Name:   "topics",
			Schema: map[string]any{"type": "object"},
		},
		Reasoning: &ReasoningConfig{Effort: "low"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}

	var body chatRequest
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("unmarshal request body: %v", err)
	}
	if body.ResponseFormat == nil {
		t.Fatal("response_format is nil, want json_schema")
	}
	if body.ResponseFormat.Type != "json_schema" {
		t.Errorf("response_format.type = %q, want json_schema", body.ResponseFormat.Type)
	}
	if body.ResponseFormat.JSONSchema.Name != "topics" || !body.ResponseFormat.JSONSchema.Strict {
		t.Errorf("json_schema = %+v, want name topics, strict true", body.ResponseFormat.JSONSchema)
	}
	if body.Provider == nil || !body.Provider.RequireParameters {
		t.Errorf("provider = %+v, want require_parameters true", body.Provider)
	}
	if body.Reasoning == nil || body.Reasoning.Effort != "low" {
		t.Errorf("reasoning = %+v, want effort low", body.Reasoning)
	}
}

func TestOpenRouterRetriesThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{}}`)
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "k", nil)
	var retried int
	resp, err := c.Chat(context.Background(), Request{
		Model:      "m",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		MaxRetries: 1,
		OnRetry: func(failedAttempt int, err error) {
			retried = failedAttempt
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("Content = %q, want ok", resp.Content)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if retried != 1 {
		t.Errorf("OnRetry failedAttempt = %d, want 1", retried)
	}
}

func TestOpenRouterRetriesExhaustedReturnsErrUnavailable(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "k", nil)
	var retries int
	_, err := c.Chat(context.Background(), Request{
		Model:      "m",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		MaxRetries: 1,
		OnRetry:    func(int, error) { retries++ },
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2 (1 attempt + 1 retry)", calls.Load())
	}
	if retries != 1 {
		t.Errorf("retries = %d, want 1", retries)
	}
}

func TestOpenRouterNoRetryOnClientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "k", nil)
	var retries int
	_, err := c.Chat(context.Background(), Request{
		Model:      "m",
		Messages:   []Message{{Role: RoleUser, Content: "hi"}},
		MaxRetries: 1,
		OnRetry:    func(int, error) { retries++ },
	})
	if err == nil {
		t.Fatal("error = nil, want error")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Errorf("error = %v, want a plain (non-retryable) error", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (no retry on 4xx)", calls.Load())
	}
	if retries != 0 {
		t.Errorf("retries = %d, want 0", retries)
	}
}

func TestOpenRouterStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"lo\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewOpenRouter(srv.URL, "k", nil)
	var got strings.Builder
	resp, err := c.Stream(context.Background(), Request{
		Model:    "m",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}, func(delta string) error {
		got.WriteString(delta)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("Content = %q, want hello", resp.Content)
	}
	if got.String() != "hello" {
		t.Errorf("onDelta collected %q, want hello", got.String())
	}
	if resp.Usage.PromptTokens != 3 {
		t.Errorf("PromptTokens = %d, want 3", resp.Usage.PromptTokens)
	}
}

func TestNewFactory(t *testing.T) {
	fake, err := New("fake", "", "")
	if err != nil {
		t.Fatalf("New(fake): %v", err)
	}
	if _, ok := fake.(*Fake); !ok {
		t.Errorf("New(fake) type = %T, want *Fake", fake)
	}

	or, err := New("openrouter", "https://openrouter.ai/api/v1", "key")
	if err != nil {
		t.Fatalf("New(openrouter): %v", err)
	}
	if _, ok := or.(*OpenRouter); !ok {
		t.Errorf("New(openrouter) type = %T, want *OpenRouter", or)
	}

	if _, err := New("ollama", "", ""); err == nil {
		t.Error("New(ollama) error = nil, want error")
	}
}

func TestFakeDeterministicPerModel(t *testing.T) {
	f := NewFake(map[string]string{"a": "reply-a"})

	r1, err := f.Chat(context.Background(), Request{Model: "a", Messages: []Message{{Role: RoleUser, Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat(a): %v", err)
	}
	r2, err := f.Chat(context.Background(), Request{Model: "a", Messages: []Message{{Role: RoleUser, Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat(a) again: %v", err)
	}
	if r1.Content != "reply-a" || r2.Content != "reply-a" {
		t.Errorf("Content = %q/%q, want reply-a both", r1.Content, r2.Content)
	}
	if r1.Usage != r2.Usage {
		t.Errorf("Usage = %+v vs %+v, want deterministic", r1.Usage, r2.Usage)
	}

	def, err := f.Chat(context.Background(), Request{Model: "unknown", Messages: []Message{{Role: RoleUser, Content: "q"}}})
	if err != nil {
		t.Fatalf("Chat(unknown): %v", err)
	}
	if def.Content != "fake reply for unknown" {
		t.Errorf("default Content = %q, want \"fake reply for unknown\"", def.Content)
	}
}

func TestFakeJSONDefaultIsValid(t *testing.T) {
	f := NewFake(nil)
	resp, err := f.Chat(context.Background(), Request{
		Model:          "judge",
		Messages:       []Message{{Role: RoleUser, Content: "x"}},
		ResponseFormat: &ResponseFormat{Name: "verdict", Schema: map[string]any{"type": "object"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(resp.Content), &out); err != nil {
		t.Errorf("fake JSON default %q is not valid JSON: %v", resp.Content, err)
	}
}

func TestFakeStreamEmitsFullContent(t *testing.T) {
	f := NewFake(map[string]string{"interviewer": "welcome!"})
	var got strings.Builder
	resp, err := f.Stream(context.Background(), Request{Model: "interviewer", Messages: []Message{{Role: RoleUser, Content: "hi"}}}, func(d string) error {
		got.WriteString(d)
		return nil
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if resp.Content != "welcome!" || got.String() != "welcome!" {
		t.Errorf("Content=%q onDelta=%q, want welcome! both", resp.Content, got.String())
	}
}
