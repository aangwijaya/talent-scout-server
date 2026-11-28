package transcribe

import (
	"context"
	"sync"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// Fake is the deterministic, network-free transcriber used by tests and by
// TRANSCRIBE_MODE=fake. Tests configure its single canned outcome.
type Fake struct {
	mu     sync.Mutex
	result Result
	err    error
	calls  int
}

func NewFake() *Fake {
	return &Fake{result: Result{
		Transcript: "fake transcript",
		Language:   "en",
		DurationMS: 5000,
		Words: []domain.TranscriptWord{
			{Word: "fake", Start: 0, End: 0.4},
			{Word: "transcript", Start: 0.5, End: 1.2},
		},
		Confidence: 0.9,
	}}
}

func (f *Fake) SetResult(result Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = result
	f.err = nil
}

func (f *Fake) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

// Calls returns how many times Transcribe was called.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *Fake) Transcribe(_ context.Context, _ []byte, _ string) (Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.err != nil {
		return Result{}, f.err
	}
	return f.result, nil
}

var _ Transcriber = (*Fake)(nil)
