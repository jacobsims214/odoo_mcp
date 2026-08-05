package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// ReadInput is the typed input for odoo_read.
type ReadInput struct {
	Model  string `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order', 'avware.units'"`
	IDs    string `json:"ids" jsonschema:"JSON array of integer IDs, e.g. [1, 2, 3]"`
	Fields string `json:"fields,omitempty" jsonschema:"JSON array of field names to return. Default: []"`
}

// readTool returns the read tool definition.
func readTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_read",
		Description: `Read specific fields on Odoo records by their IDs. Use when you already know the record IDs.`,
	}
}

// makeReadHandler returns the handler for read.
func makeReadHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, ReadInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ReadInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		// 1. Parse inputs
		ids, err := parseIDs(input.IDs)
		if err != nil {
			return toolErrorf("invalid ids: %s", err), nil, nil
		}

		fieldsStr := input.Fields
		if fieldsStr == "" {
			fieldsStr = "[]"
		}
		fields, err := parseStringArray(fieldsStr)
		if err != nil {
			return toolErrorf("invalid fields: %s", err), nil, nil
		}

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			return toolError(err.Error()), nil, nil
		}
		if err := deps.Guardrails.CheckModel(input.Model); err != nil {
			return toolError(err.Error()), nil, nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		records, err := client.Read(ctx, input.Model, ids, fields)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_read", Model: input.Model, Method: "read",
		}, start, err)

		if err != nil {
			return toolErrorf("read failed: %s", err), nil, nil
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
