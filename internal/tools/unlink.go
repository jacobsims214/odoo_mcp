package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// odooUnlinkHandler handles the odoo_unlink tool.
// Deletes Odoo records permanently.
func odooUnlinkHandler(ctx context.Context, req *mcp.CallToolRequest, input UnlinkInput, deps Deps) (*mcp.CallToolResult, any, error) {
	start := time.Now()
	sess := sessionID(req.Session)
	client := odooClient(ctx, deps)

	// Guardrails checks
	if err := deps.Guardrails.CheckRate(ctx, sess); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolError(err.Error()), nil, nil
	}

	if err := deps.Guardrails.CheckModel(input.Model); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("model blocked: %v", err), nil, nil
	}

	if err := deps.Guardrails.CheckWrite(); err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("write not allowed: %v", err), nil, nil
	}

	// Parse ids
	ids, err := parseIDs(input.IDs)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("invalid ids: %v", err), nil, nil
	}

	// Validate ids is non-empty (prevent accidental mass delete)
	if len(ids) == 0 {
		errMsg := "ids must not be empty"
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			Success:   false,
			Error:     errMsg,
		}, start, nil)
		return toolError(errMsg), nil, nil
	}

	// Call Odoo
	err = client.Unlink(ctx, input.Model, ids)
	if err != nil {
		auditResult(ctx, deps, audit.Entry{
			SessionID: sess,
			Tool:      "odoo_unlink",
			Model:     input.Model,
			IDs:       ids,
			Success:   false,
			Error:     err.Error(),
		}, start, err)
		return toolErrorf("unlink failed: %v", err), nil, nil
	}

	// Audit success
	auditResult(ctx, deps, audit.Entry{
		SessionID: sess,
		Tool:      "odoo_unlink",
		Model:     input.Model,
		IDs:       ids,
	}, start, nil)

	// Return result
	result := map[string]interface{}{
		"success": true,
		"deleted": len(ids),
	}
	resultJSON, _ := json.Marshal(result)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(resultJSON)}},
	}, nil, nil
}
