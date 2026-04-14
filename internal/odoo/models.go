package odoo

import (
	"encoding/json"
	"fmt"
)

// CallRequest is the body sent to POST /json/2/{model}/{method}
type CallRequest struct {
	IDs     []int64                `json:"ids,omitempty"`
	Context map[string]interface{} `json:"context,omitempty"`
	// Additional named params are merged at the top level
	// We use a map for flexibility
	Params map[string]interface{} `json:"-"`
}

// MarshalJSON merges Params into the top-level JSON object alongside IDs and Context.
func (r CallRequest) MarshalJSON() ([]byte, error) {
	// Create a combined map with IDs and Context first
	combined := make(map[string]interface{})

	// Add IDs if present
	if len(r.IDs) > 0 {
		combined["ids"] = r.IDs
	}

	// Add Context if present
	if len(r.Context) > 0 {
		combined["context"] = r.Context
	}

	// Merge all Params into the top level
	for k, v := range r.Params {
		combined[k] = v
	}

	return json.Marshal(combined)
}

// ErrorResponse is the error body returned by Odoo on 4xx/5xx
type ErrorResponse struct {
	Name      string        `json:"name"`
	Message   string        `json:"message"`
	Arguments []interface{} `json:"arguments"`
	Context   interface{}   `json:"context"`
	Debug     string        `json:"debug"`
}

// Error implements the error interface for ErrorResponse
func (e *ErrorResponse) Error() string {
	return fmt.Sprintf("odoo error %s: %s", e.Name, e.Message)
}

// Record is a generic Odoo record (map of field name → value)
type Record map[string]interface{}

// FieldDef describes a single field from fields_get
type FieldDef struct {
	String    string          `json:"string"`
	Type      string          `json:"type"`
	Required  bool            `json:"required"`
	Readonly  bool            `json:"readonly"`
	Relation  string          `json:"relation,omitempty"`
	Help      string          `json:"help,omitempty"`
	Selection [][]interface{} `json:"selection,omitempty"`
}

// FieldsGetResult maps field name → FieldDef
type FieldsGetResult map[string]FieldDef

// Domain is an Odoo domain filter (array of triples or prefix operators)
// We store it as raw JSON to pass through without re-encoding
type Domain = json.RawMessage

// ServerInfo holds version and user context
type ServerInfo struct {
	Version string `json:"version"`
	UID     int64  `json:"uid"`
	Login   string `json:"login"`
	Name    string `json:"name"`
}
