package integrationplatform

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/openai"
)

// NewFirecrawlProbe проверяет доступность учётной записи безопасным GET
// /v2/team/credit-usage. Метод читает баланс, не запускает scrape/crawl/search.
func NewFirecrawlProbe(cfg FirecrawlConfig) AdapterProbe {
	client := firecrawl.New(firecrawl.Config{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Logger: cfg.Logger})
	return func(ctx context.Context) error {
		if err := client.Probe(ctx); err != nil {
			return fmt.Errorf("firecrawl probe failed")
		}
		return nil
	}
}

// NewLLMProbe проверяет реальный OpenAI-compatible GET /models. Completion не
// выполняется; успешный ответ означает доступ к API, а не готовность модели к задаче.
func NewLLMProbe(cfg OpenAIConfig) AdapterProbe {
	client := openai.New(openai.Config{BaseURL: cfg.BaseURL, Model: cfg.Model, APIKey: cfg.APIKey})
	return func(ctx context.Context) error {
		if err := client.Probe(ctx); err != nil {
			return fmt.Errorf("llm probe failed")
		}
		return nil
	}
}

// NewSMTPProbe выполняет bounded connect/EHLO/NOOP с реальными host/port. Не
// выполняет AUTH: существующий SMTP-клиент не договаривается о TLS, поэтому
// использовать пароль в health probe было бы небезопасно.
func NewSMTPProbe(cfg SmtpConfig) AdapterProbe {
	client, err := NewSmtp(cfg)
	return func(ctx context.Context) error {
		if err != nil {
			return fmt.Errorf("smtp probe unavailable")
		}
		if err := client.Probe(ctx); err != nil {
			return fmt.Errorf("smtp probe failed")
		}
		return nil
	}
}

// Проверка, что созданные функции совместимы с публичным портом adapter-status.
var _ AdapterProbe = NewFirecrawlProbe(FirecrawlConfig{})
