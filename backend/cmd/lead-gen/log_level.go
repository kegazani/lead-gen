package main

import (
	"fmt"
	"log/slog"
	"strings"
)

// parseLogLevel переводит значение флага -log-level в slog.Level. Поддерживаются
// debug/info/warn/error; неизвестное значение — ошибка конфигурации, а не тихий
// откат к Info (design.md, Решение 3).
func parseLogLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("неизвестный уровень логирования %q (допустимо debug/info/warn/error)", value)
	}
}
