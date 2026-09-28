package firecrawl

import (
	"context"
	"fmt"
	"sync"
	"time"
)

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
	// UpdatedAt — время последнего успешного обновления.
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

	mu      sync.Mutex
	credits *usageSnapshot
	tokens  *usageSnapshot
}

// NewUsage собирает кэш остатка поверх существующего клиента Firecrawl.
func NewUsage(client *Client, clock Clock) *Usage {
	return &Usage{client: client, clock: clock}
}

// RefreshCredit запрашивает credit-usage и обновляет кэш при успехе. Ошибка не
// сбрасывает последнее успешное значение (оно лишь стареет).
func (u *Usage) RefreshCredit(ctx context.Context) error {
	usage, err := u.client.CreditUsage(ctx)
	if err != nil {
		return err
	}
	u.mu.Lock()
	u.credits = &usageSnapshot{value: usage.RemainingCredits, updatedAt: now(u.clock)}
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
