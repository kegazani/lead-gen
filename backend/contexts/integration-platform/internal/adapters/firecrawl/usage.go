package firecrawl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const maxProbeResponseBytes = 64 * 1024

// Probe выполняет bounded GET /v2/team/credit-usage: endpoint только читает
// остаток аккаунта и не запускает платную бизнес-операцию. Размер тела ответа
// ограничен отдельно от сетевого timeout.
func (c *Client) Probe(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint("/v2/team/credit-usage"), nil)
	if err != nil {
		return fmt.Errorf("firecrawl: probe failed")
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("firecrawl: probe failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("firecrawl: probe failed")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeResponseBytes+1))
	if err != nil || len(data) > maxProbeResponseBytes {
		return fmt.Errorf("firecrawl: probe failed")
	}
	var usage creditUsageJSON
	if json.Unmarshal(data, &usage) != nil || !usage.Success || usage.Data == nil || usage.Data.RemainingCredits == nil {
		return fmt.Errorf("firecrawl: probe failed")
	}
	return nil
}

// CreditUsage — результат GET /v2/team/credit-usage. Остаток аккаунта берётся
// только из successful-ответа; PlanCredits — объём плана и НЕ является текущим
// остатком.
type CreditUsage struct {
	// RemainingCredits — data.remainingCredits, подтверждённый остаток аккаунта.
	RemainingCredits int
	// PlanCredits — data.planCredits, объём кредитов плана без части доп.
	// начислений; за текущий остаток не выдаётся.
	PlanCredits int
}

// TokenUsage — результат GET /v2/team/token-usage. Поле информационное: не
// участвует в кредитном лимите.
type TokenUsage struct {
	// RemainingTokens — data.remainingTokens.
	RemainingTokens int
}

// creditUsageJSON — форма ответа /v2/team/credit-usage. Поля-указатели, чтобы
// отличить «0 кредитов» (валидное значение) от отсутствующего поля.
type creditUsageJSON struct {
	Success bool `json:"success"`
	Data    *struct {
		RemainingCredits *int `json:"remainingCredits"`
		PlanCredits      *int `json:"planCredits"`
	} `json:"data"`
}

// tokenUsageJSON — форма ответа /v2/team/token-usage.
type tokenUsageJSON struct {
	Success bool `json:"success"`
	Data    *struct {
		RemainingTokens *int `json:"remainingTokens"`
	} `json:"data"`
}

// CreditUsage запрашивает остаток кредитов аккаунта через GET /v2/team/credit-usage.
// Возвращает ошибку, если ответ безуспешен или поле remainingCredits отсутствует.
func (c *Client) CreditUsage(ctx context.Context) (CreditUsage, error) {
	if err := ctx.Err(); err != nil {
		return CreditUsage{}, err
	}
	var raw creditUsageJSON
	if err := c.doJSON(ctx, "GET", c.endpoint("/v2/team/credit-usage"), nil, &raw); err != nil {
		return CreditUsage{}, err
	}
	if !raw.Success || raw.Data == nil || raw.Data.RemainingCredits == nil {
		return CreditUsage{}, fmt.Errorf("firecrawl: credit-usage: ответ без data.remainingCredits")
	}
	usage := CreditUsage{RemainingCredits: *raw.Data.RemainingCredits}
	if raw.Data.PlanCredits != nil {
		usage.PlanCredits = *raw.Data.PlanCredits
	}
	return usage, nil
}

// TokenUsage запрашивает остаток токенов через GET /v2/team/token-usage.
// Возвращает ошибку, если поле remainingTokens отсутствует.
func (c *Client) TokenUsage(ctx context.Context) (TokenUsage, error) {
	if err := ctx.Err(); err != nil {
		return TokenUsage{}, err
	}
	var raw tokenUsageJSON
	if err := c.doJSON(ctx, "GET", c.endpoint("/v2/team/token-usage"), nil, &raw); err != nil {
		return TokenUsage{}, err
	}
	if !raw.Success || raw.Data == nil || raw.Data.RemainingTokens == nil {
		return TokenUsage{}, fmt.Errorf("firecrawl: token-usage: ответ без data.remainingTokens")
	}
	return TokenUsage{RemainingTokens: *raw.Data.RemainingTokens}, nil
}

// usageBalance — внутренний кэшированный остаток с признаком доступности.
type usageBalance struct {
	// Available — true, если есть успешный ответ; false означает «недоступен».
	Available bool
	// Value — значение остатка (кредиты или токены).
	Value int
	// UpdatedAt — локальное время завершения последнего успешного fetch. API
	// provider не возвращает observation timestamp; это поле только для TTL.
	UpdatedAt time.Time
}

// usageSnapshot — внутренний кэш одного показателя.
type usageSnapshot struct {
	value     int
	updatedAt time.Time
}

// Usage — кэш остатка аккаунта (кредиты и токены) с временем последнего
// успешного обновления. Разделяется всеми воркерами процесса.
type Usage struct {
	client *Client
	clock  Clock
	// creditRefresh сериализует весь fetch+commit: медленный старый HTTP-ответ
	// не может перезаписать значение из более позднего параллельного обновления.
	creditRefresh chan struct{}

	mu      sync.Mutex
	credits *usageSnapshot
	tokens  *usageSnapshot
}

// NewUsage собирает кэш остатка поверх существующего клиента Firecrawl.
func NewUsage(client *Client, clock Clock) *Usage {
	return &Usage{client: client, clock: clock, creditRefresh: make(chan struct{}, 1)}
}

// RefreshCredit запрашивает credit-usage и обновляет кэш при успехе. Ошибка не
// сбрасывает последнее успешное значение (оно лишь стареет).
func (u *Usage) RefreshCredit(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case u.creditRefresh <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-u.creditRefresh }()

	usage, err := u.client.CreditUsage(ctx)
	if err != nil {
		return err
	}
	// Firecrawl API не отдаёт timestamp снимка; локальное время fetch применяется
	// только как возраст кэшированной копии, а не выдаётся за время provider read.
	updatedAt := now(u.clock)
	u.mu.Lock()
	u.credits = &usageSnapshot{value: usage.RemainingCredits, updatedAt: updatedAt}
	u.mu.Unlock()
	return nil
}

// RefreshToken запрашивает token-usage и обновляет кэш при успехе.
func (u *Usage) RefreshToken(ctx context.Context) error {
	usage, err := u.client.TokenUsage(ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.tokens = &usageSnapshot{value: usage.RemainingTokens, updatedAt: now(u.clock)}
	u.mu.Unlock()
	return nil
}

// CreditBalance возвращает кэшированный остаток кредитов и признак доступности.
func (u *Usage) CreditBalance() usageBalance {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.credits == nil {
		return usageBalance{}
	}
	return usageBalance{Available: true, Value: u.credits.value, UpdatedAt: u.credits.updatedAt}
}

// TokenBalance возвращает кэшированный остаток токенов и признак доступности.
func (u *Usage) TokenBalance() usageBalance {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.tokens == nil {
		return usageBalance{}
	}
	return usageBalance{Available: true, Value: u.tokens.value, UpdatedAt: u.tokens.updatedAt}
}
