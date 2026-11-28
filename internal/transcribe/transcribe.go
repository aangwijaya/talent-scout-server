// Package transcribe turns a spoken answer recording into text. All speech-to-text
// access goes through Transcriber so tests and CI can use the deterministic Fake
// instead of calling a provider.
package transcribe

import (
	"context"
	"errors"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// Result is one transcription. DurationMS is the provider's estimate of the
// recording length and drives the per-session audio budget and its cost. Words
// are the provider's word timings and Confidence is its segment-length weighted
// transcript confidence; both feed the advisory fluency metrics.
type Result struct {
	Transcript string
	Language   string
	DurationMS int
	Words      []domain.TranscriptWord
	Confidence float64
}

// ErrUnavailable marks a provider or transport failure. The audio endpoint maps
// it to the retryable 502 transcription_unavailable response.
var ErrUnavailable = errors.New("transcription unavailable")

// Transcriber is the interface the audio upload handler calls.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, contentType string) (Result, error)
}
