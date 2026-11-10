package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/aangwijaya/talent-scout-server/internal/auth"
)

const (
	defaultPort           = "8080"
	defaultVersion        = "0.1.0"
	defaultPublicAppURL   = "https://talentscout.vercel.app"
	defaultLLMBaseURL     = "https://openrouter.ai/api/v1"
	defaultModelsPath     = "./config/models.yaml"
	defaultEmailFrom      = "Talent Scout <you@gmail.com>"
	defaultSessionTTL     = 168
	defaultSessionMinutes = 15
	defaultSessionMaxQues = 15
)

type Config struct {
	Port                   string
	AppEnv                 string
	Version                string
	DatabaseURL            string
	SupabaseURL            string
	SupabaseServiceRoleKey string
	SupabaseStorageBucket  string
	PublicAppURL           string
	FrontendOrigins        []string
	BFFSharedSecret        string
	RecruiterPassword      string
	AuthTokenSecret        string
	LLMMode                string
	LLMBaseURL             string
	OpenRouterAPIKey       string
	ModelsConfigPath       string
	EmailMode              string
	GmailClientID          string
	GmailClientSecret      string
	GmailRefreshToken      string
	EmailFrom              string
	SessionTTLHours        int
	SessionMaxMinutes      int
	SessionMaxQuestions    int
	TestDatabaseURL        string
}

func Load() (Config, error) {
	cfg := Config{
		Port:                   envOr("PORT", defaultPort),
		AppEnv:                 envOr("APP_ENV", "development"),
		Version:                defaultVersion,
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		SupabaseURL:            os.Getenv("SUPABASE_URL"),
		SupabaseServiceRoleKey: os.Getenv("SUPABASE_SERVICE_ROLE_KEY"),
		SupabaseStorageBucket:  envOr("SUPABASE_STORAGE_BUCKET", "cvs"),
		PublicAppURL:           envOr("PUBLIC_APP_URL", defaultPublicAppURL),
		FrontendOrigins:        splitList(os.Getenv("FRONTEND_ORIGIN")),
		BFFSharedSecret:        os.Getenv("BFF_SHARED_SECRET"),
		RecruiterPassword:      os.Getenv("RECRUITER_PASSWORD"),
		AuthTokenSecret:        os.Getenv("AUTH_TOKEN_SECRET"),
		LLMMode:                envOr("LLM_MODE", "openrouter"),
		LLMBaseURL:             envOr("LLM_BASE_URL", defaultLLMBaseURL),
		OpenRouterAPIKey:       os.Getenv("OPENROUTER_API_KEY"),
		ModelsConfigPath:       envOr("MODELS_CONFIG_PATH", defaultModelsPath),
		EmailMode:              envOr("EMAIL_MODE", "log"),
		GmailClientID:          os.Getenv("GMAIL_CLIENT_ID"),
		GmailClientSecret:      os.Getenv("GMAIL_CLIENT_SECRET"),
		GmailRefreshToken:      os.Getenv("GMAIL_REFRESH_TOKEN"),
		EmailFrom:              envOr("EMAIL_FROM", defaultEmailFrom),
		TestDatabaseURL:        os.Getenv("TEST_DATABASE_URL"),
	}

	var err error
	if cfg.SessionTTLHours, err = envInt("SESSION_TTL_HOURS", defaultSessionTTL); err != nil {
		return Config{}, err
	}
	if cfg.SessionMaxMinutes, err = envInt("SESSION_MAX_MINUTES", defaultSessionMinutes); err != nil {
		return Config{}, err
	}
	if cfg.SessionMaxQuestions, err = envInt("SESSION_MAX_QUESTIONS", defaultSessionMaxQues); err != nil {
		return Config{}, err
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	if c.Port == "" {
		return fmt.Errorf("PORT must not be empty")
	}
	switch c.LLMMode {
	case "openrouter", "fake":
	default:
		return fmt.Errorf("LLM_MODE must be openrouter or fake, got %q", c.LLMMode)
	}
	switch c.EmailMode {
	case "log", "gmail":
	default:
		return fmt.Errorf("EMAIL_MODE must be log or gmail, got %q", c.EmailMode)
	}
	for _, origin := range c.FrontendOrigins {
		if origin == "*" {
			return fmt.Errorf("FRONTEND_ORIGIN must not contain a wildcard")
		}
	}
	if len(c.AuthTokenSecret) < auth.MinSecretBytes {
		return fmt.Errorf("AUTH_TOKEN_SECRET must be at least %d bytes", auth.MinSecretBytes)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return n, nil
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
