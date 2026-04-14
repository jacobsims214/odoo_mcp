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
	OdooURL     string        // ODOO_URL — required, e.g. "http://localhost:8069"
	OdooDB      string        // ODOO_DB — required, e.g. "odoo19"
	OdooAPIKey  string        // ODOO_API_KEY — required for service account mode
	OdooTimeout time.Duration // ODOO_TIMEOUT — default 30s

	// Transport
	Transport string // ODOO_MCP_TRANSPORT — "stdio" | "http" | "sse", default "stdio"
	HTTPAddr  string // ODOO_MCP_ADDR — default ":8080"

	// Auth mode
	AuthMode string // ODOO_MCP_AUTH_MODE — "service" | "per-session", default "service"

	// Guardrails
	BlockedModels []string // ODOO_BLOCKED_MODELS — comma-separated, default list
	ReadOnlyMode  bool     // ODOO_READ_ONLY — default false
	RateLimitRPS  int      // ODOO_RATE_LIMIT_RPS — requests per second, default 10

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
		OdooURL:     os.Getenv("ODOO_URL"),
		OdooDB:      os.Getenv("ODOO_DB"),
		OdooAPIKey:  os.Getenv("ODOO_API_KEY"),
		OdooTimeout: 30 * time.Second, // default

		// Transport
		Transport: getEnvOrDefault("ODOO_MCP_TRANSPORT", "stdio"),
		HTTPAddr:  getEnvOrDefault("ODOO_MCP_ADDR", ":8080"),

		// Auth mode
		AuthMode: getEnvOrDefault("ODOO_MCP_AUTH_MODE", "service"),

		// Guardrails
		ReadOnlyMode: getEnvBool("ODOO_READ_ONLY", false),
		RateLimitRPS: getEnvInt("ODOO_RATE_LIMIT_RPS", 10),

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
	// Check required fields
	if c.OdooURL == "" {
		return fmt.Errorf("ODOO_URL is required")
	}
	if c.OdooDB == "" {
		return fmt.Errorf("ODOO_DB is required")
	}

	// OdooAPIKey is required for service auth mode
	if c.AuthMode == "service" && c.OdooAPIKey == "" {
		return fmt.Errorf("ODOO_API_KEY is required when ODOO_MCP_AUTH_MODE is 'service'")
	}

	// Validate Transport
	validTransports := map[string]bool{
		"stdio": true,
		"http":  true,
		"sse":   true,
	}
	if !validTransports[c.Transport] {
		return fmt.Errorf("invalid ODOO_MCP_TRANSPORT: %q (must be 'stdio', 'http', or 'sse')", c.Transport)
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
