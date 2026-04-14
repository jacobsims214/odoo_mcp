package odoo

import (
	"encoding/json"
	"testing"
)

func TestCallRequestMarshalJSON(t *testing.T) {
	req := CallRequest{
		IDs: []int64{1, 2, 3},
		Context: map[string]interface{}{
			"lang": "en_US",
		},
		Params: map[string]interface{}{
			"domain": []interface{}{[]interface{}{"active", "=", true}},
			"fields": []string{"name", "email"},
			"limit":  10,
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("Error marshaling: %v", err)
	}

	// Verify it's valid JSON
	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	if err != nil {
		t.Fatalf("Error unmarshaling: %v", err)
	}

	// Check that all fields are present at top level
	if _, ok := result["ids"]; !ok {
		t.Error("ids not found in marshaled JSON")
	}
	if _, ok := result["context"]; !ok {
		t.Error("context not found in marshaled JSON")
	}
	if _, ok := result["domain"]; !ok {
		t.Error("domain not found in marshaled JSON")
	}
	if _, ok := result["fields"]; !ok {
		t.Error("fields not found in marshaled JSON")
	}
	if _, ok := result["limit"]; !ok {
		t.Error("limit not found in marshaled JSON")
	}
}

func TestErrorResponseError(t *testing.T) {
	errResp := &ErrorResponse{
		Name:    "werkzeug.exceptions.Unauthorized",
		Message: "Invalid apikey",
	}
	
	errStr := errResp.Error()
	expected := "odoo error werkzeug.exceptions.Unauthorized: Invalid apikey"
	if errStr != expected {
		t.Errorf("Expected %q, got %q", expected, errStr)
	}
}
