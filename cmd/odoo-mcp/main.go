package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/simstech/odoo-mcp/internal/audit"
	"github.com/simstech/odoo-mcp/internal/config"
	"github.com/simstech/odoo-mcp/internal/odoo"
	"github.com/simstech/odoo-mcp/internal/server"
	"github.com/simstech/odoo-mcp/internal/session"
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
		"odoo_url", cfg.OdooURL,
		"odoo_db", cfg.OdooDB,
		"read_only", cfg.ReadOnlyMode,
	)

	// 3. Build Odoo client
	odooClient := odoo.NewHTTPClient(cfg.OdooURL, cfg.OdooDB, cfg.OdooAPIKey, cfg.OdooTimeout)

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

	// 5. For stdio: create a single session. For HTTP: session is per-connection.
	var sess *session.Session
	if cfg.Transport == "stdio" {
		sess = session.NewSession("stdio", odooClient)
	}

	// 6. Build MCP server
	mcpSrv := server.Build(cfg, odooClient, auditLog, sess)

	// 7. Start transport
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	switch cfg.Transport {
	case "stdio":
		slog.Info("starting stdio transport")
		return mcpserver.ServeStdio(mcpSrv)

	case "http":
		httpSrv := mcpserver.NewStreamableHTTPServer(mcpSrv,
			mcpserver.WithEndpointPath("/mcp"),
			mcpserver.WithStateful(true),
			mcpserver.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
				return ctx
			}),
		)
		slog.Info("starting HTTP transport", "addr", cfg.HTTPAddr)
		go func() {
			<-ctx.Done()
			httpSrv.Shutdown(context.Background())
		}()
		return httpSrv.Start(cfg.HTTPAddr)

	case "sse":
		sseSrv := mcpserver.NewSSEServer(mcpSrv,
			mcpserver.WithBaseURL(fmt.Sprintf("http://localhost%s", cfg.HTTPAddr)),
			mcpserver.WithKeepAlive(true),
		)
		slog.Info("starting SSE transport", "addr", cfg.HTTPAddr)
		go func() {
			<-ctx.Done()
			sseSrv.Shutdown(context.Background())
		}()
		return sseSrv.Start(cfg.HTTPAddr)

	default:
		return fmt.Errorf("unknown transport: %s (must be stdio, http, or sse)", cfg.Transport)
	}
}
