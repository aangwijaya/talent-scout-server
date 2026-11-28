//go:build live

package graph

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/store"
)

// TestLiveFluencySummary is the human-run gate for the fluency node. It uses
// fixed code-computed metrics and two short transcripts, and checks that the
// model returns a non-empty advisory summary with no hiring recommendation and
// no score. It skips unless OPENROUTER_API_KEY is set in the process environment.
func TestLiveFluencySummary(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY is not set")
	}
	baseURL := os.Getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = "https://openrouter.ai/api/v1"
	}
	models, err := config.LoadModels(filepath.Join("..", "..", "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	node, err := models.Node("fluency")
	if err != nil {
		t.Fatalf("fluency model: %v", err)
	}

	messages := []domain.Message{
		{
			ID: "m1", Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer,
			Content: "I rebuilt the dashboard in SvelteKit and measured the first load with Lighthouse.",
			Entry:   &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken},
		},
		{
			ID: "m2", Role: domain.MessageRoleApplicant, Kind: domain.MessageKindAnswer,
			Content: "I split the page by ownership and kept server data in a loader.",
			Entry:   &domain.MessageEntry{Mode: domain.MessageEntryModeSpoken},
		},
	}
	id1, id2 := messages[0].ID, messages[1].ID
	uploads := []store.AudioUpload{{ID: "a1", MessageID: &id1}, {ID: "a2", MessageID: &id2}}
	metrics := domain.Fluency{
		WordsPerMinute:       128.5,
		PauseRatio:           0.12,
		FillerRate:           0.04,
		TranscriptConfidence: 0.93,
		Languages:            []string{"en"},
		SpokenAnswers:        2,
		TypedAnswers:         0,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := llm.NewOpenRouter(baseURL, apiKey, nil)
	resp, err := client.Chat(ctx, llm.Request{
		Node:           "fluency",
		Model:          node.Model,
		Messages:       fluencyMessages(metrics, messages, uploads),
		Temperature:    models.Temperature("fluency"),
		MaxTokens:      node.MaxTokens,
		ResponseFormat: fluencyResponseFormat(),
		Timeout:        models.Timeout(),
		MaxRetries:     models.MaxRetries(),
	})
	if err != nil {
		t.Fatalf("fluency call: %v", err)
	}
	summary, err := parseFluencySummary(resp.Content)
	if err != nil {
		t.Fatalf("parse summary %q: %v", resp.Content, err)
	}
	if len(summary) > 600 {
		t.Errorf("summary length = %d, want a short note", len(summary))
	}
	lower := strings.ToLower(summary)
	for _, banned := range []string{"recommend", "hire", "reject", "shortlist", "score"} {
		if strings.Contains(lower, banned) {
			t.Errorf("summary makes a hiring/score claim (%q): %q", banned, summary)
		}
	}
	t.Logf("fluency summary: %q", summary)
	t.Logf("fluency usage: prompt=%d completion=%d tokens (liveGateFloors[fluency]=2000)", resp.Usage.PromptTokens, resp.Usage.CompletionTokens)
}
