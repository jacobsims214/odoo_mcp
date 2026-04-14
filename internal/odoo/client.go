package odoo

import (
	"context"
	"encoding/json"
)

// OdooClient is the port for all Odoo JSON-2 API operations.
// The HTTP adapter implements this interface. Tests use a mock.
type OdooClient interface {
	// SearchRead searches and reads records in a single atomic call.
	// domain is a JSON-encoded Odoo domain (e.g., `[["active","=",true]]` or `[]`)
	// fields is the list of field names to return (nil = all fields)
	SearchRead(ctx context.Context, model string, domain Domain, fields []string, opts SearchOpts) ([]Record, error)

	// Search returns only the IDs of matching records.
	Search(ctx context.Context, model string, domain Domain, opts SearchOpts) ([]int64, error)

	// Read reads specific fields on known record IDs.
	Read(ctx context.Context, model string, ids []int64, fields []string) ([]Record, error)

	// Create creates a new record and returns its ID.
	Create(ctx context.Context, model string, values map[string]interface{}) (int64, error)

	// Write updates records. Returns true on success.
	Write(ctx context.Context, model string, ids []int64, values map[string]interface{}) error

	// Unlink deletes records. Returns true on success.
	Unlink(ctx context.Context, model string, ids []int64) error

	// Call calls any method on any model with named kwargs.
	// ids may be nil for @api.model methods.
	Call(ctx context.Context, model string, method string, ids []int64, kwargs map[string]interface{}) (json.RawMessage, error)

	// FieldsGet returns field definitions for a model.
	// attributes filters which field attributes to return (nil = all).
	FieldsGet(ctx context.Context, model string, attributes []string) (FieldsGetResult, error)

	// ListModels returns all available model names on the Odoo instance.
	ListModels(ctx context.Context) ([]ModelInfo, error)

	// ServerInfo returns version and current user information.
	ServerInfo(ctx context.Context) (*ServerInfo, error)
}

// SearchOpts are optional parameters for search/search_read operations.
type SearchOpts struct {
	Limit   int // 0 = use server default (usually 80)
	Offset  int
	Order   string // e.g. "name asc, id desc"
	Context map[string]interface{}
}

// ModelInfo is a summary of an Odoo model from ir.model
type ModelInfo struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`        // technical name, e.g. "res.partner"
	Description string `json:"description"` // human label, e.g. "Contact"
	Transient   bool   `json:"transient"`
}
