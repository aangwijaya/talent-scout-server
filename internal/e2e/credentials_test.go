package e2e

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLiveCredentialsFromEnvNeverReadsDotEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("OPENROUTER_API_KEY=from-dot-env\n"), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(previous) }()

	key, baseURL, ok := liveCredentialsFrom(func(string) string { return "" })
	if ok {
		t.Fatalf("liveCredentialsFrom with an empty env returned credentials (%q, %q)", key, baseURL)
	}
	if key != "" || baseURL != "" {
		t.Fatalf("credentials = (%q, %q), want empty", key, baseURL)
	}
}

func TestLiveCredentialsFromEnvDefaultsBaseURL(t *testing.T) {
	key, baseURL, ok := liveCredentialsFrom(func(name string) string {
		if name == "OPENROUTER_API_KEY" {
			return "test-key"
		}
		return ""
	})
	if !ok || key != "test-key" || baseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("got (%q, %q, %v), want (test-key, default base URL, true)", key, baseURL, ok)
	}
}
