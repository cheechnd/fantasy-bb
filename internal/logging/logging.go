package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
)

func New(level string, w io.Writer) (*slog.Logger, error) {
	if w == nil {
		w = os.Stdout
	}
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}
	handler := slog.NewTextHandler(w, &slog.HandlerOptions{Level: lvl})
	return slog.New(handler), nil
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info", "":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", level)
	}
}

func WithCommand(ctx context.Context, logger *slog.Logger, command string) *slog.Logger {
	if logger == nil {
		logger = slog.Default()
	}
	if command == "" {
		return logger
	}
	return logger.With("command", command)
}
