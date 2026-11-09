package session

import "sync"

// Registry tracks the sessions that have a live runner goroutine in this
// process. The sweeper uses it to leave those sessions alone: only a session no
// runner owns (left over after a restart, or finished) may be abandoned.
type Registry struct {
	mu     sync.Mutex
	active map[string]struct{}
}

// NewRegistry returns an empty live-runner registry.
func NewRegistry() *Registry {
	return &Registry{active: make(map[string]struct{})}
}

// Add marks a session as owned by a live runner.
func (r *Registry) Add(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active[sessionID] = struct{}{}
}

// Remove clears the live-runner mark for a session.
func (r *Registry) Remove(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, sessionID)
}

// Has reports whether a live runner currently owns the session.
func (r *Registry) Has(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.active[sessionID]
	return ok
}
