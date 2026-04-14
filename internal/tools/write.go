package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// odooWriteHandler handles the odoo_write tool.
// Updates existing Odoo records.
func odooWriteHandler(ctx context.Context, request mcp.CallToolRequest, deps Deps) (*mcp.CallToolResult, error) {
	start := time.Now()
	sess := sessionID(ctx, deps)
	client := odooClient(ctx, deps)

	// Extract parameters
	model := request.GetString("model", "")
	if model == "" {
		return toolError("model is required"), nil
	}

	idsStr := request.GetString("ids", "")
	if idsStr == "" {
		return toolError("ids is required"), nil
	}

	valuesStr := request.GetString("values", "")
	if valuesStr == "" {
		return toolError("values is required"), nil
	}

	// Guardrails checks
	if err := deps.Guardrails.CheckRate(sess); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("rate limit exceeded: %v", err), nil
	}

	if err := deps.Guardrails.CheckModel(model); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("model blocked: %v", err), nil
	}

	if err := deps.Guardrails.CheckWrite(); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("write not allowed: %v", err), nil
	}

	// Parse ids
	ids, err := parseIDs(idsStr)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid ids: %v", err), nil
	}

	// Parse values
	values, err := parseValues(valuesStr)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			IDs:       ids,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid values: %v", err), nil
	}

	// Call Odoo
	err = client.Write(ctx, model, ids, values)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_write",
			Model:     model,
			IDs:       ids,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("write failed: %v", err), nil
	}

	// Audit success
	auditResult(ctx, deps, audit.Entry{
		SessionID: sess,
		Tool:      "odoo_write",
		Model:     model,
		IDs:       ids,
	}, start, nil)

	// Return result
	result := map[string]interface{}{
		"success": true,
		"updated": len(ids),
	}
	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}
