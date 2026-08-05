package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// ReadOnlyTools is the set of MCP tool names that are read-only and can be cached.
var ReadOnlyTools = map[string]bool{
	"odoo_search_read":   true,
	"odoo_read":          true,
	"odoo_fields_get":    true,
	"odoo_list_models":   true,
	"odoo_get_server_info": true,
}

// WriteTools is the set of MCP tool names that mutate data and should invalidate cache.
var WriteTools = map[string]bool{
	"odoo_create":       true,
	"odoo_write":        true,
	"odoo_unlink":       true,
	"odoo_message_post": true,
	"odoo_call":         true,
}

// OdooUIDResolver is a function that returns the Odoo user ID for the current
// request context. Implementations should cache the result to avoid repeated
// API calls.
type OdooUIDResolver func(ctx context.Context) (int64, error)

// NewCacheMiddleware creates a ReceivingMiddleware that caches read-only tool
// results and invalidates cache entries on write operations.
//
// The middleware intercepts "tools/call" JSON-RPC methods:
//   - For read-only tools (search_read, read, fields_get, list_models,
//     server_info): checks the cache before calling the handler. On a cache hit,
//     returns the cached result directly. On a cache miss, calls the handler,
//     stores the result in the cache, and returns it.
//   - For write tools (create, write, unlink, message_post): calls the handler
//     first, then invalidates all cache entries with the matching prefix.
//   - For all other methods: passes through without caching.
func NewCacheMiddleware(vc *ValkeyClient, resolveUID OdooUIDResolver, auditLog audit.AuditLogger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			// Only intercept tool calls
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			ctr, ok := req.(*mcp.CallToolRequest)
			if !ok {
				return next(ctx, method, req)
			}

			toolName := ctr.Params.Name

			// Read-only tools: check cache first
			if ReadOnlyTools[toolName] {
				result, err := handleReadTool(ctx, vc, resolveUID, auditLog, next, method, req, ctr, toolName)
				return result, err
			}

			// Write tools: call handler then invalidate
			if WriteTools[toolName] {
				result, err := handleWriteTool(ctx, vc, resolveUID, next, method, req, ctr, toolName)
				return result, err
			}

			// All other tools/methods: pass through
			return next(ctx, method, req)
		}
	}
}

// handleReadTool checks the cache first, then falls through to the handler.
func handleReadTool(
	ctx context.Context,
	vc *ValkeyClient,
	resolveUID OdooUIDResolver,
	auditLog audit.AuditLogger,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
	ctr *mcp.CallToolRequest,
	toolName string,
) (mcp.Result, error) {
	uid, err := resolveUID(ctx)
	if err != nil {
		// If we can't resolve the UID, skip caching and pass through
		slog.Warn("cache: failed to resolve Odoo UID, skipping cache", "tool", toolName, "error", err)
		return next(ctx, method, req)
	}

	cacheKey := CacheKey(uid, toolName, ctr.Params.Arguments)

	// Try cache hit
	cached, err := vc.Get(ctx, cacheKey)
	if err != nil {
		slog.Warn("cache: get failed", "tool", toolName, "error", err)
		// Fall through to handler on cache error
	} else if cached != "" {
		slog.Debug("cache: hit", "tool", toolName, "key", cacheKey)
		// Return cached result
		var result mcp.CallToolResult
		if err := json.Unmarshal([]byte(cached), &result); err != nil {
			slog.Warn("cache: unmarshal failed", "tool", toolName, "error", err)
			// Fall through to handler on unmarshal error
		} else {
			// Emit audit entry for cache-served data
			sessID := ""
			if ctr.Session != nil {
				sessID = ctr.Session.ID()
			}
			auditLog.Log(ctx, audit.Entry{
				SessionID: sessID,
				Tool:      toolName,
				DurationMS: 0,
				Success:   true,
				CacheHit:  true,
			})
			return &result, nil
		}
	}

	// Cache miss or error: call the handler
	result, err := next(ctx, method, req)
	if err != nil {
		return result, err
	}

	// Store in cache if successful
	if callResult, ok := result.(*mcp.CallToolResult); ok && !callResult.IsError {
		data, marshalErr := json.Marshal(callResult)
		if marshalErr != nil {
			slog.Warn("cache: marshal failed", "tool", toolName, "error", marshalErr)
		} else {
			ttl := ToolTTL(toolName)
			if setErr := vc.Set(ctx, cacheKey, string(data), ttl); setErr != nil {
				slog.Warn("cache: set failed", "tool", toolName, "error", setErr)
			} else {
				slog.Debug("cache: set", "tool", toolName, "key", cacheKey, "ttl", ttl)
			}
		}
	}

	return result, nil
}

// handleWriteTool calls the handler, then invalidates cache entries.
func handleWriteTool(
	ctx context.Context,
	vc *ValkeyClient,
	resolveUID OdooUIDResolver,
	next mcp.MethodHandler,
	method string,
	req mcp.Request,
	ctr *mcp.CallToolRequest,
	toolName string,
) (mcp.Result, error) {
	// Call the handler first
	result, err := next(ctx, method, req)
	if err != nil {
		return result, err
	}

	// Only invalidate on success
	if callResult, ok := result.(*mcp.CallToolResult); ok && callResult.IsError {
		return result, nil
	}

	// Resolve UID for cache invalidation prefix
	uid, err := resolveUID(ctx)
	if err != nil {
		slog.Warn("cache: failed to resolve Odoo UID for invalidation", "tool", toolName, "error", err)
		return result, nil
	}

	// Invalidate all cache entries for this user
	prefix := cachePrefix(uid)
	if invErr := vc.Invalidate(ctx, prefix); invErr != nil {
		slog.Warn("cache: invalidation failed", "tool", toolName, "prefix", prefix, "error", invErr)
	} else {
		slog.Debug("cache: invalidated", "tool", toolName, "prefix", prefix)
	}

	return result, nil
}

// cachePrefix returns the cache key prefix for a given Odoo UID.
// All cache entries for this user start with this prefix.
func cachePrefix(uid int64) string {
	return fmt.Sprintf("mcp:%d:", uid)
}