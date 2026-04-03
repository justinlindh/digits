package logging

import (
	"log/slog"
	"os"

	"github.com/lmittmann/tint"
)

// Setup configures the default slog logger.
// LOG_FORMAT=json uses JSON output for production; otherwise uses colorized tint.
func Setup() {
	var h slog.Handler
	if os.Getenv("LOG_FORMAT") == "json" {
		h = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	} else {
		h = tint.NewHandler(os.Stderr, &tint.Options{
			Level:      slog.LevelInfo,
			TimeFormat: "15:04:05",
		})
	}
	slog.SetDefault(slog.New(h))
}
