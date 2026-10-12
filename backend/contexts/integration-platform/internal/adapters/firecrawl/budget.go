package firecrawl

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"lead-gen/contracts/integration-platform/budget"
)

const (
	// defaultRegistryCapacity — ограничение числа активных и удерживаемых task scopes.
	defaultRegistryCapacity = 2048
	// defaultFinishedTTL — время replay завершённого task ID до безопасного eviction.
	defaultFinishedTTL = 15 * time.Minute
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
	// inflightReservations — операции, которые ещё не вернули known/unknown исход.
	inflightReservations int
	// unknown — резервы сетевых операций с неопределённым исходом.
	unknown []unknownReservation
	// status — состояние бюджета.
	status BudgetStatus
	// finishRequested блокирует новые операции с момента вызова Factory.Finish.
	finishRequested bool
	// finished отличает terminal state от времени, которое может быть zero-value
	// при впрыснутых тестовых часах.
	finished bool
	// finishedAt устанавливается только при terminal settled lifecycle.
	finishedAt time.Time
	// changed уведомляет Finish об изменении числа in-flight операций.
	changed chan struct{}
	clock   Clock
}

type unknownReservation struct {
	credits int
}

// NewTaskBudget собирает бюджет задачи. maxCredits — неотрицательный лимит:
// 0 означает «бюджет не задан» (без кредитного лимита, но с измерением
// фактического расхода), положительное значение задаёт жёсткий лимит.
// Ошибка — только при отрицательном maxCredits.
func NewTaskBudget(maxCredits int, clocks ...Clock) (*TaskBudget, error) {
	if maxCredits < 0 {
		return nil, fmt.Errorf("firecrawl: бюджет задачи: max_credits не может быть отрицательным, получено %d", maxCredits)
	}
	var clock Clock
	if len(clocks) > 0 {
		clock = clocks[0]
	}
	return &TaskBudget{maxCredits: maxCredits, status: BudgetActive, changed: make(chan struct{}), clock: clock}, nil
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
	if b.finishRequested {
		return budget.ErrTaskClosed
	}
	if b.maxCredits > 0 && b.reservedCredits+b.confirmedSpentCredits+amount > b.maxCredits {
		return fmt.Errorf("firecrawl: бюджет: операция стоимостью %d не умещается в остаток %d", amount, b.availableLocked())
	}
	b.reservedCredits += amount
	b.inflightReservations++
	b.signalChangedLocked()
	return nil
}

// Settle завершает учёт операции только при подтверждённом исходе. Неиспользованный
// резерв освобождается, фактический расход не обрезается до резерва. При
// превышении верхней границы расход фиксируется полностью, а бюджет останавливается.
// При ошибке аргументов состояние не меняется, поэтому резерв остаётся занятым.
func (b *TaskBudget) Settle(reserved, actual int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reserved <= 0 || actual < 0 || b.inflightReservations <= 0 {
		return fmt.Errorf("firecrawl: бюджет: некорректное подтверждение резерва/расхода")
	}
	if reserved > b.reservedCredits {
		return fmt.Errorf("firecrawl: бюджет: подтверждается резерв %d при активном резерве %d", reserved, b.reservedCredits)
	}
	b.reservedCredits -= reserved
	b.inflightReservations--
	b.confirmedSpentCredits += actual
	if actual > reserved {
		b.status = BudgetStopped
		b.finishIfSettledLocked()
		b.signalChangedLocked()
		return fmt.Errorf("firecrawl: подтверждённый расход %d превысил резерв %d", actual, reserved)
	}
	b.refreshStatusLocked()
	b.finishIfSettledLocked()
	b.signalChangedLocked()
	return nil
}

// MarkUnknown переводит in-flight reservation в закреплённый unknown outcome,
// не освобождая верхний резерв.
func (b *TaskBudget) MarkUnknown(reserved int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if reserved <= 0 || b.inflightReservations <= 0 || reserved > b.reservedCredits {
		return fmt.Errorf("firecrawl: бюджет: некорректная unknown reservation")
	}
	b.inflightReservations--
	b.unknown = append(b.unknown, unknownReservation{credits: reserved})
	b.status = BudgetStopped
	b.signalChangedLocked()
	return nil
}

// CanStart повторно проверяет admission после ожидания общего limiter: пока
// запрос ждал, другой вызов мог остановить task или Finish мог закрыть scope.
func (b *TaskBudget) CanStart() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status == BudgetActive && !b.finishRequested
}

// RequestFinish закрывает scope от новых резервов. Завершённое время ставится
// лишь после settlement всех in-flight и unknown reservations.
func (b *TaskBudget) RequestFinish() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.finishRequested = true
	b.finishIfSettledLocked()
	b.signalChangedLocked()
}

func (b *TaskBudget) WaitForNoInflight(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		b.mu.Lock()
		if b.inflightReservations == 0 {
			b.mu.Unlock()
			return nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (b *TaskBudget) lifecycleSnapshot() (inflight, unknown int, finished bool, finishedAt time.Time, reserved int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inflightReservations, len(b.unknown), b.finished, b.finishedAt, b.reservedCredits
}

func (b *TaskBudget) signalChangedLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *TaskBudget) finishIfSettledLocked() {
	if b.finishRequested && b.inflightReservations == 0 && len(b.unknown) == 0 && b.reservedCredits == 0 && !b.finished {
		b.finished = true
		b.finishedAt = now(b.clock)
	}
}

// Stop переводит бюджет в состояние «остановлен»: новые затратные операции
// запрещены (например, после HTTP 402).
func (b *TaskBudget) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = BudgetStopped
	b.signalChangedLocked()
}

// BudgetSnapshot — непротиворечивый снимок бюджета для отображения.
// MaxCredits == 0 означает «бюджет не задан» (кредитный лимит не применяется).
type BudgetSnapshot struct {
	MaxCredits              int
	ReservedCredits         int
	ConfirmedSpentCredits   int
	UnknownCredits          int
	OutstandingReservations int
	Available               int
	Status                  BudgetStatus
}

// Snapshot возвращает согласованный снимок бюджета под блокировкой.
func (b *TaskBudget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{
		MaxCredits:              b.maxCredits,
		ReservedCredits:         b.reservedCredits,
		ConfirmedSpentCredits:   b.confirmedSpentCredits,
		UnknownCredits:          b.unknownCreditsLocked(),
		OutstandingReservations: b.inflightReservations,
		Available:               b.availableLocked(),
		Status:                  b.status,
	}
}

func (b *TaskBudget) unknownCreditsLocked() int {
	total := 0
	for _, item := range b.unknown {
		total += item.credits
	}
	return total
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

// RegistryConfig ограничивает process-local число scope и retention завершённых
// записей. Это operational bounds, не денежный cap.
type RegistryConfig struct {
	MaxEntries  int
	FinishedTTL time.Duration
	Clock       Clock
}

// Registry — ограниченный реестр бюджетов задач, общий для воркеров процесса.
type Registry struct {
	mu                 sync.Mutex
	byID               map[string]*TaskBudget
	maxEntries         int
	finishedTTL        time.Duration
	clock              Clock
	capacityRejections int64
}

// NewRegistry собирает пустой bounded registry. Нулевая настройка использует
// конечный default; отрицательная конфигурация считается ошибкой.
func NewRegistry(cfg RegistryConfig) (*Registry, error) {
	if cfg.MaxEntries < 0 || cfg.FinishedTTL < 0 {
		return nil, fmt.Errorf("firecrawl: registry capacity и retention не могут быть отрицательными")
	}
	if cfg.MaxEntries == 0 {
		cfg.MaxEntries = defaultRegistryCapacity
	}
	if cfg.FinishedTTL == 0 {
		cfg.FinishedTTL = defaultFinishedTTL
	}
	return &Registry{
		byID:        make(map[string]*TaskBudget),
		maxEntries:  cfg.MaxEntries,
		finishedTTL: cfg.FinishedTTL,
		clock:       cfg.Clock,
	}, nil
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
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("firecrawl: budget task ID обязателен")
	}
	r.evictExpiredLocked(now(r.clock))
	if b, ok := r.byID[id]; ok {
		return b, nil
	}
	if len(r.byID) >= r.maxEntries {
		r.capacityRejections++
		return nil, budget.ErrRegistryCapacity
	}
	b, err := NewTaskBudget(maxCredits, r.clock)
	if err != nil {
		return nil, err
	}
	r.byID[id] = b
	return b, nil
}

func (r *Registry) get(id string) (*TaskBudget, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictExpiredLocked(now(r.clock))
	b, ok := r.byID[id]
	return b, ok
}

func (r *Registry) evictExpiredLocked(at time.Time) {
	for id, b := range r.byID {
		inflight, unknown, finished, finishedAt, reserved := b.lifecycleSnapshot()
		if !finished || inflight != 0 || unknown != 0 || reserved != 0 {
			continue
		}
		if at.Sub(finishedAt) >= r.finishedTTL {
			delete(r.byID, id)
		}
	}
}

// Status возвращает pressure snapshot, включая число capacity отказов.
func (r *Registry) Status() budget.RegistryStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.evictExpiredLocked(now(r.clock))
	status := budget.RegistryStatus{
		Entries: r.byIDCountLocked(), Capacity: r.maxEntries,
		CapacityRejections: r.capacityRejections,
	}
	for _, b := range r.byID {
		inflight, unknown, finished, _, _ := b.lifecycleSnapshot()
		if finished {
			status.FinishedScopes++
		} else {
			status.ActiveScopes++
		}
		status.OutstandingReservations += inflight
		status.UnknownReservations += unknown
	}
	return status
}

func (r *Registry) byIDCountLocked() int { return len(r.byID) }

// Finish requests terminal settlement and waits for outstanding calls. Aggregate
// account balance cannot attribute a debit to a task operation, so unknown scopes
// remain pinned until exact provider operation evidence exists. Finish and TTL
// never clear an unknown reservation.
func (r *Registry) Finish(ctx context.Context, taskID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	b, ok := r.get(taskID)
	if !ok {
		return nil
	}
	b.RequestFinish()
	if err := b.WaitForNoInflight(ctx); err != nil {
		return fmt.Errorf("%w: %v", budget.ErrFinishPending, err)
	}
	_, unknown, _, _, _ := b.lifecycleSnapshot()
	if unknown > 0 {
		return budget.ErrUnknownOutcomeUnreconciled
	}
	_, unknown, finished, _, reserved := b.lifecycleSnapshot()
	if unknown != 0 || reserved != 0 || !finished {
		return budget.ErrFinishPending
	}
	return nil
}
