package workflow

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDialWithRetryRetriesTransientFailure проверяет, что транзиентный сбой
// подключения не помечает рантайм выключенным навсегда: следующая попытка
// успешна, и клиент возвращается вызывающему.
func TestDialWithRetryRetriesTransientFailure(t *testing.T) {
	attempts := 0
	client, err := dialWithRetry(context.Background(), RetryConfig{Attempts: 3, InitialBackoff: time.Microsecond, MaxBackoff: time.Millisecond},
		func(context.Context) (*Client, error) {
			attempts++
			if attempts < 3 {
				return nil, errors.New("транзиентный сбой")
			}
			return &Client{}, nil
		})
	if err != nil {
		t.Fatalf("подключение с ретраем: %v", err)
	}
	if client == nil {
		t.Fatal("клиент не возвращён")
	}
	if attempts != 3 {
		t.Fatalf("попыток = %d, ожидалось 3", attempts)
	}
}

// TestDialWithRetryExhaustedBudget проверяет исход при исчерпании бюджета: клиент
// не возвращается, а ошибка называет число попыток.
func TestDialWithRetryExhaustedBudget(t *testing.T) {
	attempts := 0
	_, err := dialWithRetry(context.Background(), RetryConfig{Attempts: 2, InitialBackoff: time.Microsecond, MaxBackoff: time.Millisecond},
		func(context.Context) (*Client, error) {
			attempts++
			return nil, errors.New("рантайм недоступен")
		})
	if err == nil {
		t.Fatal("ожидалась ошибка исчерпания бюджета")
	}
	if attempts != 2 {
		t.Fatalf("попыток = %d, ожидалось 2", attempts)
	}
}

// TestDialWithRetryDoesNotRetryConfigError проверяет, что конфигурационная ошибка
// не ретраится: повтор её не исправит.
func TestDialWithRetryDoesNotRetryConfigError(t *testing.T) {
	attempts := 0
	_, err := dialWithRetry(context.Background(), RetryConfig{Attempts: 5, InitialBackoff: time.Microsecond},
		func(context.Context) (*Client, error) {
			attempts++
			return nil, ErrNotConfigured
		})
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotConfigured", err)
	}
	if attempts != 1 {
		t.Fatalf("попыток = %d, ожидалась 1", attempts)
	}
}

// TestRetryConfigWithDefaults проверяет подстановку значений бюджета ретрая.
func TestRetryConfigWithDefaults(t *testing.T) {
	cfg := RetryConfig{}.WithDefaults()
	if cfg.Attempts != DefaultConnectAttempts {
		t.Fatalf("попыток = %d, ожидалось %d", cfg.Attempts, DefaultConnectAttempts)
	}
	if cfg.InitialBackoff != DefaultConnectInitialBackoff {
		t.Fatalf("начальная задержка = %v", cfg.InitialBackoff)
	}
	if cfg.MaxBackoff != DefaultConnectMaxBackoff {
		t.Fatalf("потолок задержки = %v", cfg.MaxBackoff)
	}
}
