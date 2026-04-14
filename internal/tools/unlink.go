package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// odooUnlinkHandler handles the odoo_unlink tool.
// Deletes Odoo records permanently.
func odooUnlinkHandler(ctx context.Context, request mcp.CallToolRequest, deps Deps) (*mcp.CallToolResult, error) {
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

	// Guardrails checks
	if err := deps.Guardrails.CheckRate(sess); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("rate limit exceeded: %v", err), nil
	}

	if err := deps.Guardrails.CheckModel(model); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("model blocked: %v", err), nil
	}

	if err := deps.Guardrails.CheckWrite(); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
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
			Tool:      "odoo_unlink",
			Model:     model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid ids: %v", err), nil
	}

	// Validate ids is non-empty (prevent accidental mass delete)
	if len(ids) == 0 {
		err := "ids must not be empty"
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     model,
			Success:   false,
			Error:     err,
		}, start, nil)
		return toolError(err), nil
	}

	// Call Odoo
	err = client.Unlink(ctx, model, ids)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     model,
			IDs:       ids,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("unlink failed: %v", err), nil
	}

	// Audit success
	auditResult(ctx, deps, audit.Entry{
		SessionID: sess,
		Tool:      "odoo_unlink",
		Model:     model,
		IDs:       ids,
	}, start, nil)

	// Return result
	result := map[string]interface{}{
		"success": true,
		"deleted": len(ids),
	}
	resultJSON, _ := json.Marshal(result)
	return mcp.NewToolResultText(string(resultJSON)), nil
}
