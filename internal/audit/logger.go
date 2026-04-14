package audit

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"
)

// Entry is a single audit log record.
type Entry struct {
	Timestamp  time.Time `json:"ts"`
	SessionID  string    `json:"session_id"`
	Tool       string    `json:"tool"`
	Model      string    `json:"model,omitempty"`
	Method     string    `json:"method,omitempty"`
	IDs        []int64   `json:"ids,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Success    bool      `json:"success"`
	Error      string    `json:"error,omitempty"`
	UserLogin  string    `json:"user_login,omitempty"`
}

// Logger writes structured audit entries.
type Logger struct {
	log *slog.Logger
}

// New creates an audit Logger. If filePath is empty, writes to stdout.
func New(filePath string) (*Logger, error) {
	var w io.Writer = os.Stdout
	if filePath != "" {
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return nil, fmt.Errorf("open audit log file %s: %w", filePath, err)
		}
		w = f
	}
	return &Logger{
		log: slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		})),
	}, nil
}

// Log writes an audit entry. Call this after every tool invocation completes.
func (l *Logger) Log(ctx context.Context, e Entry) {
	attrs := []slog.Attr{
		slog.String("session_id", e.SessionID),
		slog.String("tool", e.Tool),
		slog.Int64("duration_ms", e.DurationMS),
		slog.Bool("success", e.Success),
	}
	if e.Model != "" {
		attrs = append(attrs, slog.String("model", e.Model))
	}
	if e.Method != "" {
		attrs = append(attrs, slog.String("method", e.Method))
	}
	if len(e.IDs) > 0 {
		attrs = append(attrs, slog.Any("ids", e.IDs))
	}
	if e.Error != "" {
		attrs = append(attrs, slog.String("error", e.Error))
	}
	if e.UserLogin != "" {
		attrs = append(attrs, slog.String("user_login", e.UserLogin))
	}

	l.log.LogAttrs(ctx, slog.LevelInfo, "audit", attrs...)
}

// NoopLogger is an audit logger that discards all entries (for when audit is disabled).
type NoopLogger struct{}

func (n *NoopLogger) Log(_ context.Context, _ Entry) {}

// AuditLogger is the interface for audit logging.
type AuditLogger interface {
	Log(ctx context.Context, e Entry)
}
