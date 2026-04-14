package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// readOnlyMethods are methods that never mutate state — exempt from CheckWrite.
var readOnlyMethods = map[string]bool{
	"name_search":     true,
	"default_get":     true,
	"fields_get":      true,
	"fields_view_get": true,
	"read_group":      true,
	"search_count":    true,
	"get_views":       true,
	"onchange":        true,
}

// callTool returns the odoo_call tool definition.
func callTool() mcp.Tool {
	return mcp.NewTool("odoo_call",
		mcp.WithDescription(`Call any method on any Odoo model. This is the universal tool for:
- Business workflow actions: action_confirm, action_post, action_cancel, action_done
- Chatter/messaging: message_post (post to record chatter), message_subscribe
- Name search: name_search (search by display name)
- Custom model methods: any method defined on any Odoo model
- Computed field triggers: any @api.model or @api.multi method

Examples:
- Confirm a sale order: model="sale.order", method="action_confirm", ids="[42]"
- Post to chatter: model="sale.order", method="message_post", ids="[42]", kwargs='{"body":"<p>Order reviewed by agent</p>","message_type":"comment","subtype_xmlid":"mail.mt_comment"}'
- Search by name: model="res.partner", method="name_search", kwargs='{"name":"deco","limit":10}'
- Post invoice: model="account.move", method="action_post", ids="[15]"
- Get default values: model="sale.order", method="default_get", kwargs='{"fields_list":["partner_id","pricelist_id"]}'`),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name"),
		),
		mcp.WithString("method",
			mcp.Required(),
			mcp.Description("Method name to call, e.g. 'action_confirm', 'message_post', 'name_search'"),
		),
		mcp.WithString("ids",
			mcp.Description(`JSON array of record IDs. Required for instance methods (@api.multi).
Omit or use [] for class methods (@api.model) like name_search, default_get, create.`),
			mcp.DefaultString("[]"),
		),
		mcp.WithString("kwargs",
			mcp.Description(`JSON object of named keyword arguments to pass to the method.
Example for message_post: {"body": "<p>Hello</p>", "message_type": "comment", "subtype_xmlid": "mail.mt_comment"}
Example for name_search: {"name": "deco", "limit": 10}
Omit if the method takes no arguments.`),
			mcp.DefaultString("{}"),
		),
	)
}

// makeCallHandler returns the handler for odoo_call.
func makeCallHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)
		client := odooClient(ctx, deps)

		// Extract parameters
		model, err := request.RequireString("model")
		if err != nil {
			return toolError(err.Error()), nil
		}

		method, err := request.RequireString("method")
		if err != nil {
			return toolError(err.Error()), nil
		}

		// Parse ids (optional, defaults to [])
		idsStr := request.GetString("ids", "[]")
		ids, err := parseIDs(idsStr)
		if err != nil {
			return toolErrorf("invalid ids: %v", err), nil
		}

		// Parse kwargs (optional, defaults to {})
		var kwargs map[string]interface{}
		kwargsStr := request.GetString("kwargs", "{}")
		if kwargsStr != "" && kwargsStr != "{}" {
			if err := json.Unmarshal([]byte(kwargsStr), &kwargs); err != nil {
				return toolErrorf("invalid kwargs: %v", err), nil
			}
		}
		if kwargs == nil {
			kwargs = make(map[string]interface{})
		}

		// CheckRate
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     model,
				Method:    method,
				IDs:       ids,
			}, start, err)
			return toolErrorf("rate limit exceeded: %v", err), nil
		}

		// CheckModel
		if err := deps.Guardrails.CheckModel(model); err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     model,
				Method:    method,
				IDs:       ids,
			}, start, err)
			return toolErrorf("model blocked: %v", err), nil
		}

		// CheckWrite for non-read-only methods (conservative default: check for unknown methods)
		if !readOnlyMethods[method] {
			if err := deps.Guardrails.CheckWrite(); err != nil {
				auditResult(ctx, deps, audit.Entry{
					SessionID: sessID,
					Tool:      "odoo_call",
					Model:     model,
					Method:    method,
					IDs:       ids,
				}, start, err)
				return toolErrorf("write not allowed: %v", err), nil
			}
		}

		// Call the method
		result, err := client.Call(ctx, model, method, ids, kwargs)
		if err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     model,
				Method:    method,
				IDs:       ids,
			}, start, err)
			return toolErrorf("call failed: %v", err), nil
		}

		// Audit success
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID,
			Tool:      "odoo_call",
			Model:     model,
			Method:    method,
			IDs:       ids,
		}, start, nil)

		// Return the raw JSON result as text
		return mcp.NewToolResultText(string(result)), nil
	}
}
