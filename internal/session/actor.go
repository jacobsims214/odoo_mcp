package session

import (
	"github.com/simstech/odoo-mcp/internal/odoo"
)

// Session holds the per-session state for an MCP connection.
// For stdio transport, there is exactly one session.
// For HTTP transport, each connected client gets its own session.
type Session struct {
	ID        string
	Client    odoo.OdooClient
	UserLogin string // populated after the first successful Odoo call
}

// NewSession creates a new session with the given ID and Odoo client.
func NewSession(id string, client odoo.OdooClient) *Session {
	return &Session{ID: id, Client: client}
}
