// Package budget — контракт бюджета затратных операций интеграционной
// платформы: фабрика бюджета задачи и бюджетная обёртка, встраивающая порты
// поиска и обхода.
//
// Контракт выражен интерфейсами и DTO: потребитель (например, lead-management)
// держит только этот пакет и не импортирует адаптер Firecrawl. Реализация —
// обёртка бюджета в backend/contexts/integration-platform.
package budget

import (
	"context"
	"errors"
	"time"

	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

var (
	// ErrRegistryCapacity — реестр не может принять новый scope без вытеснения
	// незавершённых или ещё удерживаемых записей.
	ErrRegistryCapacity = errors.New("budget: реестр задач заполнен")
	// ErrTaskClosed — task scope уже переведён в terminal lifecycle и не принимает
	// новые платные действия.
	ErrTaskClosed = errors.New("budget: scope задачи закрывается или завершён")
	// ErrFinishPending — Finish начал закрытие, но ожидает завершения in-flight
	// операции.
	ErrFinishPending = errors.New("budget: завершение задачи ожидает in-flight операции")
	// ErrUnknownOutcomeUnreconciled — точного provider operation evidence для
	// разрешения неизвестного резерва нет. Aggregate account balance недостаточен.
	ErrUnknownOutcomeUnreconciled = errors.New("budget: для неизвестного исхода нет точного provider operation evidence")
)

// Snapshot — состояние бюджета задачи для отображения: лимит, подтверждённый и
// консервативно reconciled расход, зарезервированный бюджет и доступный остаток.
// Бюджет необязателен:
// MaxCredits == 0 означает «бюджет не задан» — кредитный лимит не применяется,
// при этом ConfirmedSpentCredits продолжает фиксировать фактический расход.
type Snapshot struct {
	// MaxCredits — жёсткий лимит кредитов задачи; 0 означает «бюджет не задан».
	MaxCredits int `json:"max_credits"`
	// ConfirmedSpentCredits — подтверждённый фактический расход.
	ConfirmedSpentCredits int `json:"confirmed_spent_credits"`
	// ReservedCredits — зарезервированный, но ещё не подтверждённый бюджет.
	ReservedCredits int `json:"reserved_credits"`
	// UnknownCredits — часть резерва с неизвестным сетевым исходом.
	UnknownCredits int `json:"unknown_credits"`
	// OutstandingReservations — число зарезервированных операций, ещё не пришедших
	// к известному settlement/unknown исходу.
	OutstandingReservations int `json:"outstanding_reservations"`
	// Available — доступно в рамках задачи (лимит минус подтверждённый расход и
	// резервы). При
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
	// Finish запрашивает terminal settlement. Outstanding operations завершаются
	// перед изменением статуса; unknown исход не разрешается aggregate account
	// balance refresh, остаётся pinned и возвращает ErrUnknownOutcomeUnreconciled.
	Finish(ctx context.Context, taskID string) error
}

// RegistryStatus — process-local pressure snapshot реестра бюджетных задач.
type RegistryStatus struct {
	Entries                 int   `json:"entries"`
	Capacity                int   `json:"capacity"`
	ActiveScopes            int   `json:"active_scopes"`
	FinishedScopes          int   `json:"finished_scopes"`
	OutstandingReservations int   `json:"outstanding_reservations"`
	UnknownReservations     int   `json:"unknown_reservations"`
	CapacityRejections      int64 `json:"capacity_rejections"`
}

// RegistryStatusReader — необязательная read-поверхность Guard для мониторинга
// заполнения process-local task registry.
type RegistryStatusReader interface {
	RegistryStatus() RegistryStatus
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

// Balance — подтверждённое значение остатка (кредиты или токены) с локальным
// временем успешного получения ответа. UpdatedAt не является provider observation
// timestamp, если источник его отдельно не передал. Available=false означает
// «недоступен».
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
