package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/guardrails"
	"github.com/simstech/odoo-mcp/internal/odoo"
)

// Deps holds all dependencies shared by tool handlers.
type Deps struct {
	Odoo       odoo.OdooClient
	Guardrails *guardrails.Guardrails
	Audit      audit.AuditLogger
}

// RegisterAll registers all Odoo MCP tools on the given server.
func RegisterAll(s *mcp.Server, deps Deps) {
	mcp.AddTool(s, createTool(), makeCreateHandler(deps))
	mcp.AddTool(s, writeTool(), makeWriteHandler(deps))
	mcp.AddTool(s, unlinkTool(), makeUnlinkHandler(deps))
	mcp.AddTool(s, searchReadTool(), makeSearchReadHandler(deps))
	mcp.AddTool(s, readTool(), makeReadHandler(deps))
	mcp.AddTool(s, callTool(), makeCallHandler(deps))
	mcp.AddTool(s, fieldsGetTool(), makeFieldsGetHandler(deps))
	mcp.AddTool(s, listModelsTool(), makeListModelsHandler(deps))
	mcp.AddTool(s, serverInfoTool(), makeServerInfoHandler(deps))
	mcp.AddTool(s, messagePostTool(), makeMessagePostHandler(deps))
}

// Input structs for tools defined in this file.

// CreateInput is the typed input for odoo_create.
type CreateInput struct {
	Model  string `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order'"`
	Values string `json:"values" jsonschema:"JSON object of field values, e.g. {\"name\": \"New Partner\", \"email\": \"new@example.com\"}"`
}

// WriteInput is the typed input for odoo_write.
type WriteInput struct {
	Model  string `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order'"`
	IDs    string `json:"ids" jsonschema:"JSON array of integer IDs, e.g. [42, 43]"`
	Values string `json:"values" jsonschema:"JSON object of field values to update"`
}

// UnlinkInput is the typed input for odoo_unlink.
type UnlinkInput struct {
	Model string `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order'"`
	IDs   string `json:"ids" jsonschema:"JSON array of integer IDs to delete"`
}

// createTool returns the create tool definition.
func createTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_create",
		Description: "Create a new Odoo record. Returns the ID of the created record.",
	}
}

// makeCreateHandler returns the handler for create.
func makeCreateHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, CreateInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input CreateInput) (*mcp.CallToolResult, any, error) {
		return odooCreateHandler(ctx, req, input, deps)
	}
}

// writeTool returns the write tool definition.
func writeTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_write",
		Description: "Update existing Odoo records. Provide the record IDs and the fields to update.",
	}
}

// makeWriteHandler returns the handler for write.
func makeWriteHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, WriteInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input WriteInput) (*mcp.CallToolResult, any, error) {
		return odooWriteHandler(ctx, req, input, deps)
	}
}

// unlinkTool returns the unlink tool definition.
func unlinkTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_unlink",
		Description: "Delete Odoo records permanently. This cannot be undone. Provide the record IDs to delete.",
	}
}

// makeUnlinkHandler returns the handler for unlink.
func makeUnlinkHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, UnlinkInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input UnlinkInput) (*mcp.CallToolResult, any, error) {
		return odooUnlinkHandler(ctx, req, input, deps)
	}
}

// readTool and makeReadHandler are implemented in read.go
// searchReadTool and makeSearchReadHandler are implemented in search_read.go
// callTool and makeCallHandler are implemented in call.go
// fieldsGetTool and makeFieldsGetHandler are implemented in fields_get.go
// listModelsTool and makeListModelsHandler are implemented in list_models.go
// serverInfoTool and makeServerInfoHandler are implemented in server_info.go
// messagePostTool and makeMessagePostHandler are implemented in message_post.go

// sessionID extracts the session ID from the MCP server session.
func sessionID(sess *mcp.ServerSession) string {
	if sess != nil {
		return sess.ID()
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
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}
}

// toolErrorf formats and returns an MCP error result.
func toolErrorf(format string, args ...interface{}) *mcp.CallToolResult {
	return toolError(fmt.Sprintf(format, args...))
}
