package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/cache"
	"github.com/simstech/odoo-mcp/internal/config"
	"github.com/simstech/odoo-mcp/internal/guardrails"
	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/simstech/odoo-mcp/internal/tools"
	"github.com/valkey-io/valkey-go"
)

// Build constructs and returns a configured MCP server.
// If vc is non-nil, Valkey-backed caching middleware is installed.
func Build(cfg *config.Config, odooClient odoo.OdooClient, auditLog audit.AuditLogger, vc *cache.ValkeyClient) *mcp.Server {
	var valkeyCli valkey.Client
	if vc != nil {
		valkeyCli = vc.Client()
	}
	g := guardrails.New(cfg.BlockedModels, cfg.ReadOnlyMode, cfg.RateLimitRPS, valkeyCli)

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "odoo-mcp-server",
		Version: "1.0.0",
	}, &mcp.ServerOptions{
		Instructions: `You are connected to an Odoo 19 ERP server via the Odoo MCP Server.

Available tools:
- odoo_search_read   — Search and read records atomically (preferred over search+read)
- odoo_read          — Read specific fields on known record IDs
- odoo_create        — Create a new record, returns its ID
- odoo_write         — Update existing records
- odoo_unlink        — Delete records permanently
- odoo_call          — Call any method on any model (workflow actions, name_search, etc.)
- odoo_message_post  — Post a rich HTML message to any record's chatter (USE THIS for chatter, not odoo_call)
- odoo_fields_get    — Introspect a model's fields before operating on it
- odoo_list_models   — Discover all installed Odoo models
- odoo_get_server_info — Server version and current authenticated user

IMPORTANT RULES:
1. Always use odoo_message_post for chatter messages — NOT odoo_call with message_post.
   odoo_message_post correctly handles HTML formatting. odoo_call will escape HTML tags.
2. Before operating on an unfamiliar model, call odoo_fields_get first.
3. Use odoo_search_read instead of separate search + read calls.
4. For workflow actions (confirm orders, post invoices), use odoo_call.`,
		Logger: slog.Default(),
	})

	// Panic recovery middleware — catches panics in tool handlers and returns
	// an MCP error result instead of crashing the process/connection.
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			slog.Debug("mcp request", "method", method)
			defer func() {
				if r := recover(); r != nil {
					slog.Error("panic in mcp handler", "method", method, "panic", r)
					err = fmt.Errorf("internal error: panic in %s handler", method)
				}
			}()
			result, err = next(ctx, method, req)
			if err != nil {
				slog.Error("mcp handler error", "method", method, "error", err)
			}
			return result, err
		}
	})

	// Set up Valkey-backed cache middleware (if a client was provided)
	if vc != nil {
		slog.Info("cache: connected to Valkey", "addr", cfg.ValkeyAddr)
		resolver := newUIDResolver(odooClient)
		s.AddReceivingMiddleware(cache.NewCacheMiddleware(vc, resolver, auditLog))
	} else {
		slog.Warn("cache: no Valkey client provided, caching disabled")
	}

	deps := tools.Deps{
		Odoo:       odooClient,
		Guardrails: g,
		Audit:      auditLog,
	}
	tools.RegisterAll(s, deps)

	return s
}

// uidResolver caches the Odoo UID to avoid repeated ServerInfo calls.
type uidResolver struct {
	mu     sync.Mutex
	client odoo.OdooClient
	uid    int64
	done   bool
}

func newUIDResolver(client odoo.OdooClient) cache.OdooUIDResolver {
	r := &uidResolver{client: client}
	return r.resolve
}

func (r *uidResolver) resolve(ctx context.Context) (int64, error) {
	r.mu.Lock()
	if r.done {
		uid := r.uid
		r.mu.Unlock()
		return uid, nil
	}
	r.mu.Unlock()

	info, err := r.client.ServerInfo(ctx)
	if err != nil {
		return 0, err
	}

	r.mu.Lock()
	r.uid = info.UID
	r.done = true
	r.mu.Unlock()
	return info.UID, nil
}
