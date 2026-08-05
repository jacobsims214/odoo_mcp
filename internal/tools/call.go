package tools

import (
	"context"
	"encoding/json"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// CallInput is the typed input for odoo_call.
type CallInput struct {
	Model  string `json:"model" jsonschema:"Odoo model technical name"`
	Method string `json:"method" jsonschema:"Method name to call, e.g. 'action_confirm', 'message_post', 'name_search'"`
	IDs    string `json:"ids,omitempty" jsonschema:"JSON array of record IDs. Omit or use [] for class methods like name_search, default_get, create."`
	Kwargs string `json:"kwargs,omitempty" jsonschema:"JSON object of named keyword arguments to pass to the method. Omit if the method takes no arguments."`
}

// callTool returns the odoo_call tool definition.
func callTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "odoo_call",
		Description: `Call any method on any Odoo model. This is the universal tool for:
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
- Get default values: model="sale.order", method="default_get", kwargs='{"fields_list":["partner_id","pricelist_id"]}'`,
	}
}

// makeCallHandler returns the handler for odoo_call.
func makeCallHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, CallInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input CallInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)
		client := odooClient(ctx, deps)

		// Parse ids (optional, defaults to [])
		idsStr := input.IDs
		if idsStr == "" {
			idsStr = "[]"
		}
		ids, err := parseIDs(idsStr)
		if err != nil {
			return toolErrorf("invalid ids: %v", err), nil, nil
		}

		// Parse kwargs (optional, defaults to {})
		var kwargs map[string]interface{}
		kwargsStr := input.Kwargs
		if kwargsStr == "" {
			kwargsStr = "{}"
		}
		if kwargsStr != "" && kwargsStr != "{}" {
			if err := json.Unmarshal([]byte(kwargsStr), &kwargs); err != nil {
				return toolErrorf("invalid kwargs: %v", err), nil, nil
			}
		}
		if kwargs == nil {
			kwargs = make(map[string]interface{})
		}

		// CheckRate
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     input.Model,
				Method:    input.Method,
				IDs:       ids,
			}, start, err)
			return toolError(err.Error()), nil, nil
		}

		// CheckModel
		if err := deps.Guardrails.CheckModel(input.Model); err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     input.Model,
				Method:    input.Method,
				IDs:       ids,
			}, start, err)
			return toolErrorf("model blocked: %v", err), nil, nil
		}

		// CheckWrite for non-read-only methods (conservative default: check for unknown methods)
		if !readOnlyMethods[input.Method] {
			if err := deps.Guardrails.CheckWrite(); err != nil {
				auditResult(ctx, deps, audit.Entry{
					SessionID: sessID,
					Tool:      "odoo_call",
					Model:     input.Model,
					Method:    input.Method,
					IDs:       ids,
				}, start, err)
				return toolErrorf("write not allowed: %v", err), nil, nil
			}
		}

		// Call the method
		result, err := client.Call(ctx, input.Model, input.Method, ids, kwargs)
		if err != nil {
			auditResult(ctx, deps, audit.Entry{
				SessionID: sessID,
				Tool:      "odoo_call",
				Model:     input.Model,
				Method:    input.Method,
				IDs:       ids,
			}, start, err)
			return toolErrorf("call failed: %v", err), nil, nil
		}

		// Audit success
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID,
			Tool:      "odoo_call",
			Model:     input.Model,
			Method:    input.Method,
			IDs:       ids,
		}, start, nil)

		// Return the raw JSON result as text
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(result)}},
		}, nil, nil
	}
}
