// Package budget — контракт бюджета затратных операций интеграционной
// платформы: фабрика бюджета задачи и бюджетная обёртка, встраивающая порты
// поиска и обхода.
//
// Контракт выражен интерфейсами и DTO: потребитель (например, lead-management)
// держит только этот пакет и не импортирует адаптер Firecrawl. Реализация —
// обёртка бюджета в backend/contexts/integration-platform.
package budget

import (
	"time"

	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// Snapshot — состояние бюджета задачи для отображения: лимит, подтверждённый
// расход, зарезервированный бюджет и доступный остаток. Бюджет необязателен:
// MaxCredits == 0 означает «бюджет не задан» — кредитный лимит не применяется,
// при этом ConfirmedSpentCredits продолжает фиксировать фактический расход.
type Snapshot struct {
	// MaxCredits — жёсткий лимит кредитов задачи; 0 означает «бюджет не задан».
	MaxCredits int `json:"max_credits"`
	// ConfirmedSpentCredits — подтверждённый фактический расход.
	ConfirmedSpentCredits int `json:"confirmed_spent_credits"`
	// ReservedCredits — зарезервированный, но ещё не подтверждённый бюджет.
	ReservedCredits int `json:"reserved_credits"`
	// Available — доступно в рамках задачи (лимит минус расход и резерв). При
	// незаданном бюджете равно 0 и не должно использоваться как ограничение:
	// потребитель сначала проверяет MaxCredits.
	Available int `json:"available"`
	// Status — состояние бюджета: active, exhausted или stopped.
	Status string `json:"status"`
}

// Factory — фабрика бюджета задачи: создаёт бюджетную обёртку операций для
// одного задания. Бюджет необязателен: maxCredits == 0 означает «бюджет не
// задан» и не подставляет значение по умолчанию.
type Factory interface {
	// Begin создаёт (или возвращает существующий) бюджет задачи taskID. maxCredits
	// > 0 задаёт жёсткий лимит; 0 — без кредитного лимита, но с измерением
	// фактического расхода. Отрицательное значение — ошибка.
	Begin(taskID string, maxCredits int) (Task, error)
}

// Task — бюджетная обёртка операций одной задачи. Встраивает порты поиска и
// обхода: каждый затратный вызов проходит через бюджет. При незаданном бюджете
// вызовы не ограничиваются лимитом, но их фактический расход учитывается в
// Snapshot.ConfirmedSpentCredits.
type Task interface {
	search.Port
	crawl.Port
	// Snapshot возвращает текущее состояние бюджета задачи.
	Snapshot() Snapshot
}

// Balance — подтверждённое значение остатка (кредиты или токены) с временем
// последнего успешного обновления. Available=false означает «недоступен».
type Balance struct {
	Available bool
	Value     int
	UpdatedAt time.Time
}

// AccountReader — читает остаток аккаунта Firecrawl и информационное поле
// токенов. Значения берутся только из успешных ответов credit-usage/token-usage.
type AccountReader interface {
	// CreditBalance возвращает остаток кредитов аккаунта.
	CreditBalance() Balance
	// TokenBalance возвращает остаток токенов (информационно).
	TokenBalance() Balance
}
