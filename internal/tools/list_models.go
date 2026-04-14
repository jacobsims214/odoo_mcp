package tools

import (
	"context"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// listModelsTool returns the list_models tool definition.
func listModelsTool() mcp.Tool {
	return mcp.NewTool("odoo_list_models",
		mcp.WithDescription(`List all Odoo models available on this server. Use this to discover what models are installed before searching or reading records.`),
		mcp.WithString("filter",
			mcp.Description(`Substring filter on model name (e.g. "avware" to find all avware.* models). If omitted, returns all models.`),
			mcp.DefaultString(""),
		),
	)
}

// makeListModelsHandler returns the handler for list_models.
func makeListModelsHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs
		filter := request.GetString("filter", "")

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			return toolError(err.Error()), nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		models, err := client.ListModels(ctx)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_list_models", Method: "list_models",
		}, start, err)

		if err != nil {
			return toolErrorf("list_models failed: %s", err), nil
		}

		// Filter results if filter is provided
		var filtered []interface{}
		if filter != "" {
			for _, model := range models {
				if strings.Contains(model.Name, filter) {
					filtered = append(filtered, model)
				}
			}
		} else {
			for _, model := range models {
				filtered = append(filtered, model)
			}
		}

		// 5. Return JSON result
		result, err := mcp.NewToolResultJSON(filtered)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil
		}
		return result, nil
	}
}
