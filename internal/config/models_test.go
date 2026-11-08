package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeModels(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "models.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write models.yaml: %v", err)
	}
	return path
}

const validModels = `
default:
  temperature: 0.3
  timeout_seconds: 30
  max_retries_on_http_error: 1
nodes:
  planner:     { model: deepseek/deepseek-v4.1-flash, temperature: 0.2, max_tokens: 800 }
  interviewer: { model: z-ai/glm-5.3-flash,           temperature: 0.6, max_tokens: 300, stream: true }
  judge:       { model: deepseek/deepseek-v4.1-flash, temperature: 0.0, max_tokens: 200 }
  scorer:      { model: deepseek/deepseek-v4.1-flash, temperature: 0.0, max_tokens: 1200 }
prices:
  deepseek/deepseek-v4.1-flash: { in: 0.045, out: 0.5 }
  z-ai/glm-5.3-flash:           { in: 0.15,  out: 0.50 }
`

func TestLoadModels(t *testing.T) {
	mc, err := LoadModels(writeModels(t, validModels))
	if err != nil {
		t.Fatalf("LoadModels: %v", err)
	}

	planner, err := mc.Node("planner")
	if err != nil {
		t.Fatalf("Node(planner): %v", err)
	}
	if planner.Model != "deepseek/deepseek-v4.1-flash" {
		t.Errorf("planner.Model = %q, want deepseek/deepseek-v4.1-flash", planner.Model)
	}
	if planner.MaxTokens != 800 {
		t.Errorf("planner.MaxTokens = %d, want 800", planner.MaxTokens)
	}

	interviewer, err := mc.Node("interviewer")
	if err != nil {
		t.Fatalf("Node(interviewer): %v", err)
	}
	if !interviewer.Stream {
		t.Errorf("interviewer.Stream = false, want true")
	}
	if interviewer.Model != "z-ai/glm-5.3-flash" {
		t.Errorf("interviewer.Model = %q, want z-ai/glm-5.3-flash", interviewer.Model)
	}

	if got := mc.Temperature("planner"); got != 0.2 {
		t.Errorf("Temperature(planner) = %v, want 0.2", got)
	}
	if got := mc.Temperature("scorer"); got != 0.0 {
		t.Errorf("Temperature(scorer) = %v, want 0.0", got)
	}
	if got := mc.Timeout(); got != 30*time.Second {
		t.Errorf("Timeout() = %v, want 30s", got)
	}
	if got := mc.MaxRetries(); got != 1 {
		t.Errorf("MaxRetries() = %d, want 1", got)
	}

	price, ok := mc.PriceFor("z-ai/glm-5.3-flash")
	if !ok || price.In != 0.15 || price.Out != 0.50 {
		t.Errorf("PriceFor(z-ai/glm-5.3-flash) = %+v, %v; want {0.15 0.50}, true", price, ok)
	}

	if got := mc.CostUSD("deepseek/deepseek-v4.1-flash", 1000, 2000); got != (1000*0.045+2000*0.5)/1_000_000 {
		t.Errorf("CostUSD = %v, want %v", got, (1000*0.045+2000*0.5)/1_000_000)
	}
}

func TestLoadModelsParsesReasoning(t *testing.T) {
	mc, err := LoadModels(writeModels(t, `
default: { temperature: 0.3, timeout_seconds: 30, max_retries_on_http_error: 1 }
nodes:
  interviewer: { model: m, max_tokens: 300, stream: true, reasoning: { effort: low } }
prices:
  m: { in: 0.1, out: 0.2 }
`))
	if err != nil {
		t.Fatalf("LoadModels: %v", err)
	}
	node, err := mc.Node("interviewer")
	if err != nil {
		t.Fatalf("Node: %v", err)
	}
	if node.Reasoning == nil || node.Reasoning.Effort != "low" {
		t.Errorf("Reasoning = %+v, want effort low", node.Reasoning)
	}
}

func TestLoadModelsRejectsMissing(t *testing.T) {
	if _, err := LoadModels(filepath.Join(t.TempDir(), "nope.yaml")); err == nil {
		t.Fatal("LoadModels(missing file) error = nil, want error")
	}
}

func TestLoadModelsRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "node without price", body: `
default: { temperature: 0.3, timeout_seconds: 30, max_retries_on_http_error: 1 }
nodes:
  planner: { model: deepseek/deepseek-v4.1-flash, temperature: 0.2, max_tokens: 800 }
prices: {}
`},
		{name: "empty model", body: `
default: { temperature: 0.3, timeout_seconds: 30, max_retries_on_http_error: 1 }
nodes:
  planner: { model: "", max_tokens: 800 }
prices: {}
`},
		{name: "negative retries", body: `
default: { temperature: 0.3, timeout_seconds: 30, max_retries_on_http_error: -1 }
nodes: {}
prices: {}
`},
		{name: "non-positive price", body: `
default: { temperature: 0.3, timeout_seconds: 30, max_retries_on_http_error: 1 }
nodes:
  planner: { model: m, max_tokens: 800 }
prices:
  m: { in: 0, out: 0.5 }
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadModels(writeModels(t, tc.body)); err == nil {
				t.Fatalf("LoadModels() error = nil, want error")
			}
		})
	}
}
