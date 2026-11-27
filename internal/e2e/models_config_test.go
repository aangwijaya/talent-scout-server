package e2e

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
)

func TestInterviewerModelBudgetMatchesRecordedLatency(t *testing.T) {
	models, err := config.LoadModels(filepath.Join(repoRoot(), "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}

	if got := models.Timeout(); got < 60*time.Second {
		t.Errorf("shared timeout = %s, want >= 60s for the recorded 26.2s interviewer reply", got)
	}

	interviewer, err := models.Node("interviewer")
	if err != nil {
		t.Fatalf("interviewer model: %v", err)
	}
	if interviewer.MaxTokens < 4000 {
		t.Errorf("interviewer max_tokens = %d, want >= 4000", interviewer.MaxTokens)
	}
	if interviewer.Reasoning == nil || strings.ToLower(interviewer.Reasoning.Effort) != "low" {
		t.Errorf("interviewer reasoning = %+v, want effort low", interviewer.Reasoning)
	}
}

// The 2026-11-20 server#79 live gate failed two of three sessions on
// finish_reason=length: judge at 1200 tokens and scorer at 4000 (3779 prompt
// tokens with the richer seed job). Scorer produced 4000 tokens in under ~17 s,
// so 8000 still fits the 60 s timeout. The server#81 gate then truncated the
// planner at 3000 (it normally uses 1.6k-2k with the job context and sources).
func TestTranscriberModelAndPriceComeFromConfig(t *testing.T) {
	models, err := config.LoadModels(filepath.Join(repoRoot(), "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	if strings.TrimSpace(models.Transcriber.Model) == "" {
		t.Error("transcriber.model is empty")
	}
	if models.Transcriber.USDPerHour <= 0 {
		t.Errorf("transcriber.usd_per_hour = %v, want positive", models.Transcriber.USDPerHour)
	}
	if models.Transcriber.MinBilledSeconds < 0 {
		t.Errorf("transcriber.min_billed_seconds = %d, want >= 0", models.Transcriber.MinBilledSeconds)
	}
}

func TestJudgeAndScorerHaveRoomForReasoning(t *testing.T) {
	models, err := config.LoadModels(filepath.Join(repoRoot(), "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	for node, min := range map[string]int{"planner": 6000, "judge": 2400, "scorer": 8000} {
		m, err := models.Node(node)
		if err != nil {
			t.Fatalf("%s model: %v", node, err)
		}
		if m.MaxTokens < min {
			t.Errorf("%s max_tokens = %d, want >= %d", node, m.MaxTokens, min)
		}
	}
}
