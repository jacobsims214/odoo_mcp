package odoo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestWriteSendsValsKwarg pins the payload key for write.
//
// The request body keys become the Odoo method's named kwargs. Odoo's
// signature is write(self, vals), so the key must be "vals". Sending
// "values" made every update fail with:
//
//	missing a required argument: 'vals'
//
// which surfaced to callers as "write failed" on project.task and
// res.partner alike.
func TestWriteSendsValsKwarg(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("true"))
	}))
	t.Cleanup(srv.Close)

	c := &HTTPClient{baseURL: srv.URL, apiKey: "k", httpClient: srv.Client()}
	err := c.Write(context.Background(), "project.task", []int64{510},
		map[string]interface{}{"stage_id": 323})
	if err != nil {
		t.Fatalf("Write returned error: %v", err)
	}

	if _, ok := got["vals"]; !ok {
		t.Errorf("payload must carry the key \"vals\"; got keys %v", keys(got))
	}
	if _, ok := got["values"]; ok {
		t.Errorf("payload must NOT carry \"values\" — Odoo write takes vals; got keys %v", keys(got))
	}
	vals, _ := got["vals"].(map[string]interface{})
	if vals == nil || vals["stage_id"] == nil {
		t.Errorf("vals did not carry the field values; got %#v", got["vals"])
	}
	if _, ok := got["ids"]; !ok {
		t.Errorf("payload must carry \"ids\"; got keys %v", keys(got))
	}
}

func keys(m map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
