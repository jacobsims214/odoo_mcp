package server

import (
	"context"
	"log/slog"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/config"
	"github.com/simstech/odoo-mcp/internal/guardrails"
	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/simstech/odoo-mcp/internal/session"
	"github.com/simstech/odoo-mcp/internal/tools"
)

// Build constructs and returns a configured MCP server.
// For stdio transport, sess is the single pre-created session.
// For HTTP transport, sess is nil (sessions are managed per-connection).
func Build(cfg *config.Config, odooClient odoo.OdooClient, auditLog audit.AuditLogger, sess *session.Session) *server.MCPServer {
	g := guardrails.New(cfg.BlockedModels, cfg.ReadOnlyMode, cfg.RateLimitRPS)

	// Lifecycle hooks for structured logging
	hooks := &server.Hooks{}
	hooks.AddBeforeAny(func(ctx context.Context, id any, method mcp.MCPMethod, msg any) {
		slog.Debug("mcp request", "method", string(method))
	})
	hooks.AddOnError(func(ctx context.Context, id any, method mcp.MCPMethod, msg any, err error) {
		slog.Error("mcp error", "method", string(method), "error", err)
	})

	s := server.NewMCPServer(
		"odoo-mcp-server",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithRecovery(), // panic recovery — never crash on bad input
		server.WithHooks(hooks),
		server.WithInstructions(`You are connected to an Odoo 19 ERP server via the Odoo MCP Server.

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
4. For workflow actions (confirm orders, post invoices), use odoo_call.`),
	)

	deps := tools.Deps{
		Odoo:       odooClient,
		Guardrails: g,
		Audit:      auditLog,
		Session:    sess,
	}
	tools.RegisterAll(s, deps)

	return s
}
