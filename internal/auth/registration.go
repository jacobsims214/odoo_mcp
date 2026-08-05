package auth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/simstech/odoo-mcp/internal/odoo"
)

// registerKeyRequest is the expected JSON body for POST /register-key.
type registerKeyRequest struct {
	APIKey string `json:"api_key"`
}

// registerKeyResponse is the JSON body returned on successful registration.
type registerKeyResponse struct {
	UserID    string `json:"user_id"`
	OdooUID   int64  `json:"odoo_uid"`
	OdooLogin string `json:"odoo_login"`
	OdooName  string `json:"odoo_name"`
}

// keystorePutRequest is the JSON body sent to the Key Store's PUT /keys/{email} endpoint.
type keystorePutRequest struct {
	OdooUID   int64  `json:"odoo_uid"`
	OdooLogin string `json:"odoo_login"`
	APIKey    string `json:"api_key"`
}

// RegisterKeyHandler returns an http.HandlerFunc that:
// 1. Accepts an Odoo API key from the request body
// 2. Validates it by calling Odoo's ServerInfo
// 3. Stores the key in the Key Store mapped to the user's email
// 4. Returns user_id, odoo_uid, odoo_login, and odoo_name
func RegisterKeyHandler(odooURL, odooDB, keyStoreURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// 1. Parse request body (capped at 4 KB to prevent memory exhaustion)
		r.Body = http.MaxBytesReader(w, r.Body, 1<<12)

		var req registerKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				slog.Warn("register-key: request body too large", "error", err)
				writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			slog.Error("register-key: invalid JSON body", "error", err)
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}

		if req.APIKey == "" {
			slog.Warn("register-key: missing api_key")
			writeJSONError(w, http.StatusBadRequest, "api_key is required")
			return
		}

		// 2. Create a temporary Odoo client with the provided key
		client := odoo.NewHTTPClient(odooURL, odooDB, req.APIKey, 10*time.Second)

		// 3. Validate the key by calling ServerInfo
		info, err := client.ServerInfo(r.Context())
		if err != nil {
			if errors.Is(err, odoo.ErrAuthentication) {
				slog.Warn("register-key: invalid API key", "error", err)
				writeJSONError(w, http.StatusUnauthorized, "invalid API key")
				return
			}
			slog.Error("register-key: server info failed", "error", err)
			writeJSONError(w, http.StatusBadGateway, "key validation failed: Odoo unreachable")
			return
		}

		// 4. Get user email from context (set by AuthMiddleware)
		userEmail := UserEmailFromContext(r.Context())
		if userEmail == "" {
			slog.Warn("register-key: user email not found in context")
			writeJSONError(w, http.StatusBadRequest, "user email not found in request context")
			return
		}

		// 5. Store in Key Store: PUT /keys/{email}
		keyPayload := keystorePutRequest{
			OdooUID:   info.UID,
			OdooLogin: info.Login,
			APIKey:    req.APIKey,
		}

		payloadBytes, err := json.Marshal(keyPayload)
		if err != nil {
			slog.Error("register-key: failed to marshal key store payload", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}

		keyStorePutURL := fmt.Sprintf("%s/keys/%s", keyStoreURL, userEmail)
		putReq, err := http.NewRequestWithContext(r.Context(), http.MethodPut, keyStorePutURL, bytes.NewReader(payloadBytes))
		if err != nil {
			slog.Error("register-key: failed to create key store request", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		putReq.Header.Set("Content-Type", "application/json")

		httpClient := &http.Client{Timeout: 5 * time.Second}
		putResp, err := httpClient.Do(putReq)
		if err != nil {
			slog.Error("register-key: key store request failed", "error", err)
			writeJSONError(w, http.StatusInternalServerError, "failed to store API key")
			return
		}
		defer putResp.Body.Close()

		if putResp.StatusCode != http.StatusOK && putResp.StatusCode != http.StatusCreated {
			respBody, _ := io.ReadAll(putResp.Body)
			slog.Error("register-key: key store returned error",
				"status", putResp.StatusCode,
				"body", string(respBody),
			)
			writeJSONError(w, http.StatusInternalServerError, "failed to store API key")
			return
		}

		// 6. Return success
		resp := registerKeyResponse{
			UserID:    userEmail,
			OdooUID:   info.UID,
			OdooLogin: info.Login,
			OdooName:  info.Name,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			slog.Error("register-key: failed to write response", "error", err)
		}

		slog.Info("register-key: key registered successfully",
			"user_id", userEmail,
			"odoo_uid", info.UID,
			"odoo_login", info.Login,
			"odoo_name", info.Name,
		)
	}
}

// writeJSONError writes a JSON error response with the given status code and message.
func writeJSONError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}

