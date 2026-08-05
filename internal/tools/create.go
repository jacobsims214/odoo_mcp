package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// odooCreateHandler handles the odoo_create tool.
// Creates a new Odoo record and returns its ID.
func odooCreateHandler(ctx context.Context, req *mcp.CallToolRequest, input CreateInput, deps Deps) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	sess := sessionID(req.Session)
	client := odooClient(ctx, deps)

	// Guardrails checks
	if err := deps.Guardrails.CheckRate(ctx, sess); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolError(err.Error()), nil, nil
	}

	if err := deps.Guardrails.CheckModel(input.Model); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("model blocked: %v", err), nil, nil
	}

	if err := deps.Guardrails.CheckWrite(); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("write not allowed: %v", err), nil, nil
	}

	// Parse values
	values, err := parseValues(input.Values)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid values: %v", err), nil, nil
	}

	// Call Odoo
	newID, err := client.Create(ctx, input.Model, values)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("create failed: %v", err), nil, nil
	}

	// Audit success
	auditResult(ctx, deps, audit.Entry{
		SessionID: sess,
		Tool:      "odoo_create",
		Model:     input.Model,
		IDs:       []int64{newID},
	}, start, nil)

	// Return result
	result := map[string]interface{}{
		"id": newID,
	}
	resultJSON, _ := json.Marshal(result)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(resultJSON)}},
	}, nil, nil
}
