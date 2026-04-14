package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/odoo"
)

// searchReadTool returns the search_read tool definition.
func searchReadTool() mcp.Tool {
	return mcp.NewTool("odoo_search_read",
		mcp.WithDescription(`Search and read Odoo records in a single atomic operation.
Returns records matching the domain with the requested fields.
Prefer this over separate search + read calls — it runs in one SQL transaction.

Examples:
- Find all active companies: model="res.partner", domain='[["is_company","=",true],["active","=",true]]'
- Get sale orders: model="sale.order", domain='[["state","in",["sale","done"]]]', fields='["name","partner_id","amount_total"]'
- Search all records: model="res.partner", domain="[]"`),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'res.partner', 'sale.order', 'avware.units'"),
		),
		mcp.WithString("domain",
			mcp.Description(`Odoo domain filter as a JSON array of triples. Examples:
- All records: []
- Active companies: [["is_company","=",true],["active","=",true]]
- By name: [["name","ilike","deco"]]
- Multiple conditions (AND): [["state","=","sale"],["amount_total",">",1000]]
- OR condition: ["|",["state","=","draft"],["state","=","cancel"]]`),
			mcp.DefaultString("[]"),
		),
		mcp.WithString("fields",
			mcp.Description(`JSON array of field names to return. Example: ["name","email","phone"].
If omitted, returns all fields (may be slow for large models).`),
			mcp.DefaultString("[]"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Maximum number of records to return. Default: 10. Max recommended: 100."),
			mcp.DefaultNumber(10),
		),
		mcp.WithNumber("offset",
			mcp.Description("Number of records to skip (for pagination). Default: 0."),
			mcp.DefaultNumber(0),
		),
		mcp.WithString("order",
			mcp.Description(`Sort order. Example: "name asc" or "create_date desc, id asc". Default: Odoo default order.`),
			mcp.DefaultString(""),
		),
	)
}

// makeSearchReadHandler returns the handler for search_read.
func makeSearchReadHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs
		model, err := request.RequireString("model")
		if err != nil {
			return toolError(err.Error()), nil
		}

		domainStr := request.GetString("domain", "[]")
		domain, err := parseDomain(domainStr)
		if err != nil {
			return toolErrorf("invalid domain: %s", err), nil
		}

		fieldsStr := request.GetString("fields", "[]")
		fields, err := parseStringArray(fieldsStr)
		if err != nil {
			return toolErrorf("invalid fields: %s", err), nil
		}

		limit := request.GetInt("limit", 10)
		offset := request.GetInt("offset", 0)
		order := request.GetString("order", "")

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			return toolError(err.Error()), nil
		}
		if err := deps.Guardrails.CheckModel(model); err != nil {
			return toolError(err.Error()), nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		records, err := client.SearchRead(ctx, model, domain, fields, odoo.SearchOpts{
			Limit: limit, Offset: offset, Order: order,
		})

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_search_read", Model: model, Method: "search_read",
		}, start, err)

		if err != nil {
			return toolErrorf("search_read failed: %s", err), nil
		}

		// 5. Return JSON result
		result, err := mcp.NewToolResultJSON(records)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil
		}
		return result, nil
	}
}
