package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

const defaultGroqBaseURL = "https://api.groq.com/openai/v1"

// Groq calls the OpenAI-compatible Groq transcription endpoint with
// response_format=verbose_json. The language is not sent, so Groq detects it.
type Groq struct {
	endpoint string
	apiKey   string
	model    string
	client   *http.Client
}

// NewGroq builds a Groq client. baseURL is the API root
// (https://api.groq.com/openai/v1); a nil client gets the default HTTP client.
func NewGroq(baseURL, apiKey, model string, client *http.Client) *Groq {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultGroqBaseURL
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &Groq{
		endpoint: strings.TrimRight(baseURL, "/") + "/audio/transcriptions",
		apiKey:   apiKey,
		model:    model,
		client:   client,
	}
}

func (g *Groq) Transcribe(ctx context.Context, audio []byte, contentType string) (Result, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	filename := "answer.webm"
	switch {
	case strings.HasPrefix(contentType, "audio/ogg"):
		filename = "answer.ogg"
	case strings.HasPrefix(contentType, "audio/mp4"):
		filename = "answer.mp4"
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return Result{}, fmt.Errorf("%w: build upload: %v", ErrUnavailable, err)
	}
	if _, err := part.Write(audio); err != nil {
		return Result{}, fmt.Errorf("%w: write audio: %v", ErrUnavailable, err)
	}
	if err := writer.WriteField("model", g.model); err != nil {
		return Result{}, fmt.Errorf("%w: write model: %v", ErrUnavailable, err)
	}
	if err := writer.WriteField("response_format", "verbose_json"); err != nil {
		return Result{}, fmt.Errorf("%w: write response_format: %v", ErrUnavailable, err)
	}
	// Word timings feed the advisory fluency metrics; segments carry the
	// avg_logprob used for the transcript confidence.
	if err := writer.WriteField("timestamp_granularities[]", "word"); err != nil {
		return Result{}, fmt.Errorf("%w: write word granularity: %v", ErrUnavailable, err)
	}
	if err := writer.WriteField("timestamp_granularities[]", "segment"); err != nil {
		return Result{}, fmt.Errorf("%w: write segment granularity: %v", ErrUnavailable, err)
	}
	// Whisper-style models drop disfluencies unless the prompt keeps them, so
	// filler_rate is a lower bound even with this prompt.
	if err := writer.WriteField("prompt", disfluencyPrompt); err != nil {
		return Result{}, fmt.Errorf("%w: write prompt: %v", ErrUnavailable, err)
	}
	if err := writer.Close(); err != nil {
		return Result{}, fmt.Errorf("%w: close upload: %v", ErrUnavailable, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, &body)
	if err != nil {
		return Result{}, fmt.Errorf("%w: build request: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := g.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("%w: request failed: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The provider body may echo request data; never include it (or the key)
		// in an error that reaches the log.
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
		return Result{}, fmt.Errorf("%w: provider status %d", ErrUnavailable, resp.StatusCode)
	}

	var payload struct {
		Text     string  `json:"text"`
		Language string  `json:"language"`
		Duration float64 `json:"duration"`
		Words    []struct {
			Word  string  `json:"word"`
			Start float64 `json:"start"`
			End   float64 `json:"end"`
		} `json:"words"`
		Segments []struct {
			Start      float64  `json:"start"`
			End        float64  `json:"end"`
			AvgLogProb *float64 `json:"avg_logprob"`
		} `json:"segments"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return Result{}, fmt.Errorf("%w: decode response: %v", ErrUnavailable, err)
	}
	words := make([]domain.TranscriptWord, 0, len(payload.Words))
	for _, word := range payload.Words {
		words = append(words, domain.TranscriptWord{
			Word:  strings.TrimSpace(word.Word),
			Start: word.Start,
			End:   word.End,
		})
	}
	var confidence float64
	var weight float64
	for _, segment := range payload.Segments {
		if segment.AvgLogProb == nil {
			continue
		}
		length := segment.End - segment.Start
		if length <= 0 {
			length = 1
		}
		probability := math.Exp(*segment.AvgLogProb)
		probability = math.Max(0, math.Min(1, probability))
		confidence += probability * length
		weight += length
	}
	if weight > 0 {
		confidence /= weight
	}
	return Result{
		Transcript: strings.TrimSpace(payload.Text),
		Language:   iso6391(payload.Language),
		DurationMS: int(math.Round(payload.Duration * 1000)),
		Words:      words,
		Confidence: confidence,
	}, nil
}

// disfluencyPrompt keeps filler words in the transcript so filler_rate is not
// artificially zero. Whisper-style models still drop some disfluencies, so the
// metric remains a lower bound (documented beside computeFluency).
const disfluencyPrompt = "Umm, let me think, uh, so, hmm."

// languageNames maps the language name Groq's verbose_json returns ("English")
// to the ISO 639-1 code the API contract uses ("en").
var languageNames = map[string]string{
	"afrikaans": "af", "albanian": "sq", "amharic": "am", "arabic": "ar",
	"armenian": "hy", "azerbaijani": "az", "basque": "eu", "belarusian": "be",
	"bengali": "bn", "bosnian": "bs", "bulgarian": "bg", "catalan": "ca",
	"chinese": "zh", "croatian": "hr", "czech": "cs", "danish": "da",
	"dutch": "nl", "english": "en", "estonian": "et", "finnish": "fi",
	"french": "fr", "galician": "gl", "georgian": "ka", "german": "de",
	"greek": "el", "gujarati": "gu", "hebrew": "he", "hindi": "hi",
	"hungarian": "hu", "icelandic": "is", "indonesian": "id", "irish": "ga",
	"italian": "it", "japanese": "ja", "kannada": "kn", "kazakh": "kk",
	"korean": "ko", "latvian": "lv", "lithuanian": "lt", "macedonian": "mk",
	"malay": "ms", "malayalam": "ml", "marathi": "mr", "mongolian": "mn",
	"nepali": "ne", "norwegian": "no", "persian": "fa", "polish": "pl",
	"portuguese": "pt", "punjabi": "pa", "romanian": "ro", "russian": "ru",
	"serbian": "sr", "slovak": "sk", "slovenian": "sl", "spanish": "es",
	"swahili": "sw", "swedish": "sv", "tagalog": "tl", "tamil": "ta",
	"telugu": "te", "thai": "th", "turkish": "tr", "ukrainian": "uk",
	"urdu": "ur", "vietnamese": "vi", "welsh": "cy",
}

// iso6391 normalizes a provider language field. An unknown name is returned
// unchanged so a contract value is never silently dropped.
func iso6391(language string) string {
	trimmed := strings.TrimSpace(language)
	if trimmed == "" {
		return ""
	}
	if code, ok := languageNames[strings.ToLower(trimmed)]; ok {
		return code
	}
	return trimmed
}
