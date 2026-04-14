package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// fieldsGetTool returns the fields_get tool definition.
func fieldsGetTool() mcp.Tool {
	return mcp.NewTool("odoo_fields_get",
		mcp.WithDescription(`Introspect an Odoo model's fields — names, types, labels, relations, and constraints. Use this before operating on an unfamiliar model to understand its structure.`),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order'"),
		),
		mcp.WithString("attributes",
			mcp.Description(`JSON array of field attributes to return. Example: ["string","type","required","readonly","relation","help","selection"].
If omitted, returns all attributes.`),
			mcp.DefaultString(`["string","type","required","readonly","relation","help","selection"]`),
		),
	)
}

// makeFieldsGetHandler returns the handler for fields_get.
func makeFieldsGetHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs
		model, err := request.RequireString("model")
		if err != nil {
			return toolError(err.Error()), nil
		}

		attributesStr := request.GetString("attributes", `["string","type","required","readonly","relation","help","selection"]`)
		attributes, err := parseStringArray(attributesStr)
		if err != nil {
			return toolErrorf("invalid attributes: %s", err), nil
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
		fieldsResult, err := client.FieldsGet(ctx, model, attributes)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_fields_get", Model: model, Method: "fields_get",
		}, start, err)

		if err != nil {
			return toolErrorf("fields_get failed: %s", err), nil
		}

		// 5. Return JSON result
		result, err := mcp.NewToolResultJSON(fieldsResult)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil
		}
		return result, nil
	}
}
