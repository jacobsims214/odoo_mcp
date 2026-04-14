package guardrails

import (
	"fmt"
	"sync"
	"time"

	"github.com/simstech/odoo-mcp/internal/odoo"
)

// Guardrails enforces security policies on Odoo operations.
type Guardrails struct {
	blockedModels map[string]struct{}
	readOnlyMode  bool
	rateLimiter   *rateLimiter
}

// New creates a Guardrails instance from config values.
func New(blockedModels []string, readOnly bool, rps int) *Guardrails {
	blocked := make(map[string]struct{}, len(blockedModels))
	for _, m := range blockedModels {
		blocked[m] = struct{}{}
	}
	return &Guardrails{
		blockedModels: blocked,
		readOnlyMode:  readOnly,
		rateLimiter:   newRateLimiter(rps),
	}
}

// CheckModel returns an error if the model is blocked.
func (g *Guardrails) CheckModel(model string) error {
	if _, blocked := g.blockedModels[model]; blocked {
		return fmt.Errorf("%w: %s", odoo.ErrModelBlocked, model)
	}
	return nil
}

// CheckWrite returns an error if the server is in read-only mode.
// Call this before create, write, unlink, and mutating call operations.
func (g *Guardrails) CheckWrite() error {
	if g.readOnlyMode {
		return fmt.Errorf("%w: server configured with ODOO_READ_ONLY=true", odoo.ErrReadOnly)
	}
	return nil
}

// CheckRate returns an error if the session has exceeded its rate limit.
// sessionID is used to track per-session rate limits.
func (g *Guardrails) CheckRate(sessionID string) error {
	if !g.rateLimiter.allow(sessionID) {
		return fmt.Errorf("%w: exceeded %d req/s", odoo.ErrRateLimit, g.rateLimiter.rps)
	}
	return nil
}

// rateLimiter is a simple token bucket rate limiter per session.
type rateLimiter struct {
	rps     int
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens   float64
	lastTime time.Time
}

func newRateLimiter(rps int) *rateLimiter {
	return &rateLimiter{
		rps:     rps,
		buckets: make(map[string]*bucket),
	}
}

func (r *rateLimiter) allow(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	b, ok := r.buckets[sessionID]
	if !ok {
		b = &bucket{tokens: float64(r.rps), lastTime: now}
		r.buckets[sessionID] = b
	}

	// Refill tokens based on elapsed time
	elapsed := now.Sub(b.lastTime).Seconds()
	b.tokens += elapsed * float64(r.rps)
	if b.tokens > float64(r.rps) {
		b.tokens = float64(r.rps)
	}
	b.lastTime = now

	if b.tokens >= 1.0 {
		b.tokens--
		return true
	}
	return false
}
