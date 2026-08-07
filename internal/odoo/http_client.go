package odoo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPClient implements OdooClient using the Odoo 19 JSON-2 API.
type HTTPClient struct {
	baseURL    string // e.g. "http://localhost:8069"
	database   string // X-Odoo-Database header value
	apiKey     string // Bearer token
	httpClient *http.Client
	userAgent  string
}

// NewHTTPClient creates a new Odoo JSON-2 HTTP client.
func NewHTTPClient(baseURL, database, apiKey string, timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		baseURL:  strings.TrimRight(baseURL, "/"),
		database: database,
		apiKey:   apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		userAgent: "odoo-mcp-server/1.0.0",
	}
}

// call executes POST /json/2/{model}/{method} and decodes the response into result.
// result must be a pointer. If result is nil, the response body is discarded.
func (c *HTTPClient) call(ctx context.Context, model, method string, body map[string]interface{}, result interface{}) error {
	url := fmt.Sprintf("%s/json/2/%s/%s", c.baseURL, model, method)

	// Use a buffer + encoder with HTML escaping disabled.
	// Go's json.Marshal escapes <, >, & by default which corrupts HTML content
	// in fields like message_post body, brief_description, etc.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(body); err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("User-Agent", c.userAgent)
	if c.database != "" {
		req.Header.Set("X-Odoo-Database", c.database)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("http request to %s: %w", url, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var odooErr ErrorResponse
		if jsonErr := json.Unmarshal(respBody, &odooErr); jsonErr == nil {
			return ClassifyHTTPError(resp.StatusCode, &odooErr)
		}
		return ClassifyHTTPError(resp.StatusCode, nil)
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// SearchRead searches and reads records in a single atomic call.
func (c *HTTPClient) SearchRead(ctx context.Context, model string, domain Domain, fields []string, opts SearchOpts) ([]Record, error) {
	body := map[string]interface{}{
		"domain": domain,
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}
	if opts.Limit > 0 {
		body["limit"] = opts.Limit
	}
	if opts.Offset > 0 {
		body["offset"] = opts.Offset
	}
	if opts.Order != "" {
		body["order"] = opts.Order
	}
	if opts.Context != nil {
		body["context"] = opts.Context
	}

	var result []Record
	if err := c.call(ctx, model, "search_read", body, &result); err != nil {
		return nil, fmt.Errorf("search_read %s: %w", model, err)
	}
	return result, nil
}

// Search returns only the IDs of matching records.
func (c *HTTPClient) Search(ctx context.Context, model string, domain Domain, opts SearchOpts) ([]int64, error) {
	body := map[string]interface{}{
		"domain": domain,
	}
	if opts.Limit > 0 {
		body["limit"] = opts.Limit
	}
	if opts.Offset > 0 {
		body["offset"] = opts.Offset
	}
	if opts.Order != "" {
		body["order"] = opts.Order
	}
	if opts.Context != nil {
		body["context"] = opts.Context
	}

	var result []int64
	if err := c.call(ctx, model, "search", body, &result); err != nil {
		return nil, fmt.Errorf("search %s: %w", model, err)
	}
	return result, nil
}

// Read reads specific fields on known record IDs.
func (c *HTTPClient) Read(ctx context.Context, model string, ids []int64, fields []string) ([]Record, error) {
	body := map[string]interface{}{
		"ids": ids,
	}
	if len(fields) > 0 {
		body["fields"] = fields
	}

	var result []Record
	if err := c.call(ctx, model, "read", body, &result); err != nil {
		return nil, fmt.Errorf("read %s: %w", model, err)
	}
	return result, nil
}

// Create creates a new record and returns its ID.
// Odoo's @api.model_create_multi expects vals_list (a list of value dicts),
// not vals (a single dict). The response can be a single ID or a list of IDs.
func (c *HTTPClient) Create(ctx context.Context, model string, values map[string]interface{}) (int64, error) {
	body := map[string]interface{}{
		"vals_list": []interface{}{values},
	}

	var result json.RawMessage
	if err := c.call(ctx, model, "create", body, &result); err != nil {
		return 0, fmt.Errorf("create %s: %w", model, err)
	}

	// The response can be a single ID or a list of IDs.
	// Try as list first, then as single int.
	var ids []int64
	if err := json.Unmarshal(result, &ids); err == nil && len(ids) > 0 {
		return ids[0], nil
	}
	var id int64
	if err := json.Unmarshal(result, &id); err == nil {
		return id, nil
	}
	return 0, fmt.Errorf("unexpected create response: %s", string(result))
}

// Write updates records. Returns nil on success.
func (c *HTTPClient) Write(ctx context.Context, model string, ids []int64, values map[string]interface{}) error {
	body := map[string]interface{}{
		"ids":    ids,
		"values": values,
	}

	// Response is bool, but we discard it
	if err := c.call(ctx, model, "write", body, nil); err != nil {
		return fmt.Errorf("write %s: %w", model, err)
	}
	return nil
}

// Unlink deletes records. Returns nil on success.
func (c *HTTPClient) Unlink(ctx context.Context, model string, ids []int64) error {
	body := map[string]interface{}{
		"ids": ids,
	}

	// Response is bool, but we discard it
	if err := c.call(ctx, model, "unlink", body, nil); err != nil {
		return fmt.Errorf("unlink %s: %w", model, err)
	}
	return nil
}

// Call calls any method on any model with named kwargs.
// ids may be nil for @api.model methods.
func (c *HTTPClient) Call(ctx context.Context, model string, method string, ids []int64, kwargs map[string]interface{}) (json.RawMessage, error) {
	body := make(map[string]interface{})
	if ids != nil && len(ids) > 0 {
		body["ids"] = ids
	}
	// Merge kwargs into body
	for k, v := range kwargs {
		body[k] = v
	}

	var result json.RawMessage
	if err := c.call(ctx, model, method, body, &result); err != nil {
		return nil, fmt.Errorf("call %s/%s: %w", model, method, err)
	}
	return result, nil
}

// FieldsGet returns field definitions for a model.
func (c *HTTPClient) FieldsGet(ctx context.Context, model string, attributes []string) (FieldsGetResult, error) {
	body := make(map[string]interface{})
	if len(attributes) > 0 {
		body["attributes"] = attributes
	}

	result := make(FieldsGetResult)
	if err := c.call(ctx, model, "fields_get", body, &result); err != nil {
		return nil, fmt.Errorf("fields_get %s: %w", model, err)
	}
	return result, nil
}

// ListModels returns all available model names on the Odoo instance.
func (c *HTTPClient) ListModels(ctx context.Context) ([]ModelInfo, error) {
	// Call ir.model/search_read with specific fields
	body := map[string]interface{}{
		"domain": json.RawMessage(`[]`),
		"fields": []string{"name", "model", "info", "transient"},
	}

	// The response from ir.model/search_read has fields: id, name (human label), model (technical name), info, transient
	var rawRecords []map[string]interface{}
	if err := c.call(ctx, "ir.model", "search_read", body, &rawRecords); err != nil {
		return nil, fmt.Errorf("list_models: %w", err)
	}

	// Map the response to ModelInfo
	result := make([]ModelInfo, len(rawRecords))
	for i, rec := range rawRecords {
		result[i] = ModelInfo{
			ID:          int64(rec["id"].(float64)),
			Name:        rec["model"].(string),
			Description: rec["name"].(string),
		}
		if transient, ok := rec["transient"].(bool); ok {
			result[i].Transient = transient
		}
	}
	return result, nil
}

// ServerInfo returns version and current user information.
func (c *HTTPClient) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	// Get version from GET /web/version
	// For now, we'll call res.users/context_get to get user info
	// and we'll need to handle version separately

	// Call res.users/context_get to get current user context
	body := make(map[string]interface{})
	var contextResp map[string]interface{}
	if err := c.call(ctx, "res.users", "context_get", body, &contextResp); err != nil {
		return nil, fmt.Errorf("server_info: %w", err)
	}

	// Extract user info from context
	info := &ServerInfo{}

	// uid is typically in the context
	if uid, ok := contextResp["uid"].(float64); ok {
		info.UID = int64(uid)
	}

	// Get user details by reading the current user
	if info.UID > 0 {
		userRecords, err := c.Read(ctx, "res.users", []int64{info.UID}, []string{"login", "name"})
		if err == nil && len(userRecords) > 0 {
			if login, ok := userRecords[0]["login"].(string); ok {
				info.Login = login
			}
			if name, ok := userRecords[0]["name"].(string); ok {
				info.Name = name
			}
		}
	}

	// Version would typically come from a separate endpoint or config
	// For now, we'll set a placeholder
	info.Version = "19.0"

	return info, nil
}

// Verify that HTTPClient implements OdooClient
var _ OdooClient = (*HTTPClient)(nil)
