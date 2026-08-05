package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// serverInfoTool returns the server_info tool definition.
func serverInfoTool() *mcp.Tool {
	return &mcp.Tool{
		Name:        "odoo_get_server_info",
		Description: `Get Odoo server version and current authenticated user information. Use this to verify connectivity and confirm which user the MCP server is acting as.`,
	}
}

// makeServerInfoHandler returns the handler for server_info.
func makeServerInfoHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			return toolError(err.Error()), nil, nil
		}

		// 3. Call Odoo
		client := odooClient(ctx, deps)
		serverInfo, err := client.ServerInfo(ctx)

		// 4. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID, Tool: "odoo_get_server_info", Method: "server_info",
		}, start, err)

		if err != nil {
			return toolErrorf("server_info failed: %s", err), nil, nil
		}

		// 5. Return JSON result
		jsonBytes, err := json.Marshal(serverInfo)
		if err != nil {
			return toolErrorf("encode result: %s", err), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(jsonBytes)}},
		}, nil, nil
	}
}
