package firecrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"syscall"
	"testing"
	"time"
)

// TestIsTransient проверяет, что повторяемыми считаются только временные сбои:
// обрыв соединения, сетевой таймаут и ответ 5xx, — а отмена, дедлайн контекста
// и ответы 4xx (включая 402) — нет.
func TestIsTransient(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "unexpected EOF", err: fmt.Errorf("post: %w", io.ErrUnexpectedEOF), want: true},
		{name: "connection reset", err: fmt.Errorf("read: %w", syscall.ECONNRESET), want: true},
		{name: "5xx", err: &HTTPError{StatusCode: http.StatusBadGateway, Method: "POST", URL: "u"}, want: true},
		{name: "429", err: &HTTPError{StatusCode: http.StatusTooManyRequests, Method: "POST", URL: "u"}, want: false},
		{name: "402", err: &HTTPError{StatusCode: http.StatusPaymentRequired, Method: "POST", URL: "u"}, want: false},
		{name: "400", err: &HTTPError{StatusCode: http.StatusBadRequest, Method: "POST", URL: "u"}, want: false},
		{name: "отмена контекста", err: context.Canceled, want: false},
		{name: "дедлайн контекста", err: context.DeadlineExceeded, want: false},
		{name: "простая ошибка", err: errors.New("boom"), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsTransient(tc.err); got != tc.want {
				t.Fatalf("IsTransient(%v) = %v, ожидалось %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestParseRetryAfterHeader проверяет разбор заголовка Retry-After: секунды,
// HTTP-дата и неразборчивые значения.
func TestParseRetryAfterHeader(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  time.Duration
	}{
		{name: "пусто", value: "", want: 0},
		{name: "секунды", value: "30", want: 30 * time.Second},
		{name: "пробелы", value: " 12 ", want: 12 * time.Second},
		{name: "ноль", value: "0", want: 0},
		{name: "отрицательное", value: "-5", want: 0},
		{name: "мусор", value: "soon", want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRetryAfterHeader(tc.value); got != tc.want {
				t.Fatalf("parseRetryAfterHeader(%q) = %v, ожидалось %v", tc.value, got, tc.want)
			}
		})
	}

	// HTTP-дата в прошлом даёт ноль, в будущем — положительную задержку.
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeader(past); got != 0 {
		t.Fatalf("Retry-After в прошлом = %v, ожидался 0", got)
	}
	future := time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeader(future); got <= 0 {
		t.Fatalf("Retry-After в будущем = %v, ожидалось положительное значение", got)
	}
}
