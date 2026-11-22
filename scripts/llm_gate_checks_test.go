package scripts

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
)

// liveGateFloors ratchets config/models.yaml to the caps that survived the
// 2026-11-20 live gates. See docs/LESSONS.md #79 (judge/scorer) and #81
// (planner). Reasoning tokens count toward max_tokens, so a cap sized for the
// visible output truncates as soon as the prompt grows.
//
// Every LLM node in models.yaml must appear here. That is deliberate: adding a
// node, or growing a prompt until it needs more reasoning room, must make
// someone size the cap from a live gate instead of inheriting a too-small
// default. Keep each floor at about 2x the largest completion observed live.
var liveGateFloors = map[string]int{
	"planner":          6000,
	"interviewer":      4000,
	"judge":            2400,
	"scorer":           8000,
	"authenticity":     2000,
	"rubric_suggester": 3000,
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Dir(filepath.Dir(file))
}

func TestModelsKeepLiveGateCaps(t *testing.T) {
	models, err := config.LoadModels(filepath.Join(repoRoot(t), "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}

	for node, floor := range liveGateFloors {
		cfg, ok := models.Nodes[node]
		if !ok {
			t.Errorf("liveGateFloors names %q but config/models.yaml has no such node", node)
			continue
		}
		if cfg.MaxTokens < floor {
			t.Errorf("%s max_tokens = %d, want >= %d (docs/LESSONS.md #79/#81)", node, cfg.MaxTokens, floor)
		}
	}
	for node := range models.Nodes {
		if _, ok := liveGateFloors[node]; !ok {
			t.Errorf("config/models.yaml node %q has no live-gate floor; size it from a live gate and add it to liveGateFloors", node)
		}
	}

	if got := models.Timeout(); got < 60*time.Second {
		t.Errorf("shared timeout = %s, want >= 60s for the recorded 26.2s interviewer reply", got)
	}

	interviewer, ok := models.Nodes["interviewer"]
	if !ok {
		t.Fatal("config/models.yaml has no interviewer node")
	}
	if !interviewer.Stream {
		t.Error("interviewer must stream so the first-? guard can stop the reply (docs/LESSONS.md #63)")
	}
	if interviewer.Reasoning == nil || strings.ToLower(interviewer.Reasoning.Effort) != "low" {
		t.Errorf("interviewer reasoning = %+v, want effort low", interviewer.Reasoning)
	}
}
