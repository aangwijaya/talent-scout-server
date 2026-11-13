package e2e

// defaultLLMBaseURL is used when LLM_BASE_URL is not set.
const defaultLLMBaseURL = "https://openrouter.ai/api/v1"

// liveCredentialsFrom resolves the live-model configuration from the process
// environment only. A missing OPENROUTER_API_KEY means the live tests skip;
// there is deliberately no fallback to a file such as the repo .env.
func liveCredentialsFrom(getenv func(string) string) (key, baseURL string, ok bool) {
	key = getenv("OPENROUTER_API_KEY")
	if key == "" {
		return "", "", false
	}
	baseURL = getenv("LLM_BASE_URL")
	if baseURL == "" {
		baseURL = defaultLLMBaseURL
	}
	return key, baseURL, true
}
