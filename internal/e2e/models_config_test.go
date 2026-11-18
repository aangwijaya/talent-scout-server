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
