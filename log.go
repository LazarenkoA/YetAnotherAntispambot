package app

import (
	"log/slog"
	"os"
)

var Level = &slog.LevelVar{}

func InitDefaultLogger() {
	// Уровень логирования по умолчанию
	Level.Set(slog.LevelDebug)

	slogHandler := slog.NewJSONHandler(
		os.Stderr,
		&slog.HandlerOptions{
			Level: Level,
		},
	)

	slog.SetDefault(slog.New(slogHandler))
}

func SetLevel(level slog.Level) {
	Level.Set(level)
}
