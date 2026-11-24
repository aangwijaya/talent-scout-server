package e2e

import (
	"testing"

	"github.com/aangwijaya/talent-scout-server/internal/prompts"
)

var promptNodes = []prompts.Node{
	prompts.NodePlanner,
	prompts.NodeInterviewer,
	prompts.NodeJudge,
	prompts.NodeScorer,
	prompts.NodeAuthenticity,
	prompts.NodeRubricSuggester,
}

func requestWithSystem(system string) llmRequestInfo {
	return llmRequestInfo{
		MaxTokens: 100,
		Messages: []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}{{Role: "system", Content: system}},
	}
}

// The live call log distinguishes nodes by their system prompt, so every prompt
// in internal/prompts must map back to its node name.
func TestNodeFromRequestLabelsEverySystemPrompt(t *testing.T) {
	for _, node := range promptNodes {
		system, ok := prompts.System(node)
		if !ok {
			t.Fatalf("prompts.System(%q) has no prompt", node)
		}
		if got := nodeFromRequest(requestWithSystem(system)); got != string(node) {
			t.Errorf("nodeFromRequest(%s system prompt) = %q, want %q", node, got, node)
		}
	}
}

// reportLLMCalls checks truncation from truncationProblems, so every labeled
// node must get a check. The interviewer is the exception for finish_reason:
// its reply is stopped on purpose and the provider can still report length.
func TestTruncationProblemsCoverEveryLabeledNode(t *testing.T) {
	for _, node := range promptNodes {
		system, ok := prompts.System(node)
		if !ok {
			t.Fatalf("prompts.System(%q) has no prompt", node)
		}
		req := requestWithSystem(system)
		label := nodeFromRequest(req)
		if label == "unknown" {
			t.Fatalf("%s system prompt is not labeled", node)
		}

		overBudget := llmResponseInfo{CompletionTokens: req.MaxTokens + 1}
		if problems := truncationProblems(label, req, overBudget); len(problems) == 0 {
			t.Errorf("truncationProblems(%q) missed completion_tokens over max_tokens", label)
		}

		length := llmResponseInfo{FinishReason: "length"}
		problems := truncationProblems(label, req, length)
		if label == "interviewer" {
			if len(problems) != 0 {
				t.Errorf("truncationProblems(interviewer) flagged finish_reason=length on a guard-stopped reply: %v", problems)
			}
			continue
		}
		if len(problems) == 0 {
			t.Errorf("truncationProblems(%q) missed finish_reason=length", label)
		}
	}
}
