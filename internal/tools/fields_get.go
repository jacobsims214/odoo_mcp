package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// FieldsGetInput is the typed input for odoo_fields_get.
type FieldsGetInput struct {
	Model      string `json:"model" jsonschema:"Odoo model technical name, e.g. 'res.partner', 'sale.order'"`
	Attributes string `json:"attributes,omitempty" jsonschema:"JSON array of field attributes to return. Default: [\"string\",\"type\",\"required\",\"readonly\",\"relation\",\"help\",\"selection\"]"`
}

// fieldsGetTool returns the fields_get tool definition.
func fieldsGetTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_fields_get",
		Description: `Introspect an Odoo model's fields — names, types, labels, relations, and constraints. Use this before operating on an unfamiliar model to understand its structure.`,
	}
}

// makeFieldsGetHandler returns the handler for fields_get.
func makeFieldsGetHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, FieldsGetInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input FieldsGetInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		// 1. Parse inputs
		attributesStr := input.Attributes
		if attributesStr == "" {
			attributesStr = `["string","type","required","readonly","relation","help","selection"]`
		}
		attributes, err := parseStringArray(attributesStr)
		if err != nil {
			return toolErrorf("invalid attributes: %s", err), nil, nil
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
		fieldsResult, err := client.FieldsGet(ctx, input.Model, attributes)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_fields_get", Model: input.Model, Method: "fields_get",
		}, start, err)

		if err != nil {
			return toolErrorf("fields_get failed: %s", err), nil, nil
		}

		// 5. Return JSON result
		jsonBytes, err := json.Marshal(fieldsResult)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil, nil
	}
}
