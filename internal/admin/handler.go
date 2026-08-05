package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/simstech/odoo-mcp/internal/dexclient"
	"github.com/simstech/odoo-mcp/internal/userstore"
)

// Handler provides admin API handlers for managing Dex/Odoo user mappings.
type Handler struct {
	store     *userstore.Store
	dexClient *dexclient.Client
	encKey    []byte
}

// NewHandler creates a new admin Handler.
func NewHandler(store *userstore.Store, dexClient *dexclient.Client, encKey []byte) *Handler {
	return &Handler{
		store:     store,
		dexClient: dexClient,
		encKey:    encKey,
	}
}

// RegisterRoutes mounts the admin API endpoints on the given mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin", AdminPage)
	mux.HandleFunc("POST /admin/users", h.CreateUser)
	mux.HandleFunc("GET /admin/users", h.ListUsers)
	mux.HandleFunc("DELETE /admin/users/{dex_user_id}", h.DeleteUser)
}

// createUserRequest is the JSON body for POST /admin/users.
type createUserRequest struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	OdooURL    string `json:"odoo_url"`
	OdooDB     string `json:"odoo_db"`
	OdooLogin  string `json:"odoo_login"`
	OdooUID    int    `json:"odoo_uid"`
	OdooAPIKey string `json:"odoo_api_key"`
}

// userResponse is the JSON representation of a user (never includes api_key_encrypted).
type userResponse struct {
	DexUserID string `json:"dex_user_id"`
	Email     string `json:"email"`
	OdooURL   string `json:"odoo_url"`
	OdooDB    string `json:"odoo_db"`
	OdooLogin string `json:"odoo_login"`
	OdooUID   int    `json:"odoo_uid"`
	CreatedAt string `json:"created_at"`
}

// writeJSON is a helper that sets Content-Type and JSON-encodes the response.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("admin: writeJSON failed", "error", err)
	}
}

// CreateUser handles POST /admin/users.
//
// Steps:
//  1. Parse and validate the JSON body.
//  2. Validate the API key against Odoo's context_get endpoint.
//  3. Create the Dex password entry (bcrypt hashing is handled by dexclient).
//  4. Encrypt the API key with KEY_STORE_ENCRYPTION_KEY.
//  5. Store the mapping in PostgreSQL.
//  6. Return 201 with the new dex_user_id.
func (h *Handler) CreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": fmt.Sprintf("invalid JSON: %v", err)})
		return
	}

	// Validate required fields.
	if req.Username == "" || req.Password == "" || req.OdooURL == "" ||
		req.OdooDB == "" || req.OdooLogin == "" || req.OdooAPIKey == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "all fields are required"})
		return
	}

	ctx := r.Context()

	// Step 2: Validate the API key against Odoo's context_get.
	if err := validateOdooAPIKey(ctx, req.OdooURL, req.OdooUID, req.OdooAPIKey); err != nil {
		slog.Warn("admin: CreateUser: api key validation failed", "error", err)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": fmt.Sprintf("API key validation failed: %v", err)})
		return
	}

	// Step 3: Create the Dex password entry. The dexclient handles bcrypt hashing.
	// The returned string is the email (used as the Dex user ID).
	dexUserID, err := h.dexClient.CreatePassword(ctx, req.Username, req.Password)
	if err != nil {
		slog.Error("admin: CreateUser: create dex password", "error", err)
		// CreatePassword returns a specific error for AlreadyExists.
		if strings.Contains(err.Error(), "already exists") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "user already exists"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to create Dex user: %v", err)})
		return
	}

	// Step 4: Encrypt the API key.
	encryptedKey, err := userstore.Encrypt(req.OdooAPIKey, h.encKey)
	if err != nil {
		slog.Error("admin: CreateUser: encrypt api key", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to encrypt API key"})
		return
	}

	// Step 5: Store the mapping.
	mapping := &userstore.Mapping{
		DexUserID:       dexUserID,
		Email:           req.Username,
		OdooURL:         req.OdooURL,
		OdooDB:          req.OdooDB,
		OdooLogin:       req.OdooLogin,
		OdooUID:         req.OdooUID,
		APIKeyEncrypted: encryptedKey,
		CreatedAt:       time.Now().UTC(),
	}
	if err := h.store.CreateMapping(ctx, mapping); err != nil {
		slog.Error("admin: CreateUser: create mapping", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to store mapping: %v", err)})
		return
	}

	slog.Info("admin: user created", "dex_user_id", dexUserID, "email", req.Username)

	// Step 6: Return 201.
	writeJSON(w, http.StatusCreated, map[string]string{"dex_user_id": dexUserID})
}

// ListUsers handles GET /admin/users.
//
// Returns all user mappings as a JSON array. The api_key_encrypted field is
// never included in the response.
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	mappings, err := h.store.ListMappings(r.Context())
	if err != nil {
		slog.Error("admin: ListUsers: list mappings", "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to list users"})
		return
	}

	users := make([]userResponse, 0, len(mappings))
	for _, m := range mappings {
		users = append(users, userResponse{
			DexUserID: m.DexUserID,
			Email:     m.Email,
			OdooURL:   m.OdooURL,
			OdooDB:    m.OdooDB,
			OdooLogin: m.OdooLogin,
			OdooUID:   m.OdooUID,
			CreatedAt: m.CreatedAt.Format(time.RFC3339),
		})
	}

	writeJSON(w, http.StatusOK, users)
}

// DeleteUser handles DELETE /admin/users/{dex_user_id}.
//
// Removes the user mapping from PostgreSQL and returns 204 on success.
func (h *Handler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	dexUserID := r.PathValue("dex_user_id")
	if dexUserID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dex_user_id is required"})
		return
	}

	if err := h.store.DeleteMapping(r.Context(), dexUserID); err != nil {
		slog.Error("admin: DeleteUser: delete mapping", "error", err, "dex_user_id", dexUserID)
		// DeleteMapping returns an error wrapping pgx.ErrNoRows when no row matches.
		if strings.Contains(err.Error(), "no rows") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("failed to delete user: %v", err)})
		return
	}

	slog.Info("admin: user deleted", "dex_user_id", dexUserID)
	w.WriteHeader(http.StatusNoContent)
}

// validateOdooAPIKey calls Odoo's res.users/context_get endpoint to verify
// that the given API key authenticates. If expectedUID > 0, it also checks
// that the authenticated user matches the expected UID.
func validateOdooAPIKey(ctx context.Context, odooURL string, expectedUID int, apiKey string) error {
	url := strings.TrimRight(odooURL, "/") + "/json/2/res.users/context_get"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(`{}`))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("odoo returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// context_get returns the context directly, e.g. {"lang": "en_US", "uid": 5, "tz": "America/New_York"}
	// NOT wrapped in a {"result": ...} envelope.
	var ctxResult map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&ctxResult); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	uidFloat, ok := ctxResult["uid"].(float64)
	if !ok {
		return fmt.Errorf("odoo response missing uid field")
	}
	if expectedUID > 0 && int(uidFloat) != expectedUID {
		return fmt.Errorf("uid mismatch: got %d, expected %d", int(uidFloat), expectedUID)
	}

	return nil
}
