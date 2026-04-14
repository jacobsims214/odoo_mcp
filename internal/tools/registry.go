package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/guardrails"
	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/simstech/odoo-mcp/internal/session"
)

// Deps holds all dependencies shared by tool handlers.
type Deps struct {
	Odoo       odoo.OdooClient
	Guardrails *guardrails.Guardrails
	Audit      audit.AuditLogger
	Session    *session.Session // for stdio; nil for HTTP (session from context)
}

// RegisterAll registers all Odoo MCP tools on the given server.
func RegisterAll(s *server.MCPServer, deps Deps) {
	s.AddTool(searchReadTool(), makeSearchReadHandler(deps))
	s.AddTool(readTool(), makeReadHandler(deps))
	s.AddTool(createTool(), makeCreateHandler(deps))
	s.AddTool(writeTool(), makeWriteHandler(deps))
	s.AddTool(unlinkTool(), makeUnlinkHandler(deps))
	s.AddTool(callTool(), makeCallHandler(deps))
	s.AddTool(fieldsGetTool(), makeFieldsGetHandler(deps))
	s.AddTool(listModelsTool(), makeListModelsHandler(deps))
	s.AddTool(serverInfoTool(), makeServerInfoHandler(deps))
	// Dedicated chatter tool — handles HTML correctly via mail.message/create
	s.AddTool(messagePostTool(), makeMessagePostHandler(deps))
}

// readTool and makeReadHandler are implemented in read.go

// createTool returns the create tool definition.
func createTool() mcp.Tool {
	return mcp.NewTool("odoo_create",
		mcp.WithDescription("Create a new Odoo record. Returns the ID of the created record."),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order'"),
		),
		mcp.WithString("values",
			mcp.Required(),
			mcp.Description("JSON object of field values, e.g. {\"name\": \"New Partner\", \"email\": \"new@example.com\"}"),
		),
	)
}

// makeCreateHandler returns the handler for create.
func makeCreateHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return odooCreateHandler(ctx, request, deps)
	}
}

// writeTool returns the write tool definition.
func writeTool() mcp.Tool {
	return mcp.NewTool("odoo_write",
		mcp.WithDescription("Update existing Odoo records. Provide the record IDs and the fields to update."),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order'"),
		),
		mcp.WithString("ids",
			mcp.Required(),
			mcp.Description("JSON array of integer IDs, e.g. [42, 43]"),
		),
		mcp.WithString("values",
			mcp.Required(),
			mcp.Description("JSON object of field values to update"),
		),
	)
}

// makeWriteHandler returns the handler for write.
func makeWriteHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return odooWriteHandler(ctx, request, deps)
	}
}

// unlinkTool returns the unlink tool definition.
func unlinkTool() mcp.Tool {
	return mcp.NewTool("odoo_unlink",
		mcp.WithDescription("Delete Odoo records permanently. This cannot be undone. Provide the record IDs to delete."),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order'"),
		),
		mcp.WithString("ids",
			mcp.Required(),
			mcp.Description("JSON array of integer IDs to delete"),
		),
	)
}

// makeUnlinkHandler returns the handler for unlink.
func makeUnlinkHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return odooUnlinkHandler(ctx, request, deps)
	}
}

// callTool and makeCallHandler are implemented in call.go

// fieldsGetTool and makeFieldsGetHandler are implemented in fields_get.go

// listModelsTool and makeListModelsHandler are implemented in list_models.go

// serverInfoTool and makeServerInfoHandler are implemented in server_info.go

// sessionID extracts the session ID from context (HTTP) or returns "stdio".
func sessionID(ctx context.Context, deps Deps) string {
	if deps.Session != nil {
		return deps.Session.ID
	}
	// For HTTP transport, session ID comes from MCP server context
	sess := server.ClientSessionFromContext(ctx)
	if sess != nil {
		return sess.SessionID()
	}
	return "unknown"
}

// odooClient returns the Odoo client for the current request.
// For stdio: always deps.Odoo.
// For HTTP: look up session from context (future: per-session API key).
func odooClient(ctx context.Context, deps Deps) odoo.OdooClient {
	return deps.Odoo
}

// parseDomain parses a domain string (JSON array) into odoo.Domain.
// Returns an empty domain `[]` if the input is empty or "[]".
func parseDomain(domainStr string) (odoo.Domain, error) {
	if domainStr == "" || domainStr == "[]" {
		return odoo.Domain([]byte("[]")), nil
	}
	// Validate it's valid JSON
	var raw json.RawMessage = json.RawMessage(domainStr)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("domain is not valid JSON: %s", domainStr)
	}
	return odoo.Domain(raw), nil
}

// parseIDs parses a JSON array of IDs string into []int64.
func parseIDs(idsStr string) ([]int64, error) {
	if idsStr == "" || idsStr == "[]" {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(idsStr), &ids); err != nil {
		return nil, fmt.Errorf("ids must be a JSON array of integers: %w", err)
	}
	return ids, nil
}

// parseValues parses a JSON object string into map[string]interface{}.
func parseValues(valuesStr string) (map[string]interface{}, error) {
	if valuesStr == "" {
		return nil, fmt.Errorf("values is required")
	}
	var values map[string]interface{}
	if err := json.Unmarshal([]byte(valuesStr), &values); err != nil {
		return nil, fmt.Errorf("values must be a JSON object: %w", err)
	}
	return values, nil
}

// parseStringArray parses a JSON array of strings.
func parseStringArray(s string) ([]string, error) {
	if s == "" || s == "[]" {
		return nil, nil
	}
	var arr []string
	if err := json.Unmarshal([]byte(s), &arr); err != nil {
		return nil, fmt.Errorf("must be a JSON array of strings: %w", err)
	}
	return arr, nil
}

// auditResult writes an audit entry. start is the time the tool handler began.
func auditResult(ctx context.Context, deps Deps, entry audit.Entry, start time.Time, err error) {
	entry.DurationMS = time.Since(start).Milliseconds()
	entry.Success = err == nil
	if err != nil {
		entry.Error = err.Error()
	}
	deps.Audit.Log(ctx, entry)
}

// toolError returns an MCP error result (not a Go error — MCP protocol error).
func toolError(msg string) *mcp.CallToolResult {
	return mcp.NewToolResultError(msg)
}

// toolErrorf formats and returns an MCP error result.
func toolErrorf(format string, args ...interface{}) *mcp.CallToolResult {
	return mcp.NewToolResultError(fmt.Sprintf(format, args...))
}
