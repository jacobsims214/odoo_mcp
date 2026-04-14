package tools

import (
	"context"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// serverInfoTool returns the server_info tool definition.
func serverInfoTool() mcp.Tool {
	return mcp.NewTool("odoo_get_server_info",
		mcp.WithDescription(`Get Odoo server version and current authenticated user information. Use this to verify connectivity and confirm which user the MCP server is acting as.`),
	)
}

// makeServerInfoHandler returns the handler for server_info.
func makeServerInfoHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs (none)

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			return toolError(err.Error()), nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		serverInfo, err := client.ServerInfo(ctx)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_get_server_info", Method: "server_info",
		}, start, err)

		if err != nil {
			return toolErrorf("server_info failed: %s", err), nil
		}

		// 5. Return JSON result
		result, err := mcp.NewToolResultJSON(serverInfo)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil
		}
		return result, nil
	}
}
