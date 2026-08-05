// Binary mcpoauth is an MCP OAuth broker HTTP service that bridges between
// MCP clients and Dex. It handles four endpoints:
//
//   - POST /register                    — Dynamic Client Registration (RFC 7591)
//   - GET  /authorize                   — Authorization redirect with openid injection
//   - GET  /.well-known/oauth-authorization-server — RFC 8414 metadata
//   - GET  /.well-known/oauth-protected-resource   — RFC 9728 metadata
//
// Configuration is provided via environment variables (see main function).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/simstech/odoo-mcp/internal/dexclient"
)

// wellKnownClients maps client IDs that don't need dynamic registration
// to their allowed redirect URIs. These are hardcoded clients like Claude Desktop.
var wellKnownClients = map[string][]string{
	"claude-desktop": {
		"https://claude.ai/api/mcp/auth_callback",
	},
}

// isLoopbackRedirect checks if a redirect URI uses a loopback address
// (localhost, 127.0.0.1, [::1], or any IPv6 loopback address).
// Loopback redirects are accepted for all clients.
func isLoopbackRedirect(redirectURI string) bool {
	u, err := url.Parse(redirectURI)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return false
}

type config struct {
	BrokerPort      string
	BrokerPublicURL string
	SelfURL         string   // broker's own public URL (for authorization_endpoint redirect)
	DexURL          string
	DexPublicURL    string   // public Dex URL for browser redirects (e.g. http://localhost:8088)
	MCPPublicURL    string
	DexGRPCAddr     string
	DexGRPCCA      string
	DexGRPCCert    string
	DexGRPCKey     string
}

func loadConfig() *config {
	getEnv := func(key, def string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return def
	}
	return &config{
		BrokerPort:      getEnv("BROKER_PORT", "8090"),
		BrokerPublicURL: getEnv("BROKER_PUBLIC_URL", "http://localhost:8088"),
		SelfURL:         getEnv("BROKER_PUBLIC_URL", "http://localhost:8088"),
		DexURL:          getEnv("DEX_URL", "http://odoo-mcp-dex:5556"),
		DexPublicURL:    getEnv("DEX_PUBLIC_URL", "http://localhost:8088"),
		MCPPublicURL:    getEnv("MCP_PUBLIC_URL", "http://localhost:8088/mcp"),
		DexGRPCAddr:     getEnv("DEX_GRPC_ADDR", "odoo-mcp-dex:5557"),
		DexGRPCCA:       getEnv("DEX_GRPC_CA", ""),
		DexGRPCCert:     getEnv("DEX_GRPC_CERT", ""),
		DexGRPCKey:      getEnv("DEX_GRPC_KEY", ""),
	}
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Initialize Dex gRPC client.
	logger.Info("connecting to Dex gRPC", "addr", cfg.DexGRPCAddr)
	dc, err := dexclient.NewClient(context.Background(), cfg.DexGRPCAddr, cfg.DexGRPCCA, cfg.DexGRPCCert, cfg.DexGRPCKey)
	if err != nil {
		logger.Error("failed to create Dex client", "error", err)
		os.Exit(1)
	}
	defer dc.Close()

	mux := http.NewServeMux()

	// GET /health — Health check endpoint (no method restriction for compatibility).
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	// POST /register — Dynamic Client Registration (RFC 7591)
	mux.HandleFunc("POST /register", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			RedirectURIs []string `json:"redirect_uris"`
			ClientName   string   `json:"client_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		clientID, err := dc.CreateClient(r.Context(), req.RedirectURIs, req.ClientName)
		if err != nil {
			logger.Error("client registration failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "registration failed"})
			return
		}

		logger.Info("client registered", "client_id", clientID, "name", req.ClientName)

		writeJSON(w, http.StatusCreated, map[string]interface{}{
			"client_id":                  clientID,
			"client_name":                req.ClientName,
			"redirect_uris":              req.RedirectURIs,
			"grant_types":                []string{"authorization_code", "refresh_token"},
			"response_types":             []string{"code"},
			"token_endpoint_auth_method": "none",
			"client_id_issued_at":        time.Now().Unix(),
			"client_secret_expires_at":   0,
		})
	})

	// GET /authorize — Authorization redirect with openid scope injection.
	// Also ensures email and profile scopes are present so Dex includes the email
	// claim in the ID token. The MCP server's auth middleware uses the email claim
	// to look up the user's Odoo credentials in PostgreSQL — without it, the lookup
	// falls back to the protobuf-encoded Dex subject, which never matches.
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		clientID := q.Get("client_id")
		redirectURI := q.Get("redirect_uri")

		// Check well-known client support. Well-known clients like Claude Desktop
		// have hardcoded client IDs and don't go through dynamic registration.
		if clientID != "" {
			if allowedURIs, ok := wellKnownClients[clientID]; ok {
				valid := isLoopbackRedirect(redirectURI)
				if !valid {
					for _, allowed := range allowedURIs {
						if allowed == redirectURI {
							valid = true
							break
						}
					}
				}
				if !valid {
					logger.Warn("authorize rejected: invalid redirect_uri for well-known client",
						"client_id", clientID, "redirect_uri", redirectURI)
					writeJSON(w, http.StatusBadRequest, map[string]string{
						"error": "invalid redirect_uri for well-known client",
					})
					return
				}
			}
		}

		// Ensure openid, email, and profile scopes are present.
		// The MCP auth middleware (internal/auth/middleware.go) uses claims.Email
		// as the lookup key for user mappings. Without the email scope, Dex omits
		// the email claim from the ID token, causing the lookup to fail.
		scope := q.Get("scope")
		requiredScopes := []string{"openid", "email", "profile"}
		for _, required := range requiredScopes {
			if !containsScope(scope, required) {
				if scope == "" {
					scope = required
				} else {
					scope = required + " " + scope
				}
			}
		}
		q.Set("scope", scope)

		// Build redirect URL to Dex's authorization endpoint.
		dexAuthURL := fmt.Sprintf("%s/dex/auth", strings.TrimRight(cfg.DexPublicURL, "/"))
		redirectTo := dexAuthURL + "?" + q.Encode()

		logger.Info("authorize redirect", "to", redirectTo)
		http.Redirect(w, r, redirectTo, http.StatusFound)
	})

	// GET /.well-known/oauth-authorization-server — RFC 8414 metadata.
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		selfBase := strings.TrimRight(cfg.SelfURL, "/")
		dexBase := strings.TrimRight(cfg.DexPublicURL, "/")
		authEndpoint := selfBase + "/authorize"
		regEndpoint := selfBase + "/register"
		tokenEndpoint := dexBase + "/dex/token"
		jwksURI := dexBase + "/dex/keys"

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"issuer":                               dexBase + "/dex",
			"authorization_endpoint":               authEndpoint,
			"token_endpoint":                       tokenEndpoint,
			"jwks_uri":                             jwksURI,
			"registration_endpoint":                regEndpoint,
			"scopes_supported":                     []string{"openid", "profile", "email", "offline_access"},
			"response_types_supported":             []string{"code"},
			"grant_types_supported":                []string{"authorization_code", "refresh_token"},
			"token_endpoint_auth_methods_supported": []string{"none"},
			"code_challenge_methods_supported":      []string{"S256"},
		})
	})

	// GET /.well-known/oauth-protected-resource — RFC 9728 metadata.
	// authorization_servers must point to the Dex issuer URL (with /dex path),
	// not the broker's base URL.
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		mcpURL := strings.TrimRight(cfg.MCPPublicURL, "/")
		dexIssuer := strings.TrimRight(cfg.DexPublicURL, "/") + "/dex"

		writeJSON(w, http.StatusOK, map[string]interface{}{
			"resource":              mcpURL,
			"authorization_servers": []string{dexIssuer},
		})
	})

	// Start HTTP server.
	addr := ":" + cfg.BrokerPort
	server := &http.Server{
		Addr:              addr,
		Handler:           withLogging(logger, mux),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		logger.Info("shutting down server")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	logger.Info("starting MCP OAuth broker", "addr", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("server error", "error", err)
		os.Exit(1)
	}
}

// containsScope checks whether the given scope string contains the target scope.
func containsScope(scope, target string) bool {
	for _, s := range strings.Fields(scope) {
		if s == target {
			return true
		}
	}
	return false
}

// writeJSON is a helper that sets Content-Type, status code, and JSON-encodes
// the response body.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// withLogging wraps an http.Handler with request logging using slog.
func withLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(lrw, r)
		logger.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration", time.Since(start).String(),
		)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}