package storage

import (
	"context"
	"sync"
)

// Fake is an in-memory Storage for tests.
type Fake struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func NewFake() *Fake {
	return &Fake{objects: make(map[string][]byte)}
}

func (f *Fake) SetErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *Fake) Upload(_ context.Context, path string, data []byte, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.objects[path] = append([]byte(nil), data...)
	return nil
}

// Get returns the stored bytes for a path so tests can assert what was uploaded.
func (f *Fake) Get(path string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.objects[path]
	return data, ok
}
