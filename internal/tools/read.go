package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// readTool returns the read tool definition.
func readTool() mcp.Tool {
	return mcp.NewTool("odoo_read",
		mcp.WithDescription(`Read specific fields on Odoo records by their IDs. Use when you already know the record IDs.`),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order', 'avware.units'"),
		),
		mcp.WithString("ids",
			mcp.Required(),
			mcp.Description("JSON array of integer IDs, e.g. [1, 2, 3]"),
		),
		mcp.WithString("fields",
			mcp.Description(`JSON array of field names to return. Example: ["name","email","phone"].
If omitted, returns all fields (may be slow for large models).`),
			mcp.DefaultString("[]"),
		),
	)
}

// makeReadHandler returns the handler for read.
func makeReadHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs
		model, err := request.RequireString("model")
		if err != nil {
			return toolError(err.Error()), nil
		}

		idsStr, err := request.RequireString("ids")
		if err != nil {
			return toolError(err.Error()), nil
		}

		ids, err := parseIDs(idsStr)
		if err != nil {
			return toolErrorf("invalid ids: %s", err), nil
		}

		fieldsStr := request.GetString("fields", "[]")
		fields, err := parseStringArray(fieldsStr)
		if err != nil {
			return toolErrorf("invalid fields: %s", err), nil
		}

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			return toolError(err.Error()), nil
		}
		if err := deps.Guardrails.CheckModel(model); err != nil {
			return toolError(err.Error()), nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		records, err := client.Read(ctx, model, ids, fields)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_read", Model: model, Method: "read",
		}, start, err)

		if err != nil {
			return toolErrorf("read failed: %s", err), nil
		}

		// 5. Return JSON result
		result, err := mcp.NewToolResultJSON(records)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil
		}
		return result, nil
	}
}
