package config

import (
	"math"
	"strings"
	"testing"
)

func TestTranscribeModeFollowsFakeLLMMode(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)
	t.Setenv("LLM_MODE", "fake")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with LLM_MODE=fake and no TRANSCRIBE_MODE/GROQ_API_KEY: %v", err)
	}
	if cfg.TranscribeMode != "fake" {
		t.Errorf("TranscribeMode = %q, want fake", cfg.TranscribeMode)
	}
	if cfg.GroqAPIKey != "" {
		t.Errorf("GroqAPIKey = %q, want empty", cfg.GroqAPIKey)
	}
}

func TestTranscribeModeGroqRequiresKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)
	t.Setenv("TRANSCRIBE_MODE", "groq")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want missing GROQ_API_KEY error")
	}
	if !strings.Contains(err.Error(), "GROQ_API_KEY") {
		t.Errorf("error = %v, want it to name GROQ_API_KEY", err)
	}
}

func TestExplicitTranscribeFakeNeedsNoKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)
	t.Setenv("LLM_MODE", "openrouter")
	t.Setenv("TRANSCRIBE_MODE", "fake")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with TRANSCRIBE_MODE=fake: %v", err)
	}
	if cfg.TranscribeMode != "fake" {
		t.Errorf("TranscribeMode = %q, want fake", cfg.TranscribeMode)
	}
}

func TestTranscribeModeGroqWithKey(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)
	t.Setenv("LLM_MODE", "fake")
	t.Setenv("TRANSCRIBE_MODE", "groq")
	t.Setenv("GROQ_API_KEY", "secret-groq-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.TranscribeMode != "groq" || cfg.GroqAPIKey != "secret-groq-key" {
		t.Errorf("transcribe config = %q/%q, want groq/secret-groq-key", cfg.TranscribeMode, cfg.GroqAPIKey)
	}
}

func TestTranscribeModeRejectsUnknownValue(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)
	t.Setenv("TRANSCRIBE_MODE", "whisper-local")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want invalid TRANSCRIBE_MODE error")
	}
}

func TestModelsConfigParsesTranscriberAndComputesCost(t *testing.T) {
	mc, err := LoadModels(writeModels(t, validModels+`
transcriber:
  model: whisper-large-v3-turbo
  usd_per_hour: 3.6
  min_billed_seconds: 10
`))
	if err != nil {
		t.Fatalf("LoadModels: %v", err)
	}
	if mc.Transcriber.Model != "whisper-large-v3-turbo" || mc.Transcriber.USDPerHour != 3.6 || mc.Transcriber.MinBilledSeconds != 10 {
		t.Errorf("transcriber = %+v, want model/price/minimum", mc.Transcriber)
	}
	for _, tc := range []struct {
		name       string
		durationMS int
		want       float64
	}{
		{name: "below the minimum is billed at the minimum", durationMS: 5000, want: 10.0 / 3600 * 3.6},
		{name: "above the minimum bills the actual seconds", durationMS: 18000, want: 18.0 / 3600 * 3.6},
	} {
		if got := mc.TranscriptionCostUSD(tc.durationMS); math.Abs(got-tc.want) > 1e-12 {
			t.Errorf("%s: cost = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestModelsConfigRejectsInvalidTranscriber(t *testing.T) {
	_, err := LoadModels(writeModels(t, validModels+`
transcriber:
  model: whisper-large-v3-turbo
  usd_per_hour: 0
  min_billed_seconds: 10
`))
	if err == nil {
		t.Fatal("LoadModels() error = nil, want invalid transcriber price error")
	}
}
