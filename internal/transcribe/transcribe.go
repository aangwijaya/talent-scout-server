// Package transcribe turns a spoken answer recording into text. All speech-to-text
// access goes through Transcriber so tests and CI can use the deterministic Fake
// instead of calling a provider.
package transcribe

import (
	"context"
	"errors"
)

// Result is one transcription. DurationMS is the provider's estimate of the
// recording length and drives the per-session audio budget and its cost.
type Result struct {
	Transcript string
	Language   string
	DurationMS int
}

// ErrUnavailable marks a provider or transport failure. The audio endpoint maps
// it to the retryable 502 transcription_unavailable response.
var ErrUnavailable = errors.New("transcription unavailable")

// Transcriber is the interface the audio upload handler calls.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, contentType string) (Result, error)
}
