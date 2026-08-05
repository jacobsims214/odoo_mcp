// Package auth provides OAuth 2.0 Bearer token authentication middleware for the
// Odoo MCP server, using the modelcontextprotocol/go-sdk/auth package.
//
// It validates Bearer tokens against a Dex OIDC provider's JWKS endpoint first,
// then falls back to validating the token as a direct Odoo API key.
package auth

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/simstech/odoo-mcp/internal/userstore"
)

// Context key types for per-request values stored by the middleware.
type contextKey string

const (
	// ContextKeyAPIKey is the context key for the raw Odoo API key.
	ContextKeyAPIKey = contextKey("odoo_api_key")
	// ContextKeyUserEmail is the context key for the user's email/login.
	ContextKeyUserEmail = contextKey("user_email")
	// ContextKeyUserUID is the context key for the user's Odoo UID.
	ContextKeyUserUID = contextKey("user_uid")
	// ContextKeyOdooURL is the context key for the per-user Odoo URL.
	ContextKeyOdooURL = contextKey("odoo_url")
	// ContextKeyOdooDB is the context key for the per-user Odoo database.
	ContextKeyOdooDB = contextKey("odoo_db")
)

// Config holds the authentication configuration loaded from environment variables.
type Config struct {
	// DexIssuerURL is the expected OIDC issuer claim in tokens (e.g. "http://localhost:8088/dex").
	DexIssuerURL string
	// DexInternalURL is how the MCP reaches Dex internally for JWKS (e.g. "http://odoo-mcp-envoy:8088/dex").
	// Defaults to DexIssuerURL if not set.
	DexInternalURL string
	// DexPublicURL is the public Dex OIDC issuer URL for clients (e.g. "http://localhost:8088/dex").
	// Used as the authorization_servers field in RFC 9728 protected resource metadata.
	DexPublicURL string
	// MCPPublicURL is the public URL of this MCP server (e.g. "http://localhost:8088/mcp").
	// Used as the `resource` field in RFC 9728 protected resource metadata.
	MCPPublicURL string
	// OdooURL is the base URL of the Odoo instance (e.g. "http://odoo:8069").
	// Used for direct API key validation fallback.
	OdooURL string
	// UserStore provides PostgreSQL-backed lookup of Dex-to-Odoo user mappings.
	// Used after Dex OIDC token validation to retrieve the user's Odoo credentials.
	UserStore *userstore.Store
	// KeyStoreEncryptionKey is the 32-byte AES-256 key used to decrypt Odoo API keys
	// stored in the user_mappings table. Must be hex-encoded in the environment variable.
	KeyStoreEncryptionKey []byte
}

// LoadConfig reads authentication configuration from environment variables.
// Returns an error if DEX_ISSUER_URL is not set.
func LoadConfig() (*Config, error) {
	issuer := os.Getenv("DEX_ISSUER_URL")
	if issuer == "" {
		return nil, errors.New("DEX_ISSUER_URL environment variable is required")
	}
	internalURL := os.Getenv("DEX_INTERNAL_URL")
	if internalURL == "" {
		internalURL = issuer
	}
	publicURL := os.Getenv("ODOO_MCP_PUBLIC_URL")
	if publicURL == "" {
		publicURL = issuer
	}
	dexPublicURL := os.Getenv("DEX_PUBLIC_URL")
	if dexPublicURL == "" {
		dexPublicURL = issuer
	}

	// Read the encryption key for decrypting Odoo API keys from user_mappings.
	var encKey []byte
	if ek := os.Getenv("KEY_STORE_ENCRYPTION_KEY"); ek != "" {
		key, err := hex.DecodeString(ek)
		if err != nil {
			return nil, fmt.Errorf("KEY_STORE_ENCRYPTION_KEY: invalid hex encoding: %w", err)
		}
		encKey = key
	}

	return &Config{
		DexIssuerURL:         issuer,
		DexInternalURL:       internalURL,
		MCPPublicURL:         publicURL,
		DexPublicURL:         dexPublicURL,
		OdooURL:              os.Getenv("ODOO_URL"),
		KeyStoreEncryptionKey: encKey,
	}, nil
}

// NewMiddleware creates an HTTP middleware that enforces Bearer token authentication.
// It first attempts to validate the token as a Dex OIDC token (RS256 via JWKS).
// If OIDC validation fails, it falls back to treating the token as a direct Odoo
// API key by calling the Odoo res.users/context_get endpoint.
//
// When a Dex OIDC token is validated, the middleware stores standard SDK TokenInfo
// (user email, subject) in the request context for backward compatibility.
//
// When an Odoo API key is validated, the middleware stores the raw API key, user
// email, and Odoo UID in the request context via ContextKeyAPIKey, ContextKeyUserEmail,
// and ContextKeyUserUID.
func NewMiddleware(cfg *Config) (func(http.Handler) http.Handler, error) {
	// Build a remote key set from the internally-reachable Dex JWKS endpoint.
	jwksURL := cfg.DexInternalURL + "/keys"
	keySet := oidc.NewRemoteKeySet(context.Background(), jwksURL)

	// Create an ID token verifier that uses the expected public issuer.
	verifier := oidc.NewVerifier(cfg.DexIssuerURL, keySet, &oidc.Config{
		SkipClientIDCheck: true,
	})

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract Bearer token from the Authorization header.
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "Bearer ") && !strings.HasPrefix(authHeader, "bearer ") {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(
					`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource"`,
					cfg.MCPPublicURL,
				))
				http.Error(w, "missing authorization header", http.StatusUnauthorized)
				return
			}
			token := strings.TrimPrefix(authHeader, "Bearer ")
			if token == authHeader {
				token = strings.TrimPrefix(authHeader, "bearer ")
			}

			ctx := r.Context()

			// 1. Try as Dex OIDC token first.
			idToken, err := verifier.Verify(ctx, token)
			if err == nil {
				// OIDC succeeded — extract claims and store SDK TokenInfo.
				var claims struct {
					Email  string `json:"email"`
					Name   string `json:"name"`
					Scope  string `json:"scope"`
					Groups any    `json:"groups"`
				}
				if err := idToken.Claims(&claims); err == nil {
					var scopes []string
					if claims.Scope != "" {
						scopes = strings.Fields(claims.Scope)
					}

					extra := map[string]any{
						"email": claims.Email,
						"name":  claims.Name,
					}
					if claims.Groups != nil {
						extra["groups"] = claims.Groups
					}

					ti := &sdkauth.TokenInfo{
						UserID:     idToken.Subject,
						Expiration: idToken.Expiry,
						Scopes:     scopes,
						Extra:      extra,
					}

					// Look up the user's Odoo credentials from PostgreSQL after OIDC validation.
					// Use the email claim as the lookup key since Dex's subject claim is a
					// protobuf-encoded value (e.g. "\n\x05admin\x12\x05local") that doesn't
					// match the stored DexUserID (which is the email/username).
					lookupKey := claims.Email
					if lookupKey == "" {
						lookupKey = idToken.Subject
					}
					if slog.Default().Enabled(context.Background(), slog.LevelDebug) {
						slog.Debug("auth: looking up user mapping", "lookup_key", lookupKey, "subject", idToken.Subject)
					}
					if cfg.UserStore != nil {
						mapping, lookupErr := cfg.UserStore.GetMapping(ctx, lookupKey)
						if lookupErr != nil {
							slog.Error("auth: database error looking up user mapping", "sub", lookupKey, "error", lookupErr)
							w.Header().Set("WWW-Authenticate", fmt.Sprintf(
								`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource"`,
								cfg.MCPPublicURL,
							))
							http.Error(w, "internal error", http.StatusInternalServerError)
							return
						}
						if mapping == nil {
							slog.Warn("auth: user mapping not found in PostgreSQL", "sub", lookupKey)
							w.Header().Set("WWW-Authenticate", fmt.Sprintf(
								`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource"`,
								cfg.MCPPublicURL,
							))
							http.Error(w, "User not configured — contact your admin", http.StatusUnauthorized)
							return
						}
						// Decrypt the stored API key. Admin/managed users may have no Odoo
						// API key stored (they authenticate to the admin API only) — in that
						// case allow the request through without Odoo credentials rather than
						// failing. Only fail when a key IS present but cannot be decrypted.
						if mapping.APIKeyEncrypted != "" {
							apiKey, decryptErr := userstore.Decrypt(mapping.APIKeyEncrypted, cfg.KeyStoreEncryptionKey)
							if decryptErr != nil {
								slog.Error("auth: failed to decrypt API key", "sub", lookupKey, "error", decryptErr)
								w.Header().Set("WWW-Authenticate", fmt.Sprintf(
									`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource"`,
									cfg.MCPPublicURL,
								))
								http.Error(w, "internal error", http.StatusInternalServerError)
								return
							}
							// Store Odoo credentials in request context.
							r = r.WithContext(context.WithValue(r.Context(), ContextKeyOdooURL, mapping.OdooURL))
							r = r.WithContext(context.WithValue(r.Context(), ContextKeyOdooDB, mapping.OdooDB))
							r = r.WithContext(context.WithValue(r.Context(), ContextKeyAPIKey, apiKey))
							slog.Debug("auth: retrieved Odoo credentials from PostgreSQL", "sub", lookupKey, "odoo_url", mapping.OdooURL, "odoo_db", mapping.OdooDB)
						}
					}

					// Store using SDK's RequireBearerToken middleware for proper
					// TokenInfo context key (unexported type).
					inner := sdkauth.RequireBearerToken(
						sdkauth.TokenVerifier(func(_ context.Context, _ string, _ *http.Request) (*sdkauth.TokenInfo, error) {
							return ti, nil
						}),
						nil,
					)
					inner(next).ServeHTTP(w, r)
					return
				}
			}

			// 2. OIDC failed — try as direct Odoo API key.
			if cfg.OdooURL != "" {
				client := &http.Client{Timeout: 10 * time.Second}
				body := strings.NewReader(`{}`)
				req, reqErr := http.NewRequestWithContext(ctx, "POST", cfg.OdooURL+"/json/2/res.users/context_get", body)
				if reqErr == nil {
					req.Header.Set("Authorization", "bearer "+token)
					req.Header.Set("Content-Type", "application/json; charset=utf-8")

					resp, respErr := client.Do(req)
					if respErr == nil {
						var result map[string]interface{}
						json.NewDecoder(resp.Body).Decode(&result)
						resp.Body.Close()

						uidVal, _ := result["uid"].(float64)
						uid := int(uidVal)
						if uid > 0 {
							login, _ := result["login"].(string)
							ctx = context.WithValue(ctx, ContextKeyAPIKey, token)
							ctx = context.WithValue(ctx, ContextKeyUserEmail, login)
							ctx = context.WithValue(ctx, ContextKeyUserUID, uid)
							next.ServeHTTP(w, r.WithContext(ctx))
							return
						}
					} else if respErr == nil {
						resp.Body.Close()
					}
				}
			}

			// 3. Neither path succeeded — return 401.
			w.Header().Set("WWW-Authenticate", fmt.Sprintf(
				`Bearer error="invalid_token", resource_metadata="%s/.well-known/oauth-protected-resource"`,
				cfg.MCPPublicURL,
			))
			http.Error(w, "invalid token", http.StatusUnauthorized)
		})
	}, nil
}

// ProtectedResourceMetadata returns the OAuth 2.0 Protected Resource Metadata (RFC 9728)
// for the MCP server. This metadata tells MCP clients how to discover and authenticate
// with the authorization server.
//
// resourceURL is the MCP server's public URL (the actual protected resource),
// and dexPublicURL is the public Dex OIDC issuer URL (the authorization server).
func ProtectedResourceMetadata(resourceURL, dexPublicURL string) *oauthex.ProtectedResourceMetadata {
	return &oauthex.ProtectedResourceMetadata{
		Resource: resourceURL,
		AuthorizationServers: []string{
			dexPublicURL,
		},
		ScopesSupported: []string{
			"openid",
			"email",
			"profile",
			"offline_access",
		},
		BearerMethodsSupported: []string{
			"header",
		},
	}
}

// NewProtectedResourceMetadataHandler returns an http.Handler that serves OAuth 2.0
// protected resource metadata (RFC 9728) at /.well-known/oauth-protected-resource.
// It delegates to the SDK's ProtectedResourceMetadataHandler.
func NewProtectedResourceMetadataHandler(resourceURL, dexPublicURL string) http.Handler {
	return sdkauth.ProtectedResourceMetadataHandler(ProtectedResourceMetadata(resourceURL, dexPublicURL))
}

// UserEmailFromContext extracts the authenticated user's email from the request context.
// Returns empty string if no user identity is present.
//
// It first checks for SDK TokenInfo (set by Dex OIDC authentication), then falls back
// to the custom context key (set by direct Odoo API key authentication).
func UserEmailFromContext(ctx context.Context) string {
	// Try SDK TokenInfo first (Dex OIDC path).
	ti := sdkauth.TokenInfoFromContext(ctx)
	if ti != nil {
		// Prefer the email from Extra claims.
		if email, ok := ti.Extra["email"].(string); ok && email != "" {
			return email
		}
		// Fall back to UserID (which is the token's subject).
		return ti.UserID
	}

	// Fall back to our custom context key (direct API key path).
	if email, ok := ctx.Value(ContextKeyUserEmail).(string); ok {
		return email
	}

	return ""
}

// RegisterRoutes registers the /register-key endpoint on the given mux.
// The /.well-known/oauth-protected-resource endpoint is mounted directly
// in main.go WITHOUT auth middleware since it must be publicly accessible
// for OAuth client discovery (RFC 9728 §3.1).
func RegisterRoutes(mux *http.ServeMux, odooURL, odooDB, keyStoreURL string) {
	mux.Handle("/register-key", RegisterKeyHandler(odooURL, odooDB, keyStoreURL))
}