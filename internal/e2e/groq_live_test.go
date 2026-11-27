//go:build live

package e2e

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/config"
	"github.com/aangwijaya/talent-scout-server/internal/transcribe"
)

// TestLiveGroqTranscription is the human-run gate for the Groq client. The
// agent never has GROQ_API_KEY. Supply a short spoken-English recording (WebM,
// OGG or MP4) and its duration; the test skips when either is missing.
func TestLiveGroqTranscription(t *testing.T) {
	apiKey := os.Getenv("GROQ_API_KEY")
	if apiKey == "" {
		t.Skip("GROQ_API_KEY is not set")
	}
	audioPath := os.Getenv("GROQ_LIVE_AUDIO")
	if audioPath == "" {
		t.Skip("GROQ_LIVE_AUDIO is not set")
	}
	expectedSeconds, err := strconv.ParseFloat(os.Getenv("GROQ_LIVE_AUDIO_SECONDS"), 64)
	if err != nil || expectedSeconds <= 0 {
		t.Skip("GROQ_LIVE_AUDIO_SECONDS must name the sample's duration in seconds")
	}

	models, err := config.LoadModels(filepath.Join(repoRoot(), "config", "models.yaml"))
	if err != nil {
		t.Fatalf("load models: %v", err)
	}
	audio, err := os.ReadFile(audioPath)
	if err != nil {
		t.Fatalf("read %s: %v", audioPath, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := transcribe.NewGroq("https://api.groq.com/openai/v1", apiKey, models.Transcriber.Model, nil)
	result, err := client.Transcribe(ctx, audio, contentTypeForPath(audioPath))
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if strings.TrimSpace(result.Transcript) == "" {
		t.Error("transcript is empty, want the spoken words")
	}
	if result.Language != "en" {
		t.Errorf("language = %q, want en", result.Language)
	}
	gotSeconds := float64(result.DurationMS) / 1000
	tolerance := math.Max(1, expectedSeconds*0.1)
	if math.Abs(gotSeconds-expectedSeconds) > tolerance {
		t.Errorf("duration = %.2fs, want %.2fs ± %.2fs", gotSeconds, expectedSeconds, tolerance)
	}
	t.Logf("transcript=%q language=%s duration=%.2fs", result.Transcript, result.Language, gotSeconds)
}

func contentTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".mp4", ".m4a":
		return "audio/mp4"
	default:
		return "audio/webm"
	}
}
