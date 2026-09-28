package firecrawl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// HTTPError — типизированная ошибка HTTP-уровня Firecrawl. Сохраняет код
// статуса, чтобы обёртка бюджета могла различать 429 (повтор) и 402
// (остановка). Текст ошибки сохраняет прежний формат — это обычная ошибка Go,
// не утекающая наружу типом HTTP/SDK.
type HTTPError struct {
	// StatusCode — код HTTP-статуса ответа.
	StatusCode int
	// Method — HTTP-метод запроса.
	Method string
	// URL — адрес запроса.
	URL string
	// Body — обрезанное тело ответа для диагностики.
	Body string
	// RetryAfter — серверная задержка повтора из заголовка Retry-After (0 —
	// заголовок отсутствует или неразборчив).
	RetryAfter time.Duration
}

// Error реализует error и сохраняет читаемый формат прежней строковой ошибки.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("firecrawl: %s %s: статус %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// IsRateLimited сообщает, является ли ошибка ответом HTTP 429.
func IsRateLimited(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.StatusCode == http.StatusTooManyRequests
}

// IsPaymentRequired сообщает, является ли ошибка ответом HTTP 402.
func IsPaymentRequired(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.StatusCode == http.StatusPaymentRequired
}

// IsTransient сообщает, является ли ошибка временным сбоем транспорта или
// сервера: обрыв соединения без ответа (unexpected EOF, connection reset),
// сетевой таймаут или ответ 5xx. Такие сбои имеет смысл повторить.
// Отмена и дедлайн контекста транзиентными не считаются: повтор не должен
// воскрешать отменённое задание. Ответы 4xx (включая 402) — не транзиентные.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if code, ok := StatusCode(err); ok {
		return code >= http.StatusInternalServerError
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// StatusCode возвращает код статуса типизированной ошибки и true, если ошибка
// является *HTTPError.
func StatusCode(err error) (int, bool) {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.StatusCode, true
	}
	return 0, false
}

// RetryAfter возвращает серверную задержку повтора из ошибки (0 — не задана).
// Заголовок Retry-After — источник истины там, где провайдер его отдаёт: он
// точнее локального экспоненциального бэкоффа.
func RetryAfter(err error) time.Duration {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.RetryAfter
	}
	return 0
}

// parseRetryAfterHeader разбирает значение заголовка Retry-After: число секунд
// либо HTTP-дату. Неразборчивое или неположительное значение даёт ноль.
func parseRetryAfterHeader(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if secs, err := strconv.Atoi(value); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil {
		if d := time.Until(when); d > 0 {
			return d
		}
	}
	return 0
}
