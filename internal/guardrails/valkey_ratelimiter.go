package guardrails

import (
	"context"
	"fmt"
	"time"

	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/valkey-io/valkey-go"
)

// rateLimitScript atomically INCRs the key and sets EXPIRE on the first increment.
// Returns the incremented count.
//
// KEYS[1] — the rate limit counter key (ratelimit:{sessionID}:{unix_second})
// ARGS — none
var rateLimitScript = valkey.NewLuaScript(`
    local count = redis.call('INCR', KEYS[1])
    if count == 1 then
        redis.call('EXPIRE', KEYS[1], 1)
    end
    return count
`)

// ValkeyRateLimiter implements a sliding window rate limiter backed by Valkey.
// It uses INCR + EXPIRE with a 1-second window to track per-session request counts.
type ValkeyRateLimiter struct {
	client valkey.Client
	rps    int
}

// NewValkeyRateLimiter creates a new Valkey-backed rate limiter.
func NewValkeyRateLimiter(client valkey.Client, rps int) *ValkeyRateLimiter {
	return &ValkeyRateLimiter{
		client: client,
		rps:    rps,
	}
}

// check implements the rateChecker interface for ValkeyRateLimiter.
//
// Key format: ratelimit:{sessionID}:{unix_timestamp_seconds}
// Uses an atomic Lua script for INCR + conditional EXPIRE to prevent key leaks
// if the process dies between two separate commands.
//
// Returns:
//   - nil if the request is allowed
//   - error wrapping odoo.ErrRateLimit if the session has exceeded its RPS
//   - error wrapping the Valkey client error if the backend is unreachable
func (v *ValkeyRateLimiter) check(ctx context.Context, sessionID string) error {
	now := time.Now().Unix()
	key := fmt.Sprintf("ratelimit:%s:%d", sessionID, now)

	result := rateLimitScript.Exec(ctx, v.client, []string{key}, nil)
	count, err := result.AsInt64()
	if err != nil {
		return fmt.Errorf("valkey rate limit check: %w", err)
	}

	if count > int64(v.rps) {
		return fmt.Errorf("%w: exceeded %d req/s", odoo.ErrRateLimit, v.rps)
	}
	return nil
}