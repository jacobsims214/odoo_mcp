package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/odoo"
)

// SearchReadInput is the typed input for odoo_search_read.
type SearchReadInput struct {
	Model  string  `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order', 'avware.units'"`
	Domain string  `json:"domain,omitempty" jsonschema:"Odoo domain filter as a JSON array of triples. Default: []"`
	Fields string  `json:"fields,omitempty" jsonschema:"JSON array of field names to return. Default: []"`
	Limit  *float64 `json:"limit,omitempty" jsonschema:"Maximum number of records to return. Default: 10. Max recommended: 100. Use 0 for no limit (returns all matching records)."`
	Offset int      `json:"offset,omitempty" jsonschema:"Number of records to skip (for pagination). Default: 0."`
	Order  string  `json:"order,omitempty" jsonschema:"Sort order. Example: 'name asc' or 'create_date desc, id asc'. Default: Odoo default order."`
}

// searchReadTool returns the search_read tool definition.
func searchReadTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "odoo_search_read",
		Description: `Search and read Odoo records in a single atomic operation.
Returns records matching the domain with the requested fields.
Prefer this over separate search + read calls — it runs in one SQL transaction.

Examples:
- Find all active companies: model="res.partner", domain='[["is_company","=",true],["active","=",true]]'
- Get sale orders: model="sale.order", domain='[["state","in",["sale","done"]]]', fields='["name","partner_id","amount_total"]'
- Search all records: model="res.partner", domain="[]"`,
	}
}

// makeSearchReadHandler returns the handler for search_read.
func makeSearchReadHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, SearchReadInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchReadInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		// 1. Parse inputs
		domainStr := input.Domain
		if domainStr == "" {
			domainStr = "[]"
		}
		domain, err := parseDomain(domainStr)
		if err != nil {
			return toolErrorf("invalid domain: %s", err), nil, nil
		}

		fieldsStr := input.Fields
		if fieldsStr == "" {
			fieldsStr = "[]"
		}
		fields, err := parseStringArray(fieldsStr)
		if err != nil {
			return toolErrorf("invalid fields: %s", err), nil, nil
		}

		limit := 10
		if input.Limit != nil {
			limit = int(*input.Limit)
		}
		offset := input.Offset

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			return toolError(err.Error()), nil, nil
		}
		if err := deps.Guardrails.CheckModel(input.Model); err != nil {
			return toolError(err.Error()), nil, nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		records, err := client.SearchRead(ctx, input.Model, domain, fields, odoo.SearchOpts{
			Limit: limit, Offset: offset, Order: input.Order,
		})

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_search_read", Model: input.Model, Method: "search_read",
		}, start, err)

		if err != nil {
			return toolErrorf("search_read failed: %s", err), nil, nil
		}

		// 5. Return JSON result
		jsonBytes, err := json.Marshal(records)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil, nil
	}
}
