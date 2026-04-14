package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// odooCreateHandler handles the odoo_create tool.
// Creates a new Odoo record and returns its ID.
func odooCreateHandler(ctx context.Context, request mcp.CallToolRequest, deps Deps) (*mcp.CallToolResult, error) {
	start := time.Now()
	sess := sessionID(ctx, deps)
	client := odooClient(ctx, deps)

	// Extract parameters
	model := request.GetString("model", "")
	if model == "" {
		return toolError("model is required"), nil
	}

	valuesStr := request.GetString("values", "")
	if valuesStr == "" {
		return toolError("values is required"), nil
	}

	// Guardrails checks
	if err := deps.Guardrails.CheckRate(sess); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("rate limit exceeded: %v", err), nil
	}

	if err := deps.Guardrails.CheckModel(model); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("model blocked: %v", err), nil
	}

	if err := deps.Guardrails.CheckWrite(); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("write not allowed: %v", err), nil
	}

	// Parse values
	values, err := parseValues(valuesStr)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid values: %v", err), nil
	}

	// Call Odoo
	newID, err := client.Create(ctx, model, values)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_create",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("create failed: %v", err), nil
	}

	// Audit success
	auditResult(ctx, deps, audit.Entry{
		SessionID: sess,
		Tool:      "odoo_create",
		Model:     model,
		IDs:       []int64{newID},
	}, start, nil)

	// Return result
	result := map[string]interface{}{
		"id": newID,
	}
	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}
