package session

import (
	"sync"

	"github.com/aangwijaya/talent-scout-server/internal/domain"
)

// FinishFunc is called when the sweeper finishes a session. It reports whether
// an in-process runner handled the finish (and emitted session.ended); when it
// returns false the caller must record the event itself so a reconnecting
// client still sees it.
type FinishFunc func(sessionID string, reason domain.FinishReason) bool

// Registry tracks the sessions that have a graph step in flight in this
// process. The sweeper leaves those sessions alone for the duration of the
// step; once the step ends the session is removed and becomes eligible for the
// idle/time-limit rules again. A session whose runner is idle must never be
// treated as owned, or it would stay active forever.
type Registry struct {
	mu       sync.Mutex
	inFlight map[string]struct{}
	onFinish FinishFunc
}

// NewRegistry returns an empty in-flight registry.
func NewRegistry() *Registry {
	return &Registry{inFlight: make(map[string]struct{})}
}

// Add marks a session as having a step in flight.
func (r *Registry) Add(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inFlight[sessionID] = struct{}{}
}

// Remove clears the in-flight mark for a session.
func (r *Registry) Remove(sessionID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.inFlight, sessionID)
}

// Has reports whether a step is currently in flight for the session.
func (r *Registry) Has(sessionID string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.inFlight[sessionID]
	return ok
}

// SetFinishHook installs the session manager callback used by the sweeper.
func (r *Registry) SetFinishHook(fn FinishFunc) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onFinish = fn
}

// NotifyFinished lets the sweeper tell an in-process manager that it finished a
// session. It reports whether a manager handled it.
func (r *Registry) NotifyFinished(sessionID string, reason domain.FinishReason) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	fn := r.onFinish
	r.mu.Unlock()
	if fn == nil {
		return false
	}
	return fn(sessionID, reason)
}
