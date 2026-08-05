package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/audit"
)

// MessagePostInput is the typed input for odoo_message_post.
type MessagePostInput struct {
	Model        string  `json:"model" jsonschema:"Odoo model technical name, e.g. 'sale.order', 'account.move', 'res.partner'"`
	RecordID     float64 `json:"record_id" jsonschema:"ID of the record to post the message on"`
	Body         string  `json:"body" jsonschema:"HTML body of the message. Use proper HTML tags like <p>, <b>, <ul>, <li>."`
	MessageType  string  `json:"message_type,omitempty" jsonschema:"Type of message: 'comment' (default) or 'note'"`
	SubtypeXmlid string  `json:"subtype_xmlid,omitempty" jsonschema:"Subtype XML ID: 'mail.mt_comment' (default) or 'mail.mt_note'"`
}

// messagePostTool returns the odoo_message_post tool definition.
func messagePostTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "odoo_message_post",
		Description: `Post a message to an Odoo record's chatter (activity log).
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
  - "mail.mt_note"     — internal note subtype`,
	}
}

// makeMessagePostHandler returns the handler for odoo_message_post.
func makeMessagePostHandler(deps Deps) func(context.Context, *mcp.CallToolRequest, MessagePostInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input MessagePostInput) (*mcp.CallToolResult, any, error) {
		start := time.Now()
		sessID := sessionID(req.Session)

		recordID := int64(input.RecordID)
		if recordID == 0 {
			return toolError("record_id is required and must be a non-zero integer"), nil, nil
		}

		if input.Body == "" {
			return toolError("body cannot be empty"), nil, nil
		}

		messageType := input.MessageType
		if messageType == "" {
			messageType = "comment"
		}
		subtypeXmlid := input.SubtypeXmlid
		if subtypeXmlid == "" {
			subtypeXmlid = "mail.mt_comment"
		}

		// 2. Guardrails
		if err := deps.Guardrails.CheckRate(ctx, sessID); err != nil {
			return toolError(err.Error()), nil, nil
		}
		if err := deps.Guardrails.CheckModel(input.Model); err != nil {
			return toolError(err.Error()), nil, nil
		}
		if err := deps.Guardrails.CheckWrite(); err != nil {
			return toolError(err.Error()), nil, nil
		}

		// 3. Resolve subtype_id from xmlid
		client := odooClient(ctx, deps)
		subtypeID, err := resolveSubtypeID(ctx, client, subtypeXmlid)
		if err != nil {
			subtypeID = 1
		}

		// 4. Create mail.message directly — bypasses message_post HTML sanitizer.
		vals := map[string]interface{}{
			"body":         input.Body,
			"message_type": messageType,
			"model":        input.Model,
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
			Model:     input.Model,
			Method:    "message_post",
			IDs:       []int64{recordID},
		}, start, callErr)

		if callErr != nil {
			return toolErrorf("message_post failed: %s", callErr), nil, nil
		}

		// Parse the returned message ID(s)
		var msgIDs []int64
		if jsonErr := json.Unmarshal(result, &msgIDs); jsonErr == nil && len(msgIDs) > 0 {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
					`{"success": true, "message_id": %d, "model": "%s", "record_id": %d}`,
					msgIDs[0], input.Model, recordID,
				)}},
			}, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(
				`{"success": true, "model": "%s", "record_id": %d}`,
				input.Model, recordID,
			)}},
		}, nil, nil
	}
}

// resolveSubtypeID looks up the integer ID of a mail.message.subtype by its XML ID.
func resolveSubtypeID(ctx context.Context, client interface {
	Call(ctx context.Context, model, method string, ids []int64, kwargs map[string]interface{}) (json.RawMessage, error)
}, xmlid string) (int64, error) {
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
