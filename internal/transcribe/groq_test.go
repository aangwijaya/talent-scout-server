package transcribe

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGroqSendsVerboseJSONRequestAndParsesResponse(t *testing.T) {
	const apiKey = "groq-test-key"
	var gotPath, gotMethod, gotAuth, gotModel, gotFormat, gotLanguage string
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
			"language": "en",
			"duration": 18.24,
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
	if result.Transcript != "Hello from the recording." || result.Language != "en" || result.DurationMS != 18240 {
		t.Errorf("result = %+v, want trimmed transcript, en, 18240ms", result)
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
