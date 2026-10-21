package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

IMPORTANT — limit defaults to 10. If you are COUNTING records, pass an explicit
high limit or limit=0 (no limit). A result of exactly N rows when you asked for
a limit of N usually means there are more; this tool appends an explicit
truncation notice when that happens, so read the whole response, not just the
JSON array.

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

		content := []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}}

		// 6. Truncation notice.
		//
		// A bare JSON array gives the caller no way to tell "this is everything"
		// from "this is the first `limit` of many" — and the default limit is 10,
		// so an agent that omits `limit` and counts the rows it got back silently
		// undercounts. That failure mode produced repeatedly wrong audience
		// numbers before this check existed, each one plausible enough to ship.
		//
		// Only when the page came back exactly full, since that is the only case
		// that is ambiguous. Costs one search_count on that path and nothing
		// otherwise. A failing count is not an error: the records are valid and
		// the caller still gets them, just without the exact total.
		if limit > 0 && len(records) == limit {
			if total, ok := searchCount(ctx, client, input.Model, domain); ok && total > len(records) {
				content = append(content, &mcp.TextContent{Text: fmt.Sprintf(
					"\n\nTRUNCATED: returned %d of %d matching records (limit=%d, offset=%d). "+
						"The array above is NOT the full result set. Re-run with a higher limit, "+
						"limit=0 for all records, or page with offset.",
					len(records), total, limit, offset)})
			}
		}

		return &mcp.CallToolResult{Content: content}, nil, nil
	}
}

// searchCount returns the true number of records matching domain.
//
// Used only to turn an ambiguous full page into an explicit truncation notice,
// so the caller is never left guessing whether it saw everything. Returns
// ok=false on any failure — a missing total must never fail a read that
// actually succeeded.
func searchCount(ctx context.Context, client odoo.OdooClient, model string, domain odoo.Domain) (int, bool) {
	raw, err := client.Call(ctx, model, "search_count", nil, map[string]interface{}{
		"domain": json.RawMessage(domain),
	})
	if err != nil {
		return 0, false
	}
	var total int
	if err := json.Unmarshal(raw, &total); err != nil {
		return 0, false
	}
	return total, true
}
