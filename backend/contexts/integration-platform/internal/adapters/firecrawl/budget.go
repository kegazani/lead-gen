package firecrawl

import (
	"fmt"
	"sync"
)

// BudgetStatus — состояние бюджета задачи.
type BudgetStatus string

const (
	// BudgetActive — бюджет активен и может резервировать кредиты.
	BudgetActive BudgetStatus = "active"
	// BudgetExhausted — подтверждённый расход достиг лимита.
	BudgetExhausted BudgetStatus = "exhausted"
	// BudgetStopped — новые затратные операции остановлены (например, HTTP 402).
	BudgetStopped BudgetStatus = "stopped"
)

// TaskBudget — кредитный бюджет одной задачи. Поля защищены мьютексом: два
// параллельных воркера не могут одновременно зарезервировать одни и те же
// оставшиеся кредиты. Резервирование — верхний предел стоимости операции;
// после завершения вызывающий подтверждает фактический расход через Settle,
// а неиспользованный резерв освобождается.
//
// Бюджет необязателен: maxCredits == 0 означает «бюджет не задан» — кредитный
// лимит не применяется, но фактический расход всё равно измеряется
// (confirmedSpentCredits накапливается, резерв учитывается).
type TaskBudget struct {
	mu sync.Mutex

	// maxCredits — жёсткий лимит кредитов задачи; 0 — бюджет не задан.
	maxCredits int
	// reservedCredits — зарезервированный, но ещё не подтверждённый расход.
	reservedCredits int
	// confirmedSpentCredits — подтверждённый фактический расход.
	confirmedSpentCredits int
	// status — состояние бюджета.
	status BudgetStatus
}

// NewTaskBudget собирает бюджет задачи. maxCredits — неотрицательный лимит:
// 0 означает «бюджет не задан» (без кредитного лимита, но с измерением
// фактического расхода), положительное значение задаёт жёсткий лимит.
// Ошибка — только при отрицательном maxCredits.
func NewTaskBudget(maxCredits int) (*TaskBudget, error) {
	if maxCredits < 0 {
		return nil, fmt.Errorf("firecrawl: бюджет задачи: max_credits не может быть отрицательным, получено %d", maxCredits)
	}
	return &TaskBudget{maxCredits: maxCredits, status: BudgetActive}, nil
}

// Reserve атомарно резервирует amount кредитов под операцию. Проверка
// вписываемости в лимит применяется только когда бюджет задан (maxCredits > 0);
// при maxCredits == 0 лимит не применяется, но резерв всё равно накапливается.
// Возвращает ошибку, если резерв не умещается в заданный бюджет или бюджет
// остановлен.
func (b *TaskBudget) Reserve(amount int) error {
	if amount < 0 {
		return fmt.Errorf("firecrawl: бюджет: резерв не может быть отрицательным")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.status != BudgetActive {
		return fmt.Errorf("firecrawl: бюджет: новые операции остановлены (статус %q)", b.status)
	}
	if b.maxCredits > 0 && b.reservedCredits+b.confirmedSpentCredits+amount > b.maxCredits {
		return fmt.Errorf("firecrawl: бюджет: операция стоимостью %d не умещается в остаток %d", amount, b.availableLocked())
	}
	b.reservedCredits += amount
	return nil
}

// Settle завершает учёт операции: освобождает зарезервированный верхний предел
// reserved и подтверждает фактический расход actual. Неиспользованный резерв
// (reserved - actual) возвращается в доступный остаток автоматически.
func (b *TaskBudget) Settle(reserved, actual int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reserved < 0 {
		reserved = 0
	}
	if actual < 0 {
		actual = 0
	}
	if reserved > b.reservedCredits {
		reserved = b.reservedCredits
	}
	if actual > reserved {
		actual = reserved
	}
	b.reservedCredits -= reserved
	b.confirmedSpentCredits += actual
	b.refreshStatusLocked()
}

// Stop переводит бюджет в состояние «остановлен»: новые затратные операции
// запрещены (например, после HTTP 402).
func (b *TaskBudget) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = BudgetStopped
}

// BudgetSnapshot — непротиворечивый снимок бюджета для отображения.
// MaxCredits == 0 означает «бюджет не задан» (кредитный лимит не применяется).
type BudgetSnapshot struct {
	MaxCredits            int
	ReservedCredits       int
	ConfirmedSpentCredits int
	Available             int
	Status                BudgetStatus
}

// Snapshot возвращает согласованный снимок бюджета под блокировкой.
func (b *TaskBudget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{
		MaxCredits:            b.maxCredits,
		ReservedCredits:       b.reservedCredits,
		ConfirmedSpentCredits: b.confirmedSpentCredits,
		Available:             b.availableLocked(),
		Status:                b.status,
	}
}

// availableLocked — доступный остаток (max - reserved - confirmed). При незаданном
// бюджете (maxCredits <= 0) возвращает 0: значение неиспользуемо, потребитель
// обязан учитывать MaxCredits перед применением доступного остатка. Вызывается
// только под блокировкой.
func (b *TaskBudget) availableLocked() int {
	if b.maxCredits <= 0 {
		return 0
	}
	left := b.maxCredits - b.reservedCredits - b.confirmedSpentCredits
	if left < 0 {
		return 0
	}
	return left
}

// refreshStatusLocked — пересчитывает статус после изменения счётчиков. При
// незаданном бюджете (maxCredits <= 0) бюджет не становится exhausted: лимита
// нет, статус остаётся active (кроме stopped, который не сбрасывается).
func (b *TaskBudget) refreshStatusLocked() {
	if b.status == BudgetStopped {
		return
	}
	if b.maxCredits > 0 && b.confirmedSpentCredits >= b.maxCredits {
		b.status = BudgetExhausted
		return
	}
	b.status = BudgetActive
}

// Registry — реестр бюджетов задач по идентификатору, общий для всех воркеров
// процесса. Доступ защищён мьютексом.
type Registry struct {
	mu   sync.Mutex
	byID map[string]*TaskBudget
}

// NewRegistry собирает пустой реестр бюджетов.
func NewRegistry() *Registry {
	return &Registry{byID: make(map[string]*TaskBudget)}
}

// Get возвращает существующий бюджет задачи. ok=false, если бюджет не создан.
func (r *Registry) Get(id string) (*TaskBudget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b, ok := r.byID[id]
	return b, ok
}

// GetOrCreate возвращает существующий бюджет или создаёт новый с заданным
// лимитом. Ошибка — при отрицательном maxCredits; 0 создаёт безлимитный, но
// измеряющий фактический расход бюджет.
func (r *Registry) GetOrCreate(id string, maxCredits int) (*TaskBudget, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.byID[id]; ok {
		return b, nil
	}
	b, err := NewTaskBudget(maxCredits)
	if err != nil {
		return nil, err
	}
	r.byID[id] = b
	return b, nil
}
