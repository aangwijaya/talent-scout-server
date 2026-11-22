//go:build live

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
	"github.com/aangwijaya/talent-scout-server/internal/graph"
	"github.com/aangwijaya/talent-scout-server/internal/sse"
)

// TestLiveEndToEnd runs the full flow against OpenRouter with the seed job and
// the sample CVs. It is skipped unless OPENROUTER_API_KEY is set. Budget: one
// session per CV.
func TestLiveEndToEnd(t *testing.T) {
	apiKey, baseURL := liveCredentials(t)

	root := repoRoot()
	cvs := []string{
		filepath.Join(root, "testdata", "cvs", "ayu-prameswari.pdf"),
		filepath.Join(root, "testdata", "cvs", "bima-saputra.pdf"),
		filepath.Join(root, "testdata", "cvs", "citra-lestari.pdf"),
	}
	if only := os.Getenv("E2E_LIVE_CV"); only != "" {
		cvs = []string{filepath.Join(root, only)}
	}

	answers := defaultAnswers
	if strings.Contains(os.Getenv("E2E_LIVE_ANSWERS"), "thin") {
		answers = thinAnswers
	}

	var sawRetry bool
	for _, cv := range cvs {
		cv := cv
		t.Run(filepath.Base(cv), func(t *testing.T) {
			recorder := &recordingTransport{base: http.DefaultTransport}
			defer reportLLMCalls(t, recorder)
			client := &http.Client{Transport: recorder, Timeout: 90 * time.Second}
			live := NewHarness(t, HarnessOptions{
				Live:    true,
				BaseURL: baseURL,
				APIKey:  apiKey,
				Client:  client,
			})

			result := live.Run(t, cv, answers, 5*time.Minute)
			logPlannedTopics(t, recorder)

			assistantTurns := 0
			for _, message := range result.Messages {
				if message.Role != domain.MessageRoleAssistant {
					continue
				}
				assistantTurns++
				if problems := interviewerTurnProblems(message.Content); len(problems) > 0 {
					t.Errorf("interviewer turn %d %s: %q", assistantTurns, strings.Join(problems, ", "), message.Content)
				}
			}
			for _, event := range result.Events {
				if event.Type != graph.EventNodeFailed || event.NodeID == nil || *event.NodeID != "interviewer" {
					continue
				}
				var failed graph.NodeFailed
				if json.Unmarshal(event.Payload, &failed) == nil && failed.Error == "timeout" {
					attempt := 0
					if event.Attempt != nil {
						attempt = *event.Attempt
					}
					t.Errorf("interviewer attempt %d timed out in run %s", attempt, result.RunID)
				}
			}

			valid := 0
			for _, score := range result.Scores {
				if score.Status == domain.ScoreStatusValid {
					valid++
				}
			}
			if valid == 0 {
				t.Errorf("no rubric criterion got a valid evidence quote (scores: %+v)", result.Scores)
			}
			t.Logf("run %s: tokens_in=%d tokens_out=%d cost_usd=%.6f finish_reason=%s",
				result.RunID, result.Run.TotalTokensIn, result.Run.TotalTokensOut,
				result.Run.CostUSD, finishReason(result.Events))

			if hasLoopRetry(result.Events) {
				sawRetry = true
				t.Logf("captured a real loop.retry in run %s", result.RunID)
				if out := os.Getenv("E2E_FIXTURE_OUT"); out != "" {
					writeFixture(t, out, result.Events)
				}
			}
		})
	}

	if os.Getenv("E2E_REQUIRE_LOOP_RETRY") != "" && !sawRetry {
		t.Fatal("no live run emitted loop.retry")
	}
}

var emptyAnswers = []string{
	"Yes.",
	"No.",
	"It depends.",
	"Maybe.",
	"I forgot.",
}

// TestLiveEvidenceRetryFixture records the evidence-check retry path from a real
// model run. One-word answers cannot supply a 3-word verbatim quote, so every
// scorer attempt fails evidence_check and the second attempt emits loop.retry.
// It is opt-in so a normal live run does not spend an extra session.
func TestLiveEvidenceRetryFixture(t *testing.T) {
	if os.Getenv("E2E_CAPTURE_RETRY") != "1" {
		t.Skip("set E2E_CAPTURE_RETRY=1 to record the retry fixture")
	}
	apiKey, baseURL := liveCredentials(t)

	recorder := &recordingTransport{base: http.DefaultTransport}
	defer reportLLMCalls(t, recorder)
	client := &http.Client{Transport: recorder, Timeout: 90 * time.Second}
	live := NewHarness(t, HarnessOptions{
		Live:    true,
		BaseURL: baseURL,
		APIKey:  apiKey,
		Client:  client,
	})
	result := live.Run(t, filepath.Join(live.Root, "testdata", "cvs", "bima-saputra.pdf"), emptyAnswers, 5*time.Minute)

	if !hasLoopRetry(result.Events) {
		t.Fatal("the run emitted no loop.retry")
	}
	retries := 0
	for _, event := range result.Events {
		if event.Type == graph.EventLoopRetry {
			retries++
		}
	}
	if retries < 1 {
		t.Fatalf("loop.retry events = %d, want at least 1", retries)
	}
	needsManual := 0
	for _, score := range result.Scores {
		if score.Status == domain.ScoreStatusNeedsManual {
			needsManual++
		}
	}
	if needsManual != len(result.Scores) {
		t.Errorf("needs_manual scores = %d/%d, want all of them", needsManual, len(result.Scores))
	}

	out := os.Getenv("E2E_FIXTURE_OUT")
	if out == "" {
		t.Fatal("E2E_FIXTURE_OUT must point at testdata/recorded-run-with-loop-retry.json")
	}
	writeFixture(t, out, result.Events)
	t.Logf("recorded %d events (%d loop.retry) at %s", len(result.Events), retries, out)
}

func liveCredentials(t *testing.T) (key, baseURL string) {
	t.Helper()
	key, baseURL, ok := liveCredentialsFrom(os.Getenv)
	if !ok {
		t.Skip("OPENROUTER_API_KEY is not set; live E2E is skipped")
	}
	return key, baseURL
}

func finishReason(events []domain.NodeEvent) string {
	for _, event := range events {
		if event.Type != graph.EventRunCompleted {
			continue
		}
		var payload graph.RunCompleted
		if json.Unmarshal(event.Payload, &payload) == nil {
			return payload.FinishReason
		}
	}
	return ""
}

func hasLoopRetry(events []domain.NodeEvent) bool {
	for _, event := range events {
		if event.Type == graph.EventLoopRetry {
			return true
		}
	}
	return false
}

// recordedCall keeps only the request/response bodies. The Authorization header
// is never stored or printed.
type recordedCall struct {
	request  []byte
	response *bodyRecorder
}

type bodyRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *bodyRecorder) write(p []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf.Write(p)
}

func (r *bodyRecorder) bytes() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf.Bytes()...)
}

type recordingTransport struct {
	base  http.RoundTripper
	mu    sync.Mutex
	calls []recordedCall
}

func (rt *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
	}

	resp, err := rt.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	recorder := &bodyRecorder{}
	resp.Body = &teeBody{ReadCloser: resp.Body, recorder: recorder}

	rt.mu.Lock()
	rt.calls = append(rt.calls, recordedCall{request: body, response: recorder})
	rt.mu.Unlock()
	return resp, nil
}

type teeBody struct {
	io.ReadCloser
	recorder *bodyRecorder
}

func (t *teeBody) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		t.recorder.write(p[:n])
	}
	return n, err
}

type llmChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type plannedTopicInfo struct {
	Title        string `json:"title"`
	Source       string `json:"source"`
	SeedQuestion string `json:"seed_question"`
}

// logPlannedTopics prints the planner's topics with their internal source and
// first question, so a live run's PR body can show where each topic came from.
func logPlannedTopics(t *testing.T, recorder *recordingTransport) {
	t.Helper()
	recorder.mu.Lock()
	calls := append([]recordedCall(nil), recorder.calls...)
	recorder.mu.Unlock()

	for _, call := range calls {
		var req llmRequestInfo
		if err := json.Unmarshal(call.request, &req); err != nil {
			continue
		}
		if nodeFromRequest(req) != "planner" {
			continue
		}
		var body llmChatResponse
		if err := json.Unmarshal(bytes.TrimSpace(call.response.bytes()), &body); err != nil || len(body.Choices) == 0 {
			continue
		}
		var plan struct {
			Topics []plannedTopicInfo `json:"topics"`
		}
		if err := json.Unmarshal([]byte(body.Choices[0].Message.Content), &plan); err != nil {
			t.Logf("could not parse planner output: %v", err)
			continue
		}
		for i, topic := range plan.Topics {
			t.Logf("planned topic %d: source=%s title=%q first_question=%q",
				i+1, topic.Source, topic.Title, topic.SeedQuestion)
		}
	}
}

func reportLLMCalls(t *testing.T, recorder *recordingTransport) {
	t.Helper()
	recorder.mu.Lock()
	calls := append([]recordedCall(nil), recorder.calls...)
	recorder.mu.Unlock()

	for i, call := range calls {
		var req llmRequestInfo
		if err := json.Unmarshal(call.request, &req); err != nil {
			t.Logf("call %d: could not decode request: %v", i, err)
			continue
		}
		node := nodeFromRequest(req)
		resp := parseResponse(call.response.bytes())
		t.Logf("call %d node=%s model=%s max_tokens=%d stream=%v finish_reason=%q prompt_tokens=%d completion_tokens=%d",
			i, node, req.Model, req.MaxTokens, req.Stream, resp.FinishReason, resp.PromptTokens, resp.CompletionTokens)
		for _, problem := range truncationProblems(node, req, resp) {
			t.Errorf("call %d %s", i, problem)
		}
	}
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// writeFixture records a run as SSE frames, replacing every identifier and
// timestamp with deterministic values so no run-specific data is committed.
func writeFixture(t *testing.T, out string, events []domain.NodeEvent) {
	t.Helper()
	if !filepath.IsAbs(out) {
		out = filepath.Join(repoRoot(), out)
	}
	frames := make([]fixtureEvent, 0, len(events))
	ids := make(map[string]string)
	baseTime := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)

	for _, event := range events {
		if !sse.IsRunEvent(event.Type) {
			continue
		}
		var payload any
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("decode event %s: %v", event.Type, err)
		}
		payload = sanitizeValue(payload, ids, baseTime, event.Seq)
		sanitized, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("encode event %s: %v", event.Type, err)
		}
		frames = append(frames, fixtureEvent{
			ID:    event.Seq,
			Event: event.Type,
			Data: fixtureEnvelope{
				RunID:   sanitizeID(event.RunID, ids),
				Seq:     event.Seq,
				TS:      baseTime.Add(time.Duration(event.Seq) * time.Second).Format(time.RFC3339),
				NodeID:  event.NodeID,
				Attempt: event.Attempt,
				Data:    sanitized,
			},
		})
	}

	raw, err := json.MarshalIndent(frames, "", "  ")
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatalf("create fixture dir: %v", err)
	}
	if err := os.WriteFile(out, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Logf("wrote %d events to %s", len(frames), out)
}

func sanitizeValue(value any, ids map[string]string, baseTime time.Time, seq int64) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			out[key] = sanitizeValue(item, ids, baseTime, seq)
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeValue(item, ids, baseTime, seq)
		}
		return out
	case string:
		if uuidPattern.MatchString(typed) {
			return sanitizeID(typed, ids)
		}
		if parsed, err := time.Parse(time.RFC3339, typed); err == nil && !parsed.IsZero() {
			return baseTime.Add(time.Duration(seq) * time.Second).Format(time.RFC3339)
		}
		return typed
	default:
		return value
	}
}

func sanitizeID(id string, ids map[string]string) string {
	if mapped, ok := ids[id]; ok {
		return mapped
	}
	next := len(ids) + 1
	mapped := fmt.Sprintf("00000000-0000-4000-8000-%012d", next)
	ids[id] = mapped
	return mapped
}
