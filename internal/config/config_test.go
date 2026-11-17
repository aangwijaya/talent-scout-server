package config

import "testing"

const validAuthTokenSecret = "0123456789abcdef0123456789abcdef"

var allEnv = []string{
	"PORT", "APP_ENV", "DATABASE_URL", "SUPABASE_URL",
	"SUPABASE_SERVICE_ROLE_KEY", "SUPABASE_STORAGE_BUCKET", "PUBLIC_APP_URL",
	"FRONTEND_ORIGIN", "BFF_SHARED_SECRET", "RECRUITER_PASSWORD", "AUTH_TOKEN_SECRET",
	"LLM_MODE", "LLM_BASE_URL", "OPENROUTER_API_KEY", "MODELS_CONFIG_PATH",
	"EMAIL_MODE", "GMAIL_CLIENT_ID", "GMAIL_CLIENT_SECRET", "GMAIL_REFRESH_TOKEN",
	"EMAIL_FROM", "SESSION_TTL_HOURS", "SESSION_MAX_MINUTES", "SESSION_MAX_QUESTIONS",
	"LOGIN_MAX_ATTEMPTS", "LOGIN_WINDOW_MINUTES", "MESSAGE_MAX_PER_WINDOW",
	"MESSAGE_WINDOW_MINUTES", "TRUSTED_PROXY",
	"TEST_DATABASE_URL",
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, key := range allEnv {
		t.Setenv(key, "")
	}
}

func TestLoadDefaults(t *testing.T) {
	clearEnv(t)
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", cfg.Version)
	}
	if cfg.LLMMode != "openrouter" {
		t.Errorf("LLMMode = %q, want openrouter", cfg.LLMMode)
	}
	if cfg.EmailMode != "log" {
		t.Errorf("EmailMode = %q, want log", cfg.EmailMode)
	}
	if cfg.SessionTTLHours != 168 || cfg.SessionMaxMinutes != 15 || cfg.SessionMaxQuestions != 15 {
		t.Errorf("session defaults = %d/%d/%d, want 168/15/15", cfg.SessionTTLHours, cfg.SessionMaxMinutes, cfg.SessionMaxQuestions)
	}
	if len(cfg.FrontendOrigins) != 0 {
		t.Errorf("FrontendOrigins = %v, want empty", cfg.FrontendOrigins)
	}
	if cfg.LoginMaxAttempts != 5 || cfg.LoginWindowMinutes != 15 {
		t.Errorf("login rate defaults = %d/%d, want 5/15", cfg.LoginMaxAttempts, cfg.LoginWindowMinutes)
	}
	if cfg.MessageMaxPerWindow != 20 || cfg.MessageWindowMinutes != 10 {
		t.Errorf("message rate defaults = %d/%d, want 20/10", cfg.MessageMaxPerWindow, cfg.MessageWindowMinutes)
	}
	if cfg.TrustedProxy {
		t.Error("TrustedProxy = true, want false")
	}
}

func TestLoadFromEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("PORT", "9090")
	t.Setenv("EMAIL_MODE", "gmail")
	t.Setenv("LLM_MODE", "fake")
	t.Setenv("FRONTEND_ORIGIN", " https://a.example , ,http://localhost:5173 ")
	t.Setenv("SESSION_TTL_HOURS", "24")
	t.Setenv("LOGIN_MAX_ATTEMPTS", "3")
	t.Setenv("LOGIN_WINDOW_MINUTES", "30")
	t.Setenv("MESSAGE_MAX_PER_WINDOW", "7")
	t.Setenv("MESSAGE_WINDOW_MINUTES", "5")
	t.Setenv("TRUSTED_PROXY", "true")
	t.Setenv("AUTH_TOKEN_SECRET", validAuthTokenSecret)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Port != "9090" {
		t.Errorf("Port = %q, want 9090", cfg.Port)
	}
	if cfg.Version != "0.1.0" {
		t.Errorf("Version = %q, want 0.1.0", cfg.Version)
	}
	if cfg.EmailMode != "gmail" || cfg.LLMMode != "fake" {
		t.Errorf("EmailMode/LLMMode = %q/%q", cfg.EmailMode, cfg.LLMMode)
	}
	if cfg.SessionTTLHours != 24 {
		t.Errorf("SessionTTLHours = %d, want 24", cfg.SessionTTLHours)
	}
	if len(cfg.FrontendOrigins) != 2 || cfg.FrontendOrigins[0] != "https://a.example" || cfg.FrontendOrigins[1] != "http://localhost:5173" {
		t.Errorf("FrontendOrigins = %v", cfg.FrontendOrigins)
	}
	if cfg.AuthTokenSecret != validAuthTokenSecret {
		t.Errorf("AuthTokenSecret = %q, want the configured value", cfg.AuthTokenSecret)
	}
	if cfg.LoginMaxAttempts != 3 || cfg.LoginWindowMinutes != 30 {
		t.Errorf("login rate = %d/%d, want 3/30", cfg.LoginMaxAttempts, cfg.LoginWindowMinutes)
	}
	if cfg.MessageMaxPerWindow != 7 || cfg.MessageWindowMinutes != 5 {
		t.Errorf("message rate = %d/%d, want 7/5", cfg.MessageMaxPerWindow, cfg.MessageWindowMinutes)
	}
	if !cfg.TrustedProxy {
		t.Error("TrustedProxy = false, want true")
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "email mode", key: "EMAIL_MODE", val: "smtp"},
		{name: "llm mode", key: "LLM_MODE", val: "ollama"},
		{name: "wildcard origin", key: "FRONTEND_ORIGIN", val: "*"},
		{name: "session ttl", key: "SESSION_TTL_HOURS", val: "week"},
		{name: "empty auth secret", key: "AUTH_TOKEN_SECRET", val: ""},
		{name: "short auth secret", key: "AUTH_TOKEN_SECRET", val: "too-short"},
		{name: "zero login attempts", key: "LOGIN_MAX_ATTEMPTS", val: "0"},
		{name: "non-integer login window", key: "LOGIN_WINDOW_MINUTES", val: "soon"},
		{name: "zero message limit", key: "MESSAGE_MAX_PER_WINDOW", val: "0"},
		{name: "zero message window", key: "MESSAGE_WINDOW_MINUTES", val: "0"},
		{name: "invalid trusted proxy", key: "TRUSTED_PROXY", val: "maybe"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearEnv(t)
			t.Setenv(tc.key, tc.val)
			if _, err := Load(); err == nil {
				t.Fatalf("Load() error = nil, want error for %s=%q", tc.key, tc.val)
			}
		})
	}
}
