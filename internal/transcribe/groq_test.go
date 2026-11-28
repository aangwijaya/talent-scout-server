package transcribe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"mime"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestGroqSendsVerboseJSONRequestAndParsesResponse(t *testing.T) {
	const apiKey = "groq-test-key"
	var gotPath, gotMethod, gotAuth, gotModel, gotFormat, gotLanguage, gotPrompt string
	var gotGranularities []string
	var gotFile []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotModel = r.FormValue("model")
		gotFormat = r.FormValue("response_format")
		gotLanguage = r.FormValue("language")
		gotPrompt = r.FormValue("prompt")
		gotGranularities = r.MultipartForm.Value["timestamp_granularities[]"]
		file, _, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile(file): %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		gotFile, _ = io.ReadAll(file)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"text":     "  Hello from the recording.  ",
			"language": "English",
			"duration": 18.24,
			"words": []map[string]any{
				{"word": "Hello", "start": 0.0, "end": 0.4},
				{"word": "from", "start": 0.5, "end": 0.8},
				{"word": "the", "start": 0.8, "end": 1.0},
				{"word": "recording.", "start": 1.1, "end": 1.8},
			},
			"segments": []map[string]any{
				{"start": 0.0, "end": 0.8, "avg_logprob": -0.1, "no_speech_prob": 0.01},
				{"start": 0.8, "end": 1.8, "avg_logprob": -0.3, "no_speech_prob": 0.02},
			},
		})
	}))
	defer server.Close()

	client := NewGroq(server.URL, apiKey, "whisper-large-v3-turbo", server.Client())
	result, err := client.Transcribe(context.Background(), []byte("webm-bytes"), "audio/webm;codecs=opus")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/audio/transcriptions" {
		t.Errorf("path = %q, want /audio/transcriptions", gotPath)
	}
	if gotAuth != "Bearer "+apiKey {
		t.Errorf("authorization = %q, want the bearer key", gotAuth)
	}
	if gotModel != "whisper-large-v3-turbo" {
		t.Errorf("model = %q, want whisper-large-v3-turbo", gotModel)
	}
	if gotFormat != "verbose_json" {
		t.Errorf("response_format = %q, want verbose_json", gotFormat)
	}
	if gotLanguage != "" {
		t.Errorf("language = %q, want empty so the provider auto-detects", gotLanguage)
	}
	if string(gotFile) != "webm-bytes" {
		t.Errorf("file = %q, want the uploaded bytes", gotFile)
	}
	if !slices.Contains(gotGranularities, "word") || !slices.Contains(gotGranularities, "segment") {
		t.Errorf("timestamp_granularities[] = %v, want word and segment", gotGranularities)
	}
	if !strings.Contains(gotPrompt, "Umm") || !strings.Contains(gotPrompt, "uh") {
		t.Errorf("prompt = %q, want the disfluency-keeping prompt", gotPrompt)
	}
	if result.Transcript != "Hello from the recording." || result.Language != "en" || result.DurationMS != 18240 {
		t.Errorf("result = %+v, want trimmed transcript, en, 18240ms", result)
	}
	wantWords := []struct {
		word       string
		start, end float64
	}{
		{"Hello", 0, 0.4}, {"from", 0.5, 0.8}, {"the", 0.8, 1.0}, {"recording.", 1.1, 1.8},
	}
	if len(result.Words) != len(wantWords) {
		t.Fatalf("words = %+v, want %d timed words", result.Words, len(wantWords))
	}
	for i, want := range wantWords {
		if result.Words[i].Word != want.word || result.Words[i].Start != want.start || result.Words[i].End != want.end {
			t.Errorf("word[%d] = %+v, want %+v", i, result.Words[i], want)
		}
	}
	wantConfidence := (0.8*math.Exp(-0.1) + 1.0*math.Exp(-0.3)) / 1.8
	if math.Abs(result.Confidence-wantConfidence) > 0.001 {
		t.Errorf("confidence = %v, want segment-length weighted %v", result.Confidence, wantConfidence)
	}
}

func TestGroqErrorsNeverContainTheKey(t *testing.T) {
	const apiKey = "super-secret-groq-key"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("provider rejected key " + apiKey))
	}))
	defer server.Close()

	client := NewGroq(server.URL, apiKey, "whisper-large-v3-turbo", server.Client())
	_, err := client.Transcribe(context.Background(), []byte("audio"), "audio/webm")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), apiKey) {
		t.Errorf("error leaks the API key: %v", err)
	}
}

func TestGroqRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer server.Close()

	client := NewGroq(server.URL, "key", "model", server.Client())
	_, err := client.Transcribe(context.Background(), []byte("audio"), "audio/webm")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestFakeReturnsConfiguredResultAndError(t *testing.T) {
	fake := NewFake()
	fake.SetResult(Result{Transcript: "hello", Language: "en", DurationMS: 3000})
	got, err := fake.Transcribe(context.Background(), []byte("audio"), "audio/webm")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if got.Transcript != "hello" || got.DurationMS != 3000 {
		t.Errorf("result = %+v, want hello/3000", got)
	}

	fake.SetErr(ErrUnavailable)
	if _, err := fake.Transcribe(context.Background(), []byte("audio"), "audio/webm"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestGroqPartContentTypeKeepsParameters(t *testing.T) {
	// The upload endpoint accepts `audio/webm;codecs=opus`; the provider part
	// should keep a usable content type rather than the raw header.
	mediaType, _, err := mime.ParseMediaType("audio/webm;codecs=opus")
	if err != nil || mediaType != "audio/webm" {
		t.Fatalf("ParseMediaType = %q, %v", mediaType, err)
	}
}
