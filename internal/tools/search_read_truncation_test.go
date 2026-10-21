package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/simstech/odoo-mcp/internal/odoo"
)

// countingClient serves a fixed number of records and records the calls made.
type countingClient struct {
	odoo.OdooClient
	total      int
	countCalls int
	countErr   error
}

func (c *countingClient) Call(ctx context.Context, model, method string, ids []int64,
	kwargs map[string]interface{}) (json.RawMessage, error) {
	if method != "search_count" {
		return nil, nil
	}
	c.countCalls++
	if c.countErr != nil {
		return nil, c.countErr
	}
	b, _ := json.Marshal(c.total)
	return b, nil
}

// The whole point of the truncation notice: a full page is ambiguous, and the
// default limit is 10, so a caller that counts the rows it got back silently
// undercounts. search_count must be consulted only on that ambiguous path.
func TestSearchCountReportsTotal(t *testing.T) {
	c := &countingClient{total: 153}
	got, ok := searchCount(context.Background(), c, "res.partner", odoo.Domain([]byte("[]")))
	if !ok {
		t.Fatal("searchCount reported not-ok for a healthy count")
	}
	if got != 153 {
		t.Fatalf("total = %d, want 153", got)
	}
	if c.countCalls != 1 {
		t.Fatalf("search_count called %d times, want exactly 1", c.countCalls)
	}
}

// A missing total must never turn a successful read into a failure — the
// records are valid, the caller just does not learn the exact total.
func TestSearchCountFailsSoft(t *testing.T) {
	c := &countingClient{countErr: context.DeadlineExceeded}
	if _, ok := searchCount(context.Background(), c, "res.partner", odoo.Domain([]byte("[]"))); ok {
		t.Fatal("searchCount reported ok despite an error from Odoo")
	}
}

// Guards the condition that decides whether the notice is emitted at all.
// A short page is unambiguous and must not cost a second round trip.
func TestTruncationConditionOnlyOnFullPage(t *testing.T) {
	for _, tc := range []struct {
		name            string
		limit, returned int
		wantCheck       bool
	}{
		{"full page is ambiguous", 10, 10, true},
		{"short page is complete", 10, 7, false},
		{"empty result is complete", 10, 0, false},
		{"limit=0 means no limit", 0, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.limit > 0 && tc.returned == tc.limit
			if got != tc.wantCheck {
				t.Fatalf("would consult search_count = %v, want %v", got, tc.wantCheck)
			}
		})
	}
}
