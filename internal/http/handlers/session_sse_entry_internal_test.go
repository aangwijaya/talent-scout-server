package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
)

// The applicant's session SSE must never carry the paste/typing signals of
// their own answers; only the recruiter graph sees them (server#81).
func TestSessionEventForApplicantDropsEntry(t *testing.T) {
	payload, err := json.Marshal(graph.MessageCompleted{Message: domain.Message{
		ID:      "m1",
		Role:    domain.MessageRoleApplicant,
		Content: "I moved the state into a store.",
		Entry:   &domain.MessageEntry{PastedChars: 5, TypedChars: 1, DurationMS: 10},
	}})
	if err != nil {
		t.Fatal(err)
	}
	event := sessionEventForApplicant(domain.NodeEvent{Type: graph.EventMessageCompleted, Payload: payload})
	body := string(event.Payload)
	for _, leak := range []string{"pasted_chars", "typed_chars", `"entry"`} {
		if strings.Contains(body, leak) {
			t.Errorf("applicant SSE payload carries %s: %s", leak, body)
		}
	}
	if !strings.Contains(body, "I moved the state into a store.") {
		t.Errorf("applicant SSE payload lost the message content: %s", body)
	}
}
