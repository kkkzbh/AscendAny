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

	handler := slog.NewJSONHandler(output, &slog.HandlerOptions{
		Level:       parsed,
		ReplaceAttr: canonicalAttribute,
	})
	return slog.New(handler), nil
}

func canonicalAttribute(_ []string, attribute slog.Attr) slog.Attr {
	if attribute.Key == slog.TimeKey && attribute.Value.Kind() == slog.KindTime {
		attribute.Value = slog.TimeValue(attribute.Value.Time().UTC())
	}
	return attribute
}
