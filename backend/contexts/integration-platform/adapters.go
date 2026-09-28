package integrationplatform

import (
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/forum"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/openai"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/smtp"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/search"
)

// FirecrawlConfig — конфигурация адаптера Firecrawl, публикуемая корневым
// пакетом. Внутренние детали (HTTP-клиент, таймауты) сюда не протекают: они
// настраиваются внутри адаптера.
type FirecrawlConfig struct {
	// APIKey — ключ доступа Firecrawl. Обязателен.
	APIKey string
	// BaseURL — базовый адрес API. Пустое значение заменяется на адрес по
	// умолчанию (https://api.firecrawl.dev).
	BaseURL string
	// Logger — опциональный логгер безопасных параметров запроса. nil — без логов.
	Logger observ.Logger
}

// OpenAIConfig — конфигурация адаптера OpenAI-совместимого клиента (DeepSeek).
type OpenAIConfig struct {
	// BaseURL — базовый адрес API. Пустое значение заменяется на адрес по
	// умолчанию (https://api.deepseek.com).
	BaseURL string
	// Model — идентификатор модели. Пустое значение заменяется на модель по
	// умолчанию (deepseek-chat).
	Model string
	// APIKey — ключ доступа. Обязателен.
	APIKey string
}

// NewFirecrawl собирает адаптер Firecrawl, реализующий search.Port и crawl.Port.
// Возвращает ошибку, если не задан ключ доступа.
func NewFirecrawl(cfg FirecrawlConfig) (*firecrawl.Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, fmt.Errorf("integrationplatform: firecrawl: APIKey обязателен")
	}
	return firecrawl.New(firecrawl.Config{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Logger:  cfg.Logger,
	}), nil
}

// FirecrawlGuardConfig — конфигурация бюджетной обёртки Firecrawl. Бюджет на
// задачу задаётся потребителем при вызове Guard.Begin (maxCredits), поэтому
// сюда выносится только общая настройка лимитера и повторов.
type FirecrawlGuardConfig struct {
	// APIKey — ключ доступа Firecrawl. Обязателен.
	APIKey string
	// BaseURL — базовый адрес API. Пустое значение — адрес по умолчанию.
	BaseURL string
	// Logger — опциональный логгер безопасных параметров запроса. nil — без логов.
	Logger observ.Logger
	// MaxConcurrent — максимум одновременных затратных запросов. Ноль — значение
	// по умолчанию (1).
	MaxConcurrent int
	// MinInterval — минимальная пауза между обычными запросами. Ноль — 10 секунд.
	MinInterval time.Duration
	// CrawlInterval — минимальная пауза между запусками crawl. Ноль — 40 секунд.
	CrawlInterval time.Duration
	// BalanceTTL — срок свежести остатка аккаунта. Ноль — 1 минута.
	BalanceTTL time.Duration
	// MaxRetries — число повторов при HTTP 429. Ноль — 3.
	MaxRetries int
	// RetryBase — базовая задержка повтора. Ноль — 1 секунда.
	RetryBase time.Duration
}

// NewFirecrawlGuard собирает бюджетную обёртку Firecrawl, реализующую
// budget.Factory. Возвращает ошибку, если не задан ключ доступа.
func NewFirecrawlGuard(cfg FirecrawlGuardConfig) (*firecrawl.Guard, error) {
	client, err := NewFirecrawl(FirecrawlConfig{APIKey: cfg.APIKey, BaseURL: cfg.BaseURL, Logger: cfg.Logger})
	if err != nil {
		return nil, err
	}
	limiter := firecrawl.NewLimiter(firecrawl.LimiterConfig{
		MaxConcurrent: cfg.MaxConcurrent,
		MinInterval:   cfg.MinInterval,
		CrawlInterval: cfg.CrawlInterval,
	})
	return firecrawl.NewGuard(firecrawl.GuardConfig{
		Client:     client,
		Limiter:    limiter,
		BalanceTTL: cfg.BalanceTTL,
		MaxRetries: cfg.MaxRetries,
		RetryBase:  cfg.RetryBase,
	}), nil
}

// NewOpenAI собирает адаптер OpenAI-совместимого клиента, реализующий llm.Port.
func NewOpenAI(cfg OpenAIConfig) *openai.Client {
	return openai.New(openai.Config{
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
		APIKey:  cfg.APIKey,
	})
}

// ForumConfig — конфигурация адаптера форумного источника, публикуемая корневым
// пакетом. Адаптер оборачивает поисковый движок и ограничивает его доменами
// форумов.
type ForumConfig struct {
	// Search — поисковый движок, которым ищутся темы и обсуждения. Обязателен.
	Search search.Port
	// Domains — домены форумов, по которым ограничивается поиск. Пустой список
	// заменяется на список по умолчанию.
	Domains []string
}

// NewForum собирает адаптер форумного источника, реализующий forum.Port.
func NewForum(cfg ForumConfig) *forum.Client {
	return forum.New(forum.Config{
		Search:  cfg.Search,
		Domains: cfg.Domains,
	})
}

// SmtpConfig — конфигурация SMTP-адаптера, публикуемая корневым пакетом.
// Внутренние детали (таймаут подключения) настраиваются внутри адаптера.
type SmtpConfig struct {
	// Host — адрес SMTP-сервера. Обязателен.
	Host string
	// Port — порт SMTP-сервера. Ноль означает порт по умолчанию адаптера.
	Port int
	// Username — имя учётной записи. Пустое значение отключает аутентификацию.
	Username string
	// Password — пароль учётной записи.
	Password string
}

// NewSmtp собирает SMTP-адаптер, реализующий mail.Port. Возвращает ошибку, если
// не задан хост.
func NewSmtp(cfg SmtpConfig) (*smtp.Client, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return nil, fmt.Errorf("integrationplatform: smtp: Host обязателен")
	}
	return smtp.New(smtp.Config{
		Host:     cfg.Host,
		Port:     cfg.Port,
		Username: cfg.Username,
		Password: cfg.Password,
	}), nil
}
