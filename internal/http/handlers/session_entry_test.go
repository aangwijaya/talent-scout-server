package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// entry creates a valid client-side signal object for the test payloads.
func entry(pasted, typed, duration int) map[string]int {
	return map[string]int{"pasted_chars": pasted, "typed_chars": typed, "duration_ms": duration}
}

func startChat(t *testing.T) *chatFixture {
	t.Helper()
	f := newChatFixture(t, false)
	rec := httptest.NewRecorder()
	f.handler.Start(rec, chatRequest(http.MethodPost, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	waitManager(t, f.manager, f.session.ID)
	return f
}

func TestSendMessageStoresEntryAndHidesItFromApplicant(t *testing.T) {
	f := startChat(t)

	payload, _ := json.Marshal(map[string]any{
		"content": "I optimized rendering.",
		"entry":   entry(120, 80, 4000),
	})
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"entry"`) {
		t.Errorf("send-message response exposes entry:\n%s", rec.Body.String())
	}

	messages, err := f.store.ListMessagesBySession(context.Background(), f.session.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	var answer *domain.Message
	for i := range messages {
		if messages[i].Role == domain.MessageRoleApplicant {
			answer = &messages[i]
		}
	}
	if answer == nil {
		t.Fatal("no applicant message stored")
	}
	if answer.Entry == nil {
		t.Fatal("stored applicant message has no entry")
	}
	want := domain.MessageEntry{PastedChars: 120, TypedChars: 80, DurationMS: 4000, Mode: domain.MessageEntryModeTyped}
	if *answer.Entry != want {
		t.Errorf("stored entry = %+v, want %+v", *answer.Entry, want)
	}

	// The applicant's own transcript must not expose the client signals.
	rec = httptest.NewRecorder()
	f.handler.Messages(rec, chatRequest(http.MethodGet, f.raw, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("messages status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"entry"`) {
		t.Errorf("applicant transcript exposes entry:\n%s", rec.Body.String())
	}
}

func TestSendMessageWithoutEntryIsAccepted(t *testing.T) {
	f := startChat(t)

	payload, _ := json.Marshal(map[string]string{"content": "I optimized rendering."})
	rec := httptest.NewRecorder()
	f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body: %s)", rec.Code, rec.Body.String())
	}

	messages, err := f.store.ListMessagesBySession(context.Background(), f.session.ID, false)
	if err != nil {
		t.Fatalf("ListMessagesBySession: %v", err)
	}
	for _, message := range messages {
		if message.Role == domain.MessageRoleApplicant && message.Entry != nil {
			t.Errorf("message without entry stored entry %+v", *message.Entry)
		}
	}
}

func TestSendMessageRejectsMalformedEntry(t *testing.T) {
	f := newChatFixture(t, true)

	for name, body := range map[string]map[string]any{
		"negative pasted": {"content": "answer", "entry": entry(-1, 10, 1000)},
		"negative typed":  {"content": "answer", "entry": entry(0, -1, 1000)},
		"negative time":   {"content": "answer", "entry": entry(0, 10, -1)},
		"too many pasted": {"content": "answer", "entry": entry(10000001, 0, 1000)},
		"too many typed":  {"content": "answer", "entry": entry(0, 10000001, 1000)},
		"too much time":   {"content": "answer", "entry": entry(0, 10, 10000001)},
		"missing field":   {"content": "answer", "entry": map[string]int{"pasted_chars": 1, "typed_chars": 2}},
		"wrong type":      {"content": "answer", "entry": map[string]any{"pasted_chars": "many", "typed_chars": 2, "duration_ms": 1000}},
	} {
		t.Run(name, func(t *testing.T) {
			payload, _ := json.Marshal(body)
			rec := httptest.NewRecorder()
			f.handler.SendMessage(rec, chatRequest(http.MethodPost, f.raw, string(payload)))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
			}
			code, _, _ := decodeError(t, rec)
			if code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", code)
			}
		})
	}
}
