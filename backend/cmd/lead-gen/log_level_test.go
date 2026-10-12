package main

import (
	"log/slog"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		ok   bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"DEBUG", slog.LevelDebug, true},
		{" info ", slog.LevelInfo, true},
		{"trace", 0, false},
		{"", 0, false},
		{"banana", 0, false},
	}
	for _, c := range cases {
		got, err := parseLogLevel(c.in)
		if c.ok && err != nil {
			t.Fatalf("parseLogLevel(%q): неожиданная ошибка %v", c.in, err)
		}
		if !c.ok && err == nil {
			t.Fatalf("parseLogLevel(%q): ожидалась ошибка, получен уровень %v", c.in, got)
		}
		if c.ok && got != c.want {
			t.Fatalf("parseLogLevel(%q) = %v, ожидался %v", c.in, got, c.want)
		}
	}
}
