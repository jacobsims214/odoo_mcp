package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/odoo"
)

// ListModelsInput is the typed input for odoo_list_models.
type ListModelsInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Substring filter on model name (e.g. 'avware' to find all avware.* models). If omitted, returns all models."`
}

// listModelsTool returns the list_models tool definition.
func listModelsTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_list_models",
		Description: "List all Odoo models available on this server. Use this to discover what models are installed before searching or reading records.",
	}
}

// makeListModelsHandler returns the handler for list_models.
func makeListModelsHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, ListModelsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListModelsInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		// 1. Guardrails
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			return toolError(err.Error()), nil, nil
		}

		// 2. Call Odoo
		client := odooClient(ctx, deps)
		models, err := client.ListModels(ctx)

		// 3. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_list_models", Method: "list_models",
		}, start, err)

		if err != nil {
			return toolErrorf("list_models failed: %s", err), nil, nil
		}

		// 4. Filter by name if a filter string was provided
		if input.Filter != "" {
			var filtered []odoo.ModelInfo
			for _, m := range models {
				if strings.Contains(m.Name, input.Filter) {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}

		// 5. Return JSON result
		jsonBytes, err := json.Marshal(models)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil, nil
	}
}
