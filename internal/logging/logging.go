// Package logging centralizes the process-wide structured logging setup.
package logging

import (
	"io"
	"log"
	"log/slog"
	"os"
	"strings"
)

// Setup configures slog and forwards legacy standard-library log calls to it.
func Setup() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	log.SetFlags(0)
	log.SetOutput(slogWriter{})
}

type slogWriter struct{}

func (slogWriter) Write(p []byte) (int, error) {
	message := strings.TrimSpace(string(p))
	if message != "" {
		slog.Info(message, "legacy_log", true)
	}
	return len(p), nil
}

var _ io.Writer = slogWriter{}
