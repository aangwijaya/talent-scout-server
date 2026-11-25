package api_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func loadYAML(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return out
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func objectAt(t *testing.T, root map[string]any, path ...string) map[string]any {
	t.Helper()
	var cur any = root
	for i, key := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("%s: %q is not an object", strings.Join(path[:i], "."), path[i-1])
		}
		v, ok := m[key]
		if !ok {
			t.Fatalf("missing %s", strings.Join(path[:i+1], "."))
		}
		cur = v
	}
	m, ok := cur.(map[string]any)
	if !ok {
		t.Fatalf("%s is not an object", strings.Join(path, "."))
	}
	return m
}

func hasKey(t *testing.T, m map[string]any, key string) {
	t.Helper()
	if _, ok := m[key]; !ok {
		t.Errorf("missing key %q", key)
	}
}

func schemaProperties(t *testing.T, spec map[string]any, name string) map[string]any {
	t.Helper()
	return objectAt(t, spec, "components", "schemas", name, "properties")
}

func collectProperties(m map[string]any, out map[string]bool) {
	props, _ := m["properties"].(map[string]any)
	for name := range props {
		out[name] = true
	}
	for _, key := range []string{"oneOf", "allOf"} {
		items, _ := m[key].([]any)
		for _, item := range items {
			if child, ok := item.(map[string]any); ok {
				collectProperties(child, out)
			}
		}
	}
}

func TestOpenAPIDeclaresVoiceInterviewContract(t *testing.T) {
	spec := loadYAML(t, "openapi.yaml")
	paths := objectAt(t, spec, "paths")
	hasKey(t, paths, "/sessions/{token}/audio")
	hasKey(t, paths, "/applications/{applicationId}/messages/{messageId}/audio")

	schemas := objectAt(t, spec, "components", "schemas")
	for _, name := range []string{"VoiceSession", "AudioUploadResponse", "MessageAudio", "Fluency"} {
		hasKey(t, schemas, name)
	}

	hasKey(t, schemaProperties(t, spec, "Job"), "spoken_english_required")
	hasKey(t, schemaProperties(t, spec, "PublicJob"), "spoken_english_required")
	hasKey(t, schemaProperties(t, spec, "JobCreateRequest"), "spoken_english_required")
	hasKey(t, schemaProperties(t, spec, "JobUpdateRequest"), "spoken_english_required")

	suggestion := schemaProperties(t, spec, "RubricSuggestionResponse")
	hasKey(t, suggestion, "spoken_english_required")
	hasKey(t, suggestion, "spoken_english_reason")

	hasKey(t, schemaProperties(t, spec, "SessionDetail"), "voice")
	hasKey(t, schemaProperties(t, spec, "ApplicationDetail"), "fluency")

	entry := schemaProperties(t, spec, "MessageEntry")
	for _, name := range []string{"mode", "fallback_reason", "start_latency_ms", "speaking_ms", "rerecord_count"} {
		hasKey(t, entry, name)
	}

	sendProps := map[string]bool{}
	collectProperties(objectAt(t, spec, "components", "schemas", "SendMessageRequest"), sendProps)
	if !sendProps["content"] || !sendProps["audio_id"] {
		t.Errorf("SendMessageRequest must accept exactly one of content or audio_id, properties = %v", sendProps)
	}

	recruiterProps := map[string]bool{}
	collectProperties(objectAt(t, spec, "components", "schemas", "RecruiterMessage"), recruiterProps)
	if !recruiterProps["audio"] {
		t.Error("RecruiterMessage must expose audio")
	}

	nodeID := objectAt(t, spec, "components", "schemas", "GraphRunNode", "properties")["id"].(map[string]any)
	idEnum, _ := nodeID["enum"].([]any)
	if !containsString(idEnum, "fluency") {
		t.Errorf("GraphRunNode id enum lacks fluency: %v", idEnum)
	}

	codeSchema := objectAt(t, spec, "components", "schemas", "Error", "properties", "error", "properties", "code")
	codes, _ := codeSchema["enum"].([]any)
	for _, code := range []string{"audio_budget_exceeded", "no_speech", "transcription_unavailable", "recording_deleted"} {
		if !containsString(codes, code) {
			t.Errorf("Error code enum lacks %q", code)
		}
	}
}

func TestContractDocsDescribeVoiceInterview(t *testing.T) {
	api := read(t, "../docs/API.md")
	for _, want := range []string{
		"POST /sessions/{token}/audio",
		"GET /applications/{applicationId}/messages/{messageId}/audio",
		"spoken_english_required",
		"recording_consent",
		"SESSION_MAX_MINUTES",
		"audio_budget_exceeded",
		"transcription_unavailable",
		"recording_deleted",
		"fluency",
	} {
		if !strings.Contains(api, want) {
			t.Errorf("docs/API.md lacks %q", want)
		}
	}

	events := read(t, "../docs/EVENTS.md")
	for _, want := range []string{"fluency", "text_session", "authenticity → fluency", "fluency → recruiter_gate"} {
		if !strings.Contains(events, want) {
			t.Errorf("docs/EVENTS.md lacks %q", want)
		}
	}

	prd := read(t, "../docs/PRD.md")
	for _, want := range []string{
		"FR-16",
		"SESSION_MAX_MINUTES",
		"Your spoken answers are recorded and transcribed by a third-party service (Groq).",
		"The recordings are kept for 30 days so the recruiter can listen, then deleted. You can answer in text instead.",
		"voice sessions are ≤ $0.05",
	} {
		if !strings.Contains(prd, want) {
			t.Errorf("docs/PRD.md lacks %q", want)
		}
	}
	if strings.Contains(prd, "voice/video") {
		t.Error("docs/PRD.md still lists voice as out of scope")
	}

	architecture := read(t, "../docs/ARCHITECTURE.md")
	for _, want := range []string{"fluency", "SESSION_MAX_MINUTES", "30 days", "24 hours", "audio"} {
		if !strings.Contains(architecture, want) {
			t.Errorf("docs/ARCHITECTURE.md lacks %q", want)
		}
	}
}

func containsString(items []any, want string) bool {
	for _, item := range items {
		if s, ok := item.(string); ok && s == want {
			return true
		}
	}
	return false
}
