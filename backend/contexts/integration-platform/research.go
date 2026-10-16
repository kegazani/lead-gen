package integrationplatform

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	acquisition "lead-gen/backend/contexts/integration-platform/internal/adapters/researchacquisition"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

// ResearchAcquisitionConfig собирает отдельный исследовательский маршрут.
// Pool обязан принадлежать клиентской red зоне: сохранённые receipts содержат
// запросы, страницы и контакты. Миграции migrations/red применяет composition.
type ResearchAcquisitionConfig struct {
	Pool          *pgxpool.Pool
	Firecrawl     FirecrawlConfig
	Account       contract.AccountPolicy
	MaxConcurrent int
	MinInterval   time.Duration
	// HTTPClient опционально задаёт транспорт existing proxy и доверие CA.
	HTTPClient *http.Client
}

// NewResearchAcquisition сохраняет policy до возврата клиента; отсутствующие
// key/pool не подменяются заглушкой. Сеть к провайдеру конструктор не запускает.
func NewResearchAcquisition(cfg ResearchAcquisitionConfig) (contract.Port, contract.Ledger, error) {
	if cfg.Pool == nil || strings.TrimSpace(cfg.Firecrawl.APIKey) == "" || cfg.Account.AccountID == "" || cfg.Account.MaxCredits < 0 || cfg.MaxConcurrent < 0 || cfg.MinInterval < 0 {
		return nil, nil, fmt.Errorf("integrationplatform: research acquisition: pool, key, account и допустимые пределы обязательны")
	}
	concurrent := cfg.MaxConcurrent
	if concurrent == 0 {
		concurrent = 1
	}
	store := acquisition.NewStore(cfg.Pool, concurrent, cfg.MinInterval)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := store.EnsureAccount(ctx, cfg.Account); err != nil {
		return nil, nil, fmt.Errorf("integrationplatform: research account: %w", err)
	}
	client := firecrawl.New(firecrawl.Config{APIKey: cfg.Firecrawl.APIKey, BaseURL: cfg.Firecrawl.BaseURL, HTTPClient: cfg.HTTPClient, Logger: cfg.Firecrawl.Logger})
	return acquisition.NewClient(client, store, cfg.Account.AccountID), store, nil
}
