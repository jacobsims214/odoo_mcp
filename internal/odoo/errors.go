package odoo

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Sentinel errors for common Odoo error conditions
var (
	ErrAccessDenied   = errors.New("odoo: access denied")
	ErrNotFound       = errors.New("odoo: record not found")
	ErrValidation     = errors.New("odoo: validation error")
	ErrModelBlocked   = errors.New("odoo: model is blocked by guardrails")
	ErrReadOnly       = errors.New("odoo: server is in read-only mode")
	ErrRateLimit      = errors.New("odoo: rate limit exceeded")
	ErrAuthentication = errors.New("odoo: authentication failed")
	ErrServerError    = errors.New("odoo: internal server error")
)

// ClassifyHTTPError maps an HTTP status code + Odoo ErrorResponse to a sentinel error.
// The returned error wraps the sentinel with context using fmt.Errorf("%w: %s", sentinel, msg).
func ClassifyHTTPError(statusCode int, odooErr *ErrorResponse) error {
	msg := ""
	if odooErr != nil {
		msg = odooErr.Message
	}
	switch statusCode {
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s", ErrAuthentication, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s", ErrAccessDenied, msg)
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", ErrNotFound, msg)
	default:
		// Try to classify by exception name
		if odooErr != nil {
			name := odooErr.Name
			if strings.Contains(name, "AccessError") || strings.Contains(name, "AccessDenied") {
				return fmt.Errorf("%w: %s", ErrAccessDenied, msg)
			}
			if strings.Contains(name, "ValidationError") || strings.Contains(name, "UserError") {
				return fmt.Errorf("%w: %s", ErrValidation, msg)
			}
			if strings.Contains(name, "MissingError") {
				return fmt.Errorf("%w: %s", ErrNotFound, msg)
			}
		}
		return fmt.Errorf("%w: %s", ErrServerError, msg)
	}
}
