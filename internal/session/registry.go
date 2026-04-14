package session

import (
	"sync"
	"time"
)

// Registry manages active sessions for HTTP transport.
// For stdio transport, use a single pre-created Session directly.
type Registry struct {
	mu       sync.RWMutex
	sessions map[string]*Session
	ttl      time.Duration
	lastSeen map[string]time.Time
}

// NewRegistry creates a session registry with the given idle TTL.
func NewRegistry(ttl time.Duration) *Registry {
	r := &Registry{
		sessions: make(map[string]*Session),
		lastSeen: make(map[string]time.Time),
		ttl:      ttl,
	}
	go r.reapLoop()
	return r
}

// Get returns the session for the given ID, or nil if not found.
func (r *Registry) Get(id string) *Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[id]
}

// Set stores a session.
func (r *Registry) Set(s *Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
	r.lastSeen[s.ID] = time.Now()
}

// Delete removes a session.
func (r *Registry) Delete(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
	delete(r.lastSeen, id)
}

// Touch updates the last-seen time for a session (call on every request).
func (r *Registry) Touch(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sessions[id]; exists {
		r.lastSeen[id] = time.Now()
	}
}

// reapLoop periodically removes sessions that have been idle longer than TTL.
func (r *Registry) reapLoop() {
	ticker := time.NewTicker(r.ttl / 2)
	defer ticker.Stop()

	for range ticker.C {
		r.mu.Lock()
		now := time.Now()
		for id, lastSeen := range r.lastSeen {
			if now.Sub(lastSeen) > r.ttl {
				delete(r.sessions, id)
				delete(r.lastSeen, id)
			}
		}
		r.mu.Unlock()
	}
}
