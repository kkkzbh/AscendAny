package logging

import (
	"fmt"
	"io"
	"log/slog"
)

func New(output io.Writer, level string) (*slog.Logger, error) {
	if output == nil {
		return nil, fmt.Errorf("log output is required")
	}

	var parsed slog.Level
	switch level {
	case "debug":
		parsed = slog.LevelDebug
	case "info":
		parsed = slog.LevelInfo
	case "warn":
		parsed = slog.LevelWarn
	case "error":
		parsed = slog.LevelError
	default:
		return nil, fmt.Errorf("unsupported log level %q", level)
	}

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{Level: parsed})
	return slog.New(handler), nil
}
