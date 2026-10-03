package workflow

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

// Значения бюджета ретрая подключения по умолчанию.
const (
	// DefaultConnectAttempts — число попыток подключения к Temporal.
	DefaultConnectAttempts = 5

	// DefaultConnectInitialBackoff — начальная задержка между попытками.
	DefaultConnectInitialBackoff = 500 * time.Millisecond

	// DefaultConnectMaxBackoff — потолок возрастающей задержки.
	DefaultConnectMaxBackoff = 30 * time.Second
)

// RetryConfig — бюджет ретрая подключения к рантайму исполнения.
type RetryConfig struct {
	// Attempts — число попыток, включая первую. Меньше 1 трактуется как 1.
	Attempts int

	// InitialBackoff — задержка перед второй попыткой; далее удваивается до
	// MaxBackoff.
	InitialBackoff time.Duration

	// MaxBackoff — потолок задержки. Ноль — значение по умолчанию.
	MaxBackoff time.Duration
}

// WithDefaults заполняет нулевые поля значениями по умолчанию.
func (c RetryConfig) WithDefaults() RetryConfig {
	if c.Attempts < 1 {
		c.Attempts = DefaultConnectAttempts
	}
	if c.InitialBackoff <= 0 {
		c.InitialBackoff = DefaultConnectInitialBackoff
	}
	if c.MaxBackoff <= 0 {
		c.MaxBackoff = DefaultConnectMaxBackoff
	}
	return c
}

// DialWithRetry подключается к Temporal, повторяя попытки с возрастающей
// задержкой в пределах бюджета. Транзиентный сбой не помечает рантайм выключенным
// навсегда: при первой удаче возвращается клиент. Конфигурационная ошибка
// (ErrNotConfigured) не ретраится — повтор её не исправит.
func DialWithRetry(ctx context.Context, cfg Config, metrics observ.Metrics, retry RetryConfig) (*Client, error) {
	return dialWithRetry(ctx, retry, func(ctx context.Context) (*Client, error) {
		return Dial(ctx, cfg, metrics)
	})
}

// dialWithRetry — проверяемая сердцевина ретрая: dial инкапсулирует одну попытку.
func dialWithRetry(ctx context.Context, retry RetryConfig, dial func(context.Context) (*Client, error)) (*Client, error) {
	retry = retry.WithDefaults()
	backoff := retry.InitialBackoff
	var lastErr error
	for attempt := 1; attempt <= retry.Attempts; attempt++ {
		client, err := dial(ctx)
		if err == nil {
			return client, nil
		}
		if errors.Is(err, ErrNotConfigured) {
			return nil, err
		}
		lastErr = err
		if attempt == retry.Attempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("workflow: подключение к Temporal прервано: %w", ctx.Err())
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > retry.MaxBackoff {
			backoff = retry.MaxBackoff
		}
	}
	return nil, fmt.Errorf("workflow: подключение к Temporal не удалось за %d попыток: %w", retry.Attempts, lastErr)
}
