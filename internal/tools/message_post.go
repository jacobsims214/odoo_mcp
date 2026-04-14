package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// messagePostTool returns the odoo_message_post tool definition.
// This tool posts a properly-formatted HTML message to any Odoo record's chatter.
// It uses mail.message/create directly (bypassing message_post's HTML sanitizer)
// so that rich HTML — bold, lists, links — renders correctly in the Odoo UI.
func messagePostTool() mcp.Tool {
	return mcp.NewTool("odoo_message_post",
		mcp.WithDescription(`Post a message to an Odoo record's chatter (activity log).
Renders rich HTML correctly — bold, italic, lists, links all display properly in the Odoo UI.

Use this instead of odoo_call with message_post — this tool handles HTML formatting correctly.

Examples:
- Log a status update on a unit:
    model="avware.units", record_id=1, body="<p><strong>Unit listed on website.</strong></p><p>All prep tasks complete.</p>"

- Post a note on a sale order:
    model="sale.order", record_id=42, body="<p>Customer confirmed delivery for <strong>Friday</strong>.</p>"

- Log an action with a list:
    model="project.task", record_id=7, body="<p>Completed the following:</p><ul><li>Photos uploaded</li><li>SEO fields set</li></ul>"

message_type options:
  - "comment"  — visible to followers (default, shows in chatter)
  - "note"     — internal note (yellow background, not sent to followers)

subtype options (subtype_xmlid):
  - "mail.mt_comment"  — standard discussion message (default)
  - "mail.mt_note"     — internal note subtype`),
		mcp.WithString("model",
			mcp.Required(),
			mcp.Description("Odoo model technical name, e.g. 'avware.units', 'sale.order', 'project.task'"),
		),
		mcp.WithNumber("record_id",
			mcp.Required(),
			mcp.Description("ID of the record to post the message on"),
		),
		mcp.WithString("body",
			mcp.Required(),
			mcp.Description(`HTML body of the message. Use standard HTML tags for formatting:
- Bold: <strong>text</strong>
- Italic: <em>text</em>
- Paragraph: <p>text</p>
- Unordered list: <ul><li>item</li></ul>
- Ordered list: <ol><li>item</li></ol>
- Link: <a href="https://example.com">text</a>

Do NOT double-wrap in <p> tags — write the HTML directly.`),
		),
		mcp.WithString("message_type",
			mcp.Description(`Type of message. Options:
- "comment" — visible chatter message, sent to followers (default)
- "note"    — internal note, yellow background, not emailed to followers`),
			mcp.DefaultString("comment"),
		),
		mcp.WithString("subtype_xmlid",
			mcp.Description(`Subtype XML ID. Options:
- "mail.mt_comment" — standard discussion (default)
- "mail.mt_note"    — internal note`),
			mcp.DefaultString("mail.mt_comment"),
		),
	)
}

// makeMessagePostHandler returns the handler for odoo_message_post.
// Uses mail.message/create directly to bypass Odoo's HTML sanitizer in message_post,
// which would escape HTML tags when called via the external JSON-2 API.
func makeMessagePostHandler(deps Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		start := time.Now()
		sessID := sessionID(ctx, deps)

		// 1. Parse inputs
		model, err := req.RequireString("model")
		if err != nil {
			return toolError(err.Error()), nil
		}

		recordID := int64(req.GetFloat("record_id", 0))
		if recordID == 0 {
			return toolError("record_id is required and must be a non-zero integer"), nil
		}

		body, err := req.RequireString("body")
		if err != nil {
			return toolError(err.Error()), nil
		}
		if body == "" {
			return toolError("body cannot be empty"), nil
		}

		messageType := req.GetString("message_type", "comment")
		subtypeXmlid := req.GetString("subtype_xmlid", "mail.mt_comment")

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(sessID); err != nil {
			return toolError(err.Error()), nil
		}
		if err := deps.Guardrails.CheckModel(model); err != nil {
			return toolError(err.Error()), nil
		}
		if err := deps.Guardrails.CheckWrite(); err != nil {
			return toolError(err.Error()), nil
		}

		// 3. Resolve subtype_id from xmlid
		//    We need the integer ID of the subtype for mail.message/create.
		client := odooClient(ctx, deps)
		subtypeID, err := resolveSubtypeID(ctx, client, subtypeXmlid)
		if err != nil {
			// Fall back to subtype_id=1 (mt_comment) if resolution fails
			subtypeID = 1
		}

		// 4. Create mail.message directly — bypasses message_post HTML sanitizer.
		//    This is the correct approach for the JSON-2 external API.
		//    message_post sanitizes HTML as untrusted input; mail.message/create does not.
		vals := map[string]interface{}{
			"body":         body,
			"message_type": messageType,
			"model":        model,
			"res_id":       recordID,
			"subtype_id":   subtypeID,
		}

		result, callErr := client.Call(ctx, "mail.message", "create", nil, map[string]interface{}{
			"vals_list": []interface{}{vals},
		})

		// 5. Audit
		auditResult(ctx, deps, audit.Entry{
			SessionID: sessID,
			Tool:      "odoo_message_post",
			Model:     model,
			Method:    "message_post",
			IDs:       []int64{recordID},
		}, start, callErr)

		if callErr != nil {
			return toolErrorf("message_post failed: %s", callErr), nil
		}

		// Parse the returned message ID(s)
		var msgIDs []int64
		if jsonErr := json.Unmarshal(result, &msgIDs); jsonErr == nil && len(msgIDs) > 0 {
			return mcp.NewToolResultText(fmt.Sprintf(
				`{"success": true, "message_id": %d, "model": "%s", "record_id": %d}`,
				msgIDs[0], model, recordID,
			)), nil
		}

		return mcp.NewToolResultText(fmt.Sprintf(
			`{"success": true, "model": "%s", "record_id": %d}`,
			model, recordID,
		)), nil
	}
}

// resolveSubtypeID looks up the integer ID of a mail.message.subtype by its XML ID.
// e.g. "mail.mt_comment" → 1, "mail.mt_note" → 2
func resolveSubtypeID(ctx context.Context, client interface {
	Call(ctx context.Context, model, method string, ids []int64, kwargs map[string]interface{}) (json.RawMessage, error)
}, xmlid string) (int64, error) {
	// Split "mail.mt_comment" into module="mail", name="mt_comment"
	module := ""
	name := xmlid
	for i := len(xmlid) - 1; i >= 0; i-- {
		if xmlid[i] == '.' {
			module = xmlid[:i]
			name = xmlid[i+1:]
			break
		}
	}
	if module == "" {
		return 1, fmt.Errorf("invalid xmlid format: %s", xmlid)
	}

	result, err := client.Call(ctx, "ir.model.data", "search_read", nil, map[string]interface{}{
		"domain": []interface{}{
			[]interface{}{"module", "=", module},
			[]interface{}{"name", "=", name},
			[]interface{}{"model", "=", "mail.message.subtype"},
		},
		"fields": []string{"res_id"},
		"limit":  1,
	})
	if err != nil {
		return 1, fmt.Errorf("resolve subtype %s: %w", xmlid, err)
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(result, &records); err != nil || len(records) == 0 {
		return 1, fmt.Errorf("subtype %s not found", xmlid)
	}

	if resID, ok := records[0]["res_id"].(float64); ok {
		return int64(resID), nil
	}
	return 1, fmt.Errorf("subtype %s: unexpected res_id type", xmlid)
}
