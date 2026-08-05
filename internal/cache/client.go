package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

// ValkeyClient wraps a valkey.Client with cache-specific operations.
type ValkeyClient struct {
	client valkey.Client
}

// NewValkeyClient creates a new ValkeyClient connected to the given address.
func NewValkeyClient(addr string) (*ValkeyClient, error) {
	client, err := valkey.NewClient(valkey.ClientOption{
		InitAddress: []string{addr},
	})
	if err != nil {
		return nil, fmt.Errorf("valkey connect: %w", err)
	}
	return &ValkeyClient{client: client}, nil
}

// Client returns the underlying valkey.Client for use by other components
// (e.g., guardrails for Valkey-backed rate limiting).
func (vc *ValkeyClient) Client() valkey.Client {
	return vc.client
}

// Close shuts down the underlying Valkey connection.
func (vc *ValkeyClient) Close() {
	vc.client.Close()
}

// CacheKey builds a cache key in the format "mcp:{odoo_uid}:{tool}:{param_hash}".
// param_hash is the SHA-256 hex digest of the JSON-marshaled params.
func CacheKey(odooUID int64, tool string, params interface{}) string {
	hash := paramHash(params)
	return fmt.Sprintf("mcp:%d:%s:%s", odooUID, tool, hash)
}

// paramHash returns the SHA-256 hex digest of the JSON representation of v.
func paramHash(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		// If marshaling fails, use a fallback hash of the error string.
		h := sha256.Sum256([]byte(fmt.Sprintf("marshal-error:%v", err)))
		return hex.EncodeToString(h[:])
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Get retrieves a cached value by key. Returns empty string and nil if the key
// does not exist.
func (vc *ValkeyClient) Get(ctx context.Context, key string) (string, error) {
	resp := vc.client.Do(ctx, vc.client.B().Get().Key(key).Build())
	val, err := resp.ToString()
	if err != nil {
		if err == valkey.Nil {
			return "", nil
		}
		return "", fmt.Errorf("cache get: %w", err)
	}
	return val, nil
}

// Set stores a value with the given TTL. If ttl is zero, the key persists
// without expiration.
func (vc *ValkeyClient) Set(ctx context.Context, key, value string, ttl time.Duration) error {
	if ttl > 0 {
		resp := vc.client.Do(ctx, vc.client.B().Set().Key(key).Value(value).Ex(ttl).Build())
		if err := resp.Error(); err != nil {
			return fmt.Errorf("cache set: %w", err)
		}
	} else {
		resp := vc.client.Do(ctx, vc.client.B().Set().Key(key).Value(value).Build())
		if err := resp.Error(); err != nil {
			return fmt.Errorf("cache set: %w", err)
		}
	}
	return nil
}

// Invalidate deletes all cache keys matching the given prefix.
// The prefix should include the trailing colon, e.g. "mcp:1:odoo_search_read:".
func (vc *ValkeyClient) Invalidate(ctx context.Context, prefix string) error {
	var cursor uint64
	var keys []string
	for {
		// SCAN for keys matching the prefix
		resp := vc.client.Do(ctx, vc.client.B().Scan().Cursor(cursor).Match(prefix+"*").Build())
		entries, err := resp.AsScanEntry()
		if err != nil {
			return fmt.Errorf("cache scan: %w", err)
		}
		keys = append(keys, entries.Elements...)
		cursor = entries.Cursor
		if cursor == 0 {
			break
		}
	}

	if len(keys) == 0 {
		return nil
	}

	// Delete all matched keys
	resp := vc.client.Do(ctx, vc.client.B().Del().Key(keys...).Build())
	if _, err := resp.AsInt64(); err != nil {
		return fmt.Errorf("cache del: %w", err)
	}
	return nil
}

// TTL constants for different result types.
const (
	// TTLSearch is the TTL for search_read results (30 seconds).
	TTLSearch = 30 * time.Second
	// TTLRead is the TTL for read results (30 seconds).
	TTLRead = 30 * time.Second
	// TTLFields is the TTL for fields_get results (5 minutes).
	TTLFields = 5 * time.Minute
	// TTLModels is the TTL for list_models results (1 hour).
	TTLModels = 1 * time.Hour
	// TTLServerInfo is the TTL for server_info results (5 minutes).
	TTLServerInfo = 5 * time.Minute
)

// ToolTTL returns the appropriate cache TTL for a given tool name.
func ToolTTL(tool string) time.Duration {
	switch tool {
	case "odoo_search_read":
		return TTLSearch
	case "odoo_read":
		return TTLRead
	case "odoo_fields_get":
		return TTLFields
	case "odoo_list_models":
		return TTLModels
	case "odoo_get_server_info":
		return TTLServerInfo
	default:
		return TTLSearch
	}
}