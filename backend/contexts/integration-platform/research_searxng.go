package integrationplatform

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/researchsearxng"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

// SearXNGResearchConfig — наш настроенный metasearch endpoint. BaseURL доверен
// deployment; пользовательский SearchRequest не может выбирать endpoint.
// Ни кредитного cap, ни Firecrawl grant в конфигурации нет.
type SearXNGResearchConfig struct {
	Pool          *pgxpool.Pool
	BaseURL       string
	HTTPClient    *http.Client
	Engines       []string
	MaxConcurrent int
	MaxAttempts   int
	MinInterval   time.Duration
}

func NewSearXNGResearch(cfg SearXNGResearchConfig) (contract.Port, error) {
	concurrent := cfg.MaxConcurrent
	if concurrent == 0 {
		concurrent = 2
	}
	attempts := cfg.MaxAttempts
	if attempts == 0 {
		attempts = 3
	}
	sum := sha256.Sum256([]byte(strings.TrimRight(cfg.BaseURL, "/")))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	store, err := researchsearxng.NewStore(ctx, cfg.Pool, "searxng/"+hex.EncodeToString(sum[:]), concurrent, attempts, cfg.MinInterval)
	if err != nil {
		return nil, err
	}
	return researchsearxng.NewClient(researchsearxng.Config{BaseURL: cfg.BaseURL, HTTPClient: cfg.HTTPClient, Engines: cfg.Engines}, store)
}
