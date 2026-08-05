package guardrails

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/valkey-io/valkey-go"
)

// rateChecker is the interface for rate limiting implementations.
// Both the in-memory token bucket and Valkey-backed implementations satisfy it.
// Returns nil if the request is allowed, an ErrRateLimit-wrapped error if the
// rate is exceeded, or a wrapped backend error if the underlying store fails.
type rateChecker interface {
	check(ctx context.Context, sessionID string) error
}

// Guardrails enforces security policies on Odoo operations.
type Guardrails struct {
	blockedModels map[string]struct{}
	readOnlyMode  bool
	rateLimiter   rateChecker
	rps           int
}

// New creates a Guardrails instance from config values.
// If vc is non-nil, a Valkey-backed rate limiter is used; otherwise the
// in-memory token bucket is used (for local dev without Valkey).
// server.Build extracts the valkey.Client from cache.ValkeyClient.Client()
// before passing it here.
func New(blockedModels []string, readOnly bool, rps int, vc valkey.Client) *Guardrails {
	blocked := make(map[string]struct{}, len(blockedModels))
	for _, m := range blockedModels {
		blocked[m] = struct{}{}
	}

	var rl rateChecker
	if vc != nil {
		rl = NewValkeyRateLimiter(vc, rps)
	} else {
		rl = newRateLimiter(rps)
	}

	return &Guardrails{
		blockedModels: blocked,
		readOnlyMode:  readOnly,
		rateLimiter:   rl,
		rps:           rps,
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
// ctx is propagated to the underlying rate checker for cancellation/deadline support.
// Returns the checker's error directly — use errors.Is(err, odoo.ErrRateLimit) to
// distinguish rate-limit rejections from backend errors.
func (g *Guardrails) CheckRate(ctx context.Context, sessionID string) error {
	return g.rateLimiter.check(ctx, sessionID)
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

func (r *rateLimiter) check(ctx context.Context, sessionID string) error {
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
		return nil
	}
	return fmt.Errorf("%w: exceeded %d req/s", odoo.ErrRateLimit, r.rps)
}
