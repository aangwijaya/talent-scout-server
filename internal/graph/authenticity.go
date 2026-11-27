package graph

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/llm"
	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

// AuthenticityOutput is the authenticity node's validated JSON reply.
type AuthenticityOutput struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

func parseAuthenticityOutput(content string) (AuthenticityOutput, error) {
	var output AuthenticityOutput
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &output); err != nil {
		return AuthenticityOutput{}, fmt.Errorf("parse authenticity JSON: %w", err)
	}
	switch domain.AuthenticityLevel(output.Level) {
	case domain.AuthenticityLevelNone, domain.AuthenticityLevelPossible, domain.AuthenticityLevelLikely:
	default:
		return AuthenticityOutput{}, fmt.Errorf("invalid authenticity level %q", output.Level)
	}
	reasons := make([]string, 0, len(output.Reasons))
	for _, reason := range output.Reasons {
		if trimmed := strings.TrimSpace(reason); trimmed != "" {
			reasons = append(reasons, trimmed)
		}
	}
	output.Reasons = reasons
	return output, nil
}

// authenticityUser renders each applicant answer with its client entry signals
// as untrusted data. Paste ratio and typing speed are computed here so the
// model sees a consistent signal rather than raw counters alone.
func authenticityUser(messages []domain.Message) string {
	var b strings.Builder
	b.WriteString("Applicant answers and their client entry signals (untrusted data):\n")
	position := 0
	for _, message := range messages {
		if message.Role != domain.MessageRoleApplicant {
			continue
		}
		position++
		fmt.Fprintf(&b, "\nAnswer %d:\n", position)
		b.WriteString(prompts.WrapUntrusted("answer", message.Content))
		b.WriteString("\nSignals:\n")
		if message.Entry == nil {
			b.WriteString(prompts.WrapUntrusted("entry_signals", "no entry signals"))
			b.WriteString("\n")
			continue
		}
		total := message.Entry.PastedChars + message.Entry.TypedChars
		if message.Entry.Mode == domain.MessageEntryModeSpoken {
			fmt.Fprintf(&b, "mode: spoken\nspeaking_ms: %d\nstart_latency_ms: %d\nrerecord_count: %d",
				message.Entry.SpeakingMS, message.Entry.StartLatencyMS, message.Entry.RerecordCount)
			b.WriteString("\n")
			continue
		}
		mode := message.Entry.Mode
		if mode == "" {
			mode = domain.MessageEntryModeTyped
		}
		ratio := 0.0
		if total > 0 {
			ratio = float64(message.Entry.PastedChars) / float64(total)
		}
		charsPerSecond := 0.0
		if message.Entry.DurationMS > 0 {
			charsPerSecond = float64(message.Entry.TypedChars) / (float64(message.Entry.DurationMS) / 1000)
		}
		signals := fmt.Sprintf(
			"mode: %s\npasted_chars: %d\ntyped_chars: %d\nduration_ms: %d\ntotal_chars: %d\npaste_ratio: %.2f\nchars_per_second: %.2f",
			mode, message.Entry.PastedChars, message.Entry.TypedChars, message.Entry.DurationMS, total,
			math.Round(ratio*100)/100, math.Round(charsPerSecond*100)/100)
		if message.Entry.FallbackReason != "" {
			signals += fmt.Sprintf("\nfallback_reason: %s", message.Entry.FallbackReason)
		}
		b.WriteString(prompts.WrapUntrusted("entry_signals", signals))
		b.WriteString("\n")
	}
	b.WriteString(`
Return only JSON matching this shape:
{"level":"none"|"possible"|"likely","reasons":["..."]}
Each reason must be a short, factual sentence a recruiter can read. This assessment is advisory: never reject, never change a score, and never claim certainty.`)
	return b.String()
}

func authenticityMessages(messages []domain.Message) []llm.Message {
	out, _ := prompts.Messages(prompts.NodeAuthenticity, authenticityUser(messages))
	return out
}

func authenticityResponseFormat() *llm.ResponseFormat {
	property := func() map[string]any { return map[string]any{"type": "string"} }
	return &llm.ResponseFormat{
		Name: "authenticity_assessment",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"level": map[string]any{
					"type": "string",
					"enum": []string{
						string(domain.AuthenticityLevelNone),
						string(domain.AuthenticityLevelPossible),
						string(domain.AuthenticityLevelLikely),
					},
				},
				"reasons": map[string]any{
					"type":  "array",
					"items": property(),
				},
			},
			"required":             []string{"level", "reasons"},
			"additionalProperties": false,
		},
	}
}

// runAuthenticity runs the advisory authenticity node. Any failure is safe: the
// runner's node.failed is left in node_events, no assessment is stored, and the
// scoring path still completes the run.
func (m *SessionManager) runAuthenticity(ctx context.Context, sess domain.Session, st *liveSession, messages []domain.Message) {
	st.executed["authenticity"] = true
	// Clear an earlier assessment first, so a failed check on a rescore leaves null.
	if err := m.store.SetApplicationAuthenticity(ctx, sess.ApplicationID, nil); err != nil {
		m.logger.Error("clear authenticity failed", "session_id", sess.ID, "error", err)
	}
	var output AuthenticityOutput
	var parseErr error
	_, _, err := st.runner.runLLMNodeWithSummary(ctx, "authenticity", 1, llm.Request{
		Messages:       authenticityMessages(messages),
		ResponseFormat: authenticityResponseFormat(),
	}, "applicant answers and entry signals", nil, func(resp llm.Response) string {
		output, parseErr = parseAuthenticityOutput(resp.Content)
		if parseErr != nil {
			return "invalid"
		}
		return "level=" + output.Level
	})
	if err != nil {
		m.logger.Error("authenticity step failed", "session_id", sess.ID, "error", err)
		return
	}
	if parseErr != nil {
		m.logger.Error("authenticity output invalid", "session_id", sess.ID, "error", parseErr)
		return
	}
	auth := domain.Authenticity{
		Level:      domain.AuthenticityLevel(output.Level),
		Reasons:    output.Reasons,
		AssessedAt: time.Now().UTC().Truncate(time.Microsecond),
	}
	if err := m.store.SetApplicationAuthenticity(ctx, sess.ApplicationID, &auth); err != nil {
		m.logger.Error("store authenticity failed", "session_id", sess.ID, "error", err)
	}
}
