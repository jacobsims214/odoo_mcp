package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/simstech/odoo-mcp/internal/admin"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/auth"
	"github.com/simstech/odoo-mcp/internal/cache"
	"github.com/simstech/odoo-mcp/internal/config"
	"github.com/simstech/odoo-mcp/internal/dexclient"
	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/simstech/odoo-mcp/internal/server"
	"github.com/simstech/odoo-mcp/internal/userstore"
)

var version = "dev" // injected via ldflags: -ldflags "-X main.version=1.0.0"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	// 1. Load and validate config
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	// 2. Configure structured logging
	logLevel := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		logLevel = slog.LevelDebug
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})))

	slog.Info("odoo-mcp-server starting",
		"version", version,
		"transport", cfg.Transport,
		"read_only", cfg.ReadOnlyMode,
	)

	// 3. Build Odoo client
	odooClient := odoo.NewHTTPClient(os.Getenv("ODOO_URL"), os.Getenv("ODOO_DB"), cfg.OdooAPIKey, cfg.OdooTimeout)

	// 4. Build audit logger
	var auditLog audit.AuditLogger
	if cfg.AuditLog {
		al, err := audit.New(cfg.AuditFile)
		if err != nil {
			return fmt.Errorf("create audit logger: %w", err)
		}
		auditLog = al
	} else {
		auditLog = &audit.NoopLogger{}
	}

	// 5. Connect to PostgreSQL for user mappings
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var store *userstore.Store
	if cfg.DatabaseURL != "" {
		s, err := userstore.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			return fmt.Errorf("connect to database: %w", err)
		}
		store = s
		defer store.Close()
		slog.Info("connected to PostgreSQL for user mappings")
	} else {
		slog.Warn("no DATABASE_URL set — user mapping storage disabled")
	}

	// 6. Connect to Dex gRPC for admin API
	var dexCli *dexclient.Client
	if cfg.DexGRPCAddr != "" {
		dc, err := dexclient.NewClient(ctx, cfg.DexGRPCAddr, cfg.DexGRPCCA, cfg.DexGRPCCert, cfg.DexGRPCKey)
		if err != nil {
			return fmt.Errorf("connect to Dex gRPC: %w", err)
		}
		dexCli = dc
		defer dexCli.Close()
		slog.Info("connected to Dex gRPC", "addr", cfg.DexGRPCAddr)
	} else {
		slog.Warn("no DEX_GRPC_ADDR set — Dex admin API disabled")
	}

	// 7. Bootstrap admin user (if database and Dex are available)
	if store != nil && dexCli != nil {
		if err := bootstrapAdmin(ctx, store, dexCli); err != nil {
			return fmt.Errorf("bootstrap admin: %w", err)
		}
	}

	// 8. Connect to Valkey for caching (optional)
	var vc *cache.ValkeyClient
	if cfg.ValkeyAddr != "" {
		vc, err = cache.NewValkeyClient(cfg.ValkeyAddr)
		if err != nil {
			slog.Warn("failed to connect to Valkey, caching disabled", "error", err)
		} else {
			defer vc.Close()
			slog.Info("connected to Valkey", "addr", cfg.ValkeyAddr)
		}
	}

	// 9. Build MCP server
	mcpSrv := server.Build(cfg, odooClient, auditLog, vc)

	// 10. Start transport
	switch cfg.Transport {
	case "stdio":
		slog.Info("starting stdio transport")
		return mcpSrv.Run(ctx, &mcp.StdioTransport{})

	case "http":
		return startHTTP(ctx, cfg, mcpSrv, auditLog, vc, store, dexCli)

	default:
		return fmt.Errorf("unknown transport: %s (must be stdio or http)", cfg.Transport)
	}
}

// startHTTP builds the HTTP mux and starts the HTTP server.
func startHTTP(ctx context.Context, cfg *config.Config, mcpSrv *mcp.Server, auditLog audit.AuditLogger, vc *cache.ValkeyClient, store *userstore.Store, dexCli *dexclient.Client) error {
	mux := http.NewServeMux()

	// Parse the encryption key for API key storage (needed by both auth middleware and admin handler)
	var encKey []byte
	if cfg.KeyStoreEncryptionKey != "" {
		key, err := hex.DecodeString(cfg.KeyStoreEncryptionKey)
		if err != nil {
			slog.Error("invalid KEY_STORE_ENCRYPTION_KEY", "error", err)
		} else {
			encKey = key
		}
	}

	// Create Dex OIDC middleware for MCP and admin endpoints (needs UserStore).
	// Created once and shared between MCP and admin routes.
	var dexMW func(http.Handler) http.Handler
	if store != nil {
		authCfg := &auth.Config{
			DexIssuerURL:          cfg.DexIssuerURL,
			DexInternalURL:        cfg.DexInternalURL,
			MCPPublicURL:          cfg.MCPPublicURL,
			UserStore:             store,
			KeyStoreEncryptionKey: encKey,
		}
		var err error
		dexMW, err = auth.NewMiddleware(authCfg)
		if err != nil {
			slog.Warn("failed to create auth middleware, MCP and admin API will be unprotected", "error", err)
		}
	}

	// MCP endpoint via Streamable HTTP — protected by auth middleware
	mcpHandler := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		// Extract per-user Odoo credentials from the auth middleware context.
		// The auth middleware (internal/auth/middleware.go) stores these values
		// after Dex OIDC token validation + user mapping lookup.
		apiKey := r.Context().Value(auth.ContextKeyAPIKey)
		odooURL := r.Context().Value(auth.ContextKeyOdooURL)
		odooDB := r.Context().Value(auth.ContextKeyOdooDB)

		if apiKey != nil && odooURL != nil && odooDB != nil {
			apiKeyStr, _ := apiKey.(string)
			urlStr, _ := odooURL.(string)
			dbStr, _ := odooDB.(string)

			if apiKeyStr != "" && urlStr != "" && dbStr != "" {
				// Create a per-user OdooClient with the user's personal credentials.
				perUserClient := odoo.NewHTTPClient(urlStr, dbStr, apiKeyStr, cfg.OdooTimeout)
				// Build a per-user server with this client.
				return server.Build(cfg, perUserClient, auditLog, vc)
			}
		}

		// Fallback to shared server (for admin users with no Odoo creds).
		return mcpSrv
	}, &mcp.StreamableHTTPOptions{
		Stateless: cfg.StatelessSessions,
	})
	if dexMW != nil {
		mux.Handle("/mcp", dexMW(mcpHandler))
	} else {
		mux.Handle("/mcp", mcpHandler)
	}

	// Admin UI and API routes
	if store != nil && dexCli != nil {
		adminHandler := admin.NewHandler(store, dexCli, encKey)

		// Admin HTML pages — no auth (user needs to load before login)
		mux.HandleFunc("GET /admin", admin.AdminPage)
		mux.HandleFunc("GET /admin/", admin.AdminPage)

		// Admin API endpoints — protected by Dex OIDC middleware (reuse dexMW from above)
		if dexMW != nil {
			mux.Handle("GET /admin/users", dexMW(http.HandlerFunc(adminHandler.ListUsers)))
			mux.Handle("POST /admin/users", dexMW(http.HandlerFunc(adminHandler.CreateUser)))
			mux.Handle("DELETE /admin/users/{dex_user_id}", dexMW(http.HandlerFunc(adminHandler.DeleteUser)))
		} else {
			slog.Warn("admin API endpoints unprotected — no auth middleware available")
			mux.Handle("GET /admin/users", http.HandlerFunc(adminHandler.ListUsers))
			mux.Handle("POST /admin/users", http.HandlerFunc(adminHandler.CreateUser))
			mux.Handle("DELETE /admin/users/{dex_user_id}", http.HandlerFunc(adminHandler.DeleteUser))
		}
	}

	// Health check
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ok","version":"%s"}`, version)
	})

	// OAuth protected resource metadata (RFC 9728) — no auth
	// Use DexPublicURL if set, otherwise fall back to DexIssuerURL (which includes /dex path).
	metadataURL := cfg.DexPublicURL
	if metadataURL == "" {
		metadataURL = cfg.DexIssuerURL
	}
	if metadataURL != "" {
		mux.Handle("GET /.well-known/oauth-protected-resource",
			auth.NewProtectedResourceMetadataHandler(cfg.MCPPublicURL, metadataURL))
	}

	// Start HTTP server with graceful shutdown
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("starting HTTP transport", "addr", cfg.HTTPAddr)
	go func() {
		<-ctx.Done()
		slog.Info("shutting down HTTP server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()
		httpSrv.Shutdown(shutdownCtx)
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}

// bootstrapAdmin creates the initial admin user on first startup.
// It is a no-op if any users already exist.
func bootstrapAdmin(ctx context.Context, store *userstore.Store, dexCli *dexclient.Client) error {
	mappings, err := store.ListMappings(ctx)
	if err != nil {
		return fmt.Errorf("list mappings for bootstrap: %w", err)
	}
	if len(mappings) > 0 {
		slog.Info("bootstrap: admin already exists, skipping")
		return nil
	}

	username := getEnv("ADMIN_USERNAME", "admin")
	password := os.Getenv("ADMIN_PASSWORD")
	if password == "" {
		password = generateRandomPassword(16)
		slog.Info("BOOTSTRAP: default admin created",
			"username", username,
			"password", password,
		)
	}

	dexUserID, err := dexCli.CreatePassword(ctx, username, password)
	if err != nil {
		return fmt.Errorf("create admin in Dex: %w", err)
	}

	if err := store.CreateMapping(ctx, &userstore.Mapping{
		DexUserID: dexUserID,
		Email:     username,
	}); err != nil {
		return fmt.Errorf("store admin mapping: %w", err)
	}

	slog.Info("bootstrap: admin user created", "username", username, "dex_user_id", dexUserID)
	return nil
}

// generateRandomPassword generates a cryptographically random alphanumeric password.
func generateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, length)
	for i := range result {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		if err != nil {
			result[i] = charset[i%len(charset)]
			continue
		}
		result[i] = charset[n.Int64()]
	}
	return string(result)
}

// getEnv returns the environment variable value or a default if not set.
func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
