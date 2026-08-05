package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration for the Odoo MCP server.
type Config struct {
	// Odoo connection
	OdooAPIKey  string        // ODOO_API_KEY — required for service account mode
	OdooTimeout time.Duration // ODOO_TIMEOUT — default 30s

	// Transport
	Transport string // ODOO_MCP_TRANSPORT — "stdio" | "http" | "sse", default "stdio"
	HTTPAddr  string // ODOO_MCP_ADDR — default ":8080"

	// Auth mode
	AuthMode      string // ODOO_MCP_AUTH_MODE — "service" | "per-session", default "service"
	MCPPublicURL  string // ODOO_MCP_PUBLIC_URL — public URL of this MCP server, default "http://localhost:8080"
	DexPublicURL  string // DEX_PUBLIC_URL — public Dex OIDC issuer URL for clients, default same as DEX_ISSUER_URL
	KeyStoreURL   string // KEY_STORE_URL — URL of the Key Store service, default "http://keystore:8080"

	// Session mode
	StatelessSessions bool // STATELESS_SESSIONS — default false (stateful)

	// Guardrails
	BlockedModels []string // ODOO_BLOCKED_MODELS — comma-separated, default list
	ReadOnlyMode  bool     // ODOO_READ_ONLY — default false
	RateLimitRPS  int      // ODOO_RATE_LIMIT_RPS — requests per second, default 10

	// Cache
	ValkeyAddr string // VALKEY_ADDR — Valkey/Redis address, default "valkey:6379"

	// Database (user_mappings)
	DatabaseURL string // DATABASE_URL — PostgreSQL connection string for the MCP database

	// Dex OIDC (token validation)
	DexIssuerURL   string // DEX_ISSUER_URL — Dex OIDC issuer URL, e.g. "http://localhost:8088/dex"
	DexInternalURL string // DEX_INTERNAL_URL — Internal Dex URL for JWKS, defaults to DEX_ISSUER_URL

	// Dex gRPC (admin API for user/password management)
	DexGRPCAddr string // DEX_GRPC_ADDR — Dex gRPC address, e.g. "dex-grpc.namespace.svc.cluster.local:5557"
	DexGRPCCA   string // DEX_GRPC_CA — CA cert file for Dex gRPC mTLS
	DexGRPCCert string // DEX_GRPC_CERT — client cert file for Dex gRPC mTLS
	DexGRPCKey  string // DEX_GRPC_KEY — client key file for Dex gRPC mTLS

	// Key Store encryption
	KeyStoreEncryptionKey string // KEY_STORE_ENCRYPTION_KEY — 32-byte hex-encoded AES-256 key for encrypting Odoo API keys

	// Logging
	LogLevel  string // ODOO_LOG_LEVEL — "debug"|"info"|"warn"|"error", default "info"
	AuditLog  bool   // ODOO_AUDIT_LOG — default true
	AuditFile string // ODOO_AUDIT_FILE — path to audit log file, default "" (stdout)
}

// DefaultBlockedModels are models that cannot be accessed via MCP by default.
var DefaultBlockedModels = []string{
	"ir.rule",
	"ir.model.access",
	"res.users.apikeys",
}

// Load reads configuration from environment variables.
// It loads a .env file if present (for local dev).
func Load() (*Config, error) {
	// Load .env file if it exists (no-op if it doesn't)
	_ = godotenv.Load()

	c := &Config{
		// Odoo connection
		OdooAPIKey:  os.Getenv("ODOO_API_KEY"),
		OdooTimeout: 30 * time.Second, // default

		// Transport
		Transport: getEnvOrDefault("ODOO_MCP_TRANSPORT", "stdio"),
		HTTPAddr:  getEnvOrDefault("ODOO_MCP_ADDR", ":8080"),

		// Auth mode
		AuthMode:     getEnvOrDefault("ODOO_MCP_AUTH_MODE", "service"),
		MCPPublicURL: getEnvOrDefault("ODOO_MCP_PUBLIC_URL", "http://localhost:8080"),
		DexPublicURL: os.Getenv("DEX_PUBLIC_URL"),
		KeyStoreURL:  getEnvOrDefault("KEY_STORE_URL", "http://keystore:8080"),

		// Session mode
		StatelessSessions: getEnvBool("STATELESS_SESSIONS", false),

		// Guardrails
		ReadOnlyMode: getEnvBool("ODOO_READ_ONLY", false),
		RateLimitRPS: getEnvInt("ODOO_RATE_LIMIT_RPS", 10),

		// Cache
		ValkeyAddr: getEnvOrDefault("VALKEY_ADDR", "valkey:6379"),

		// Database
		DatabaseURL: os.Getenv("DATABASE_URL"),

// Dex OIDC
		DexIssuerURL:   os.Getenv("DEX_ISSUER_URL"),
		DexInternalURL: getEnvOrDefault("DEX_INTERNAL_URL", os.Getenv("DEX_ISSUER_URL")),

		// Dex gRPC
		DexGRPCAddr: getEnvOrDefault("DEX_GRPC_ADDR", ""),
		DexGRPCCA:   os.Getenv("DEX_GRPC_CA"),
		DexGRPCCert: os.Getenv("DEX_GRPC_CERT"),
		DexGRPCKey:  os.Getenv("DEX_GRPC_KEY"),

		// Key Store encryption
		KeyStoreEncryptionKey: os.Getenv("KEY_STORE_ENCRYPTION_KEY"),

		// Logging
		LogLevel:  getEnvOrDefault("ODOO_LOG_LEVEL", "info"),
		AuditLog:  getEnvBool("ODOO_AUDIT_LOG", true),
		AuditFile: os.Getenv("ODOO_AUDIT_FILE"),
	}

	// Parse ODOO_TIMEOUT if provided
	if timeoutStr := os.Getenv("ODOO_TIMEOUT"); timeoutStr != "" {
		duration, err := time.ParseDuration(timeoutStr)
		if err != nil {
			return nil, fmt.Errorf("invalid ODOO_TIMEOUT: %w", err)
		}
		c.OdooTimeout = duration
	}

	// Parse ODOO_BLOCKED_MODELS
	c.BlockedModels = DefaultBlockedModels
	if blockedStr := os.Getenv("ODOO_BLOCKED_MODELS"); blockedStr != "" {
		customBlocked := strings.Split(blockedStr, ",")
		for i, model := range customBlocked {
			customBlocked[i] = strings.TrimSpace(model)
		}
		// Merge with defaults
		c.BlockedModels = append(c.BlockedModels, customBlocked...)
	}

	return c, nil
}

// Validate checks that required fields are set and values are valid.
func (c *Config) Validate() error {
	// OdooAPIKey is required for service auth mode
	if c.AuthMode == "service" && c.OdooAPIKey == "" {
		return fmt.Errorf("ODOO_API_KEY is required when ODOO_MCP_AUTH_MODE is 'service'")
	}

	// Validate Transport
	validTransports := map[string]bool{
		"stdio": true,
		"http":  true,
	}
	if !validTransports[c.Transport] {
		return fmt.Errorf("invalid ODOO_MCP_TRANSPORT: %q (must be 'stdio' or 'http')", c.Transport)
	}

	// Validate AuthMode
	validAuthModes := map[string]bool{
		"service":     true,
		"per-session": true,
	}
	if !validAuthModes[c.AuthMode] {
		return fmt.Errorf("invalid ODOO_MCP_AUTH_MODE: %q (must be 'service' or 'per-session')", c.AuthMode)
	}

	// Validate LogLevel
	validLogLevels := map[string]bool{
		"debug": true,
		"info":  true,
		"warn":  true,
		"error": true,
	}
	if !validLogLevels[c.LogLevel] {
		return fmt.Errorf("invalid ODOO_LOG_LEVEL: %q (must be 'debug', 'info', 'warn', or 'error')", c.LogLevel)
	}

	// Validate RateLimitRPS
	if c.RateLimitRPS < 1 {
		return fmt.Errorf("ODOO_RATE_LIMIT_RPS must be at least 1, got %d", c.RateLimitRPS)
	}

	return nil
}

// Helper functions

// getEnvOrDefault returns the environment variable value or a default if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvBool parses an environment variable as a boolean.
func getEnvBool(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		switch strings.ToLower(value) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off":
			return false
		}
	}
	return defaultValue
}

// getEnvInt parses an environment variable as an integer.
func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intVal, err := strconv.Atoi(value); err == nil {
			return intVal
		}
	}
	return defaultValue
}
