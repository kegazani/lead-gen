package firecrawl

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"

	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/documentparse"
	"lead-gen/contracts/integration-platform/search"
)

// Значения по умолчанию обёртки бюджета. Бэкофф и число повторов подобраны
// так, чтобы покрыть окно лимита Firecrawl: при 429 провайдер просит повторить
// через 20–45 секунд, поэтому короткие задержки (1/2/4 с) не дожидались снятия
// лимита и задания падали. Если пришёл заголовок Retry-After, он перекрывает
// бэкофф (см. Task.callWithRetry).
const (
	defaultBalanceTTL = time.Minute
	defaultMaxRetries = 4
	defaultRetryBase  = 5 * time.Second
)

// ErrExtractCostUnknown возвращается, пока для используемой формы Firecrawl
// Extract нет подтверждённой верхней оценки стоимости.
var ErrExtractCostUnknown = errors.New("firecrawl: стоимость Extract не подтверждена")

// GuardConfig — настройки бюджетной обёртки Firecrawl.
type GuardConfig struct {
	// Client — raw-клиент Firecrawl. Обязателен.
	Client *Client
	// Limiter — общий лимитер. nil означает лимитер по умолчанию.
	Limiter *Limiter
	// Usage — кэш остатка аккаунта. nil означает пустой кэш поверх Client.
	Usage *Usage
	// BalanceTTL — срок свежести остатка аккаунта. Ноль — defaultBalanceTTL.
	BalanceTTL time.Duration
	// MaxRetries — число повторов при 429. Ноль — defaultMaxRetries.
	MaxRetries int
	// RetryBase — базовая задержка повтора. Ноль — defaultRetryBase.
	RetryBase time.Duration
	// Clock — часы; nil — системное время.
	Clock Clock
	// Sleep — впрыскиваемая задержка (для тестов); nil — реальный таймер.
	Sleep func(ctx context.Context, d time.Duration) error
	// Jitter — источник случайного разброса задержки повтора; nil — math/rand.
	Jitter func(max int64) int64
	// Registry ограничивает process-local task scopes. Нулевые поля используют
	// конечные default capacity/TTL; эти значения не являются monetary cap.
	Registry RegistryConfig
}

// Guard — разделяемое состояние аккаунта Firecrawl для всех воркеров процесса:
// raw-клиент, лимитер, кэш остатка и реестр бюджетов задач. Создаёт бюджетную
// задачу через Task и останавливает новые операции аккаунта после HTTP 402.
type Guard struct {
	client         *Client
	limiter        *Limiter
	usage          *Usage
	registry       *Registry
	balanceTTL     time.Duration
	maxRetries     int
	retryBase      time.Duration
	clock          Clock
	sleep          func(ctx context.Context, d time.Duration) error
	jitter         func(max int64) int64
	accountStopped atomic.Bool
}

// NewGuard собирает обёртку, применяя значения по умолчанию.
func NewGuard(cfg GuardConfig) *Guard {
	if cfg.Client == nil {
		panic("firecrawl: GuardConfig.Client обязателен")
	}
	limiter := cfg.Limiter
	if limiter == nil {
		limiter = NewLimiter(LimiterConfig{Clock: cfg.Clock, Sleep: cfg.Sleep})
	}
	usage := cfg.Usage
	if usage == nil {
		usage = NewUsage(cfg.Client, cfg.Clock)
	}
	balanceTTL := cfg.BalanceTTL
	if balanceTTL <= 0 {
		balanceTTL = defaultBalanceTTL
	}
	maxRetries := cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}
	retryBase := cfg.RetryBase
	if retryBase <= 0 {
		retryBase = defaultRetryBase
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = realSleep
	}
	jitter := cfg.Jitter
	if jitter == nil {
		jitter = rand.Int63n
	}
	return &Guard{
		client:     cfg.Client,
		limiter:    limiter,
		usage:      usage,
		registry:   mustNewRegistry(cfg.Registry),
		balanceTTL: balanceTTL,
		maxRetries: maxRetries,
		retryBase:  retryBase,
		clock:      cfg.Clock,
		sleep:      sleep,
		jitter:     jitter,
	}
}

func mustNewRegistry(cfg RegistryConfig) *Registry {
	r, err := NewRegistry(cfg)
	if err != nil {
		panic(err)
	}
	return r
}

// Task создаёт (или возвращает существующий) бюджет задачи с заданным лимитом
// кредитов и возвращает бюджетную обёртку операций. maxCredits == 0 означает
// «бюджет не задан»: кредитный лимит не применяется, но фактический расход
// измеряется; перед каждой затратной операцией всё равно запрашивается свежий
// остаток аккаунта, и без него операция не запускается.
func (g *Guard) Task(id string, maxCredits int) (*Task, error) {
	if g.client.legacyDisabled {
		return nil, legacyDisabledError()
	}
	b, err := g.registry.GetOrCreate(id, maxCredits)
	if err != nil {
		return nil, err
	}
	return &Task{id: id, budget: b, guard: g}, nil
}

// Begin реализует budget.Factory: создаёт бюджетную обёртку задачи. maxCredits
// == 0 задаёт безлимитную, но измеряющую фактический расход задачу.
func (g *Guard) Begin(taskID string, maxCredits int) (budget.Task, error) {
	return g.Task(taskID, maxCredits)
}

// Finish реализует budget.Factory: блокирует новые резервы scope и ждёт
// завершения in-flight попыток. Без exact provider operation evidence unknown
// outcome остаётся pinned; агрегированный account balance его не разрешает.
func (g *Guard) Finish(ctx context.Context, taskID string) error {
	return g.registry.Finish(ctx, taskID)
}

// RegistryStatus возвращает ограниченный status snapshot Guard для наблюдения
// capacity pressure и pinned unknown/outstanding scope.
func (g *Guard) RegistryStatus() budget.RegistryStatus { return g.registry.Status() }

// CreditBalance реализует budget.AccountReader: возвращает остаток кредитов.
func (g *Guard) CreditBalance() budget.Balance {
	b := g.usage.CreditBalance()
	return budget.Balance{Available: b.Available, Value: b.Value, UpdatedAt: b.UpdatedAt}
}

// TokenBalance реализует budget.AccountReader: возвращает остаток токенов.
func (g *Guard) TokenBalance() budget.Balance {
	b := g.usage.TokenBalance()
	return budget.Balance{Available: b.Available, Value: b.Value, UpdatedAt: b.UpdatedAt}
}

// RefreshCredit запрашивает credit-usage (например, перед задачей).
func (g *Guard) RefreshCredit(ctx context.Context) error { return g.usage.RefreshCredit(ctx) }

// RefreshToken запрашивает token-usage (информационное поле).
func (g *Guard) RefreshToken(ctx context.Context) error { return g.usage.RefreshToken(ctx) }

// freshBalance возвращает свежий остаток аккаунта, обновляя его при истечении
// TTL. Если получить остаток нельзя, возвращает ошибку.
func (g *Guard) freshBalance(ctx context.Context) (usageBalance, error) {
	bal := g.usage.CreditBalance()
	if bal.Available && g.isFresh(bal.UpdatedAt) {
		return bal, nil
	}
	if err := g.usage.RefreshCredit(ctx); err != nil {
		return usageBalance{}, fmt.Errorf("firecrawl: остаток аккаунта недоступен: %w", err)
	}
	return g.usage.CreditBalance(), nil
}

// isFresh сообщает, не истёк ли срок свежести остатка.
func (g *Guard) isFresh(updatedAt time.Time) bool {
	return now(g.clock).Sub(updatedAt) <= g.balanceTTL
}

// Task — бюджетная обёртка операций Firecrawl одной задачи. Реализует
// search.Port и crawl.Port; каждая попытка атомарно резервируется до сети,
// выдерживает лимитер, при явном 429 может повториться с новым резервом, а
// неизвестный исход удерживает полный резерв без автоматического повтора.
type Task struct {
	id     string
	budget *TaskBudget
	guard  *Guard
}

// Проверка на этапе компиляции, что Task реализует оба порта и контракт бюджета.
var (
	_ search.Port                 = (*Task)(nil)
	_ crawl.Port                  = (*Task)(nil)
	_ documentparse.Port          = (*Task)(nil)
	_ budget.Task                 = (*Task)(nil)
	_ budget.Factory              = (*Guard)(nil)
	_ budget.RegistryStatusReader = (*Guard)(nil)
	_ budget.AccountReader        = (*Guard)(nil)
)

// Snapshot возвращает снимок бюджета задачи по контракту budget.
func (t *Task) Snapshot() budget.Snapshot {
	s := t.budget.Snapshot()
	return budget.Snapshot{
		MaxCredits:              s.MaxCredits,
		ConfirmedSpentCredits:   s.ConfirmedSpentCredits,
		ReservedCredits:         s.ReservedCredits,
		UnknownCredits:          s.UnknownCredits,
		OutstandingReservations: s.OutstandingReservations,
		Available:               s.Available,
		Status:                  string(s.Status),
	}
}

// Search выполняет поиск с учётом бюджета и остатка аккаунта.
func (t *Task) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	limit := q.MaxResults
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	cost := CostSearch(limit)
	bal, err := t.guard.freshBalance(ctx)
	if err != nil {
		return nil, err
	}
	if bal.Value < cost {
		return nil, fmt.Errorf("firecrawl: остаток аккаунта (%d) меньше стоимости поиска (%d)", bal.Value, cost)
	}

	var results []search.Result
	err = t.runReserved(ctx, OpSearch, cost, false, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.Search(ctx, q)
		results = got
		return CostSearch(len(got)), err
	})
	return results, err
}

// Crawl обходит сайт. Верхняя граница страниц определяется денежным тормозом:
// при заданном бюджете задачи — доступным бюджетом, при незаданном —
// подтверждённым остатком аккаунта (freshBalance). Явный max_pages лишь сужает
// эту границу: без него обход идёт в режиме движка, но не дороже доступных денег.
// Резерв равен итоговой границе, фактический расход сверяется по завершении.
func (t *Task) Crawl(ctx context.Context, r crawl.Request) (crawl.CrawlOutcome, error) {
	if err := r.Validate(); err != nil {
		return crawl.CrawlOutcome{}, err
	}

	// Свежий остаток аккаунта запрашивается перед каждой затратной операцией и
	// служит денежной границей наравне с бюджетом задачи.
	bal, err := t.guard.freshBalance(ctx)
	if err != nil {
		return crawl.CrawlOutcome{}, err
	}
	limit := bal.Value
	// Доступный бюджет задачи сужает границу, но не заменяет её: бюджет — это
	// про деньги, а не про число страниц. Отправлять движку весь остаток
	// бюджета как лимит страниц нельзя — движок попытается выкачать пол-сайта.
	if snap := t.budget.Snapshot(); snap.MaxCredits > 0 && snap.Available < limit {
		limit = snap.Available
	}
	// Явный предел страниц (внутренний предохранитель на сайт) сужает границу
	// сильнее всего — именно он задаёт реальный объём обхода одного сайта.
	if r.MaxPages > 0 && r.MaxPages < limit {
		limit = r.MaxPages
	}
	if limit <= 0 {
		return crawl.CrawlOutcome{}, fmt.Errorf("firecrawl: недостаточно кредитов для обхода")
	}

	req := r
	req.MaxPages = limit

	var outcome crawl.CrawlOutcome
	err = t.runReserved(ctx, OpCrawl, limit, true, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.Crawl(ctx, req)
		outcome = got
		return len(got.Pages), err
	})
	return outcome, err
}

// Scrape обходит одну страницу в режиме markdown (1 кредит).
func (t *Task) Scrape(ctx context.Context, url string) (ScrapeResult, error) {
	var result ScrapeResult
	err := t.runFixed(ctx, OpScrape, 1, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.Scrape(ctx, url)
		result = got
		if err != nil {
			return 0, err
		}
		return 1, nil
	})
	return result, err
}

// Map получает список URL сайта (1 кредит за вызов).
func (t *Task) Map(ctx context.Context, url string) ([]string, error) {
	var links []string
	err := t.runFixed(ctx, OpMap, 1, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.Map(ctx, url)
		links = got
		if err != nil {
			return 0, err
		}
		return 1, nil
	})
	return links, err
}

// Extract выполняет структурированную экстракцию контактов. Учитывается по
// базовой стоимости scrape (1 кредит); переменная стоимость extract — открытый
// вопрос (см. design.md).
func (t *Task) Extract(ctx context.Context, r crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	if err := r.Validate(); err != nil {
		return nil, err
	}
	return nil, ErrExtractCostUnknown
}

// ParseDocument разбирает предоставленный документ через движок. Учитывается по
// базовой стоимости scrape (1 кредит): разбор документа — одна операция движка.
func (t *Task) ParseDocument(ctx context.Context, r documentparse.Request) (documentparse.Document, error) {
	if err := r.Validate(); err != nil {
		return documentparse.Document{}, err
	}
	var document documentparse.Document
	err := t.runFixed(ctx, OpScrape, 1, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.ParseDocument(ctx, r)
		document = got
		if err != nil {
			return 0, err
		}
		return 1, nil
	})
	return document, err
}

// runFixed — обёртка операций с фиксированной стоимостью 1 кредит.
func (t *Task) runFixed(ctx context.Context, op Operation, cost int, call func(context.Context) (int, error)) error {
	bal, err := t.guard.freshBalance(ctx)
	if err != nil {
		return err
	}
	if bal.Value < cost {
		return fmt.Errorf("firecrawl: остаток аккаунта (%d) меньше стоимости операции (%d)", bal.Value, cost)
	}
	return t.runReserved(ctx, op, cost, false, call)
}

// runReserved — общий путь затратной операции. Каждая явно безопасная повторная
// попытка резервируется отдельно. Любой неизвестный исход сохраняет полный резерв
// и не повторяется; crawl create POST поэтому никогда не дублируется при сетевом
// сбое. Только явный 429, подтверждающий отказ до обработки, допускает новый
// зарезервированный повтор. HTTP 402 останавливает весь Guard.
func (t *Task) runReserved(ctx context.Context, op Operation, cost int, crawlOp bool, call func(context.Context) (int, error)) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.guard.accountStopped.Load() {
			return fmt.Errorf("firecrawl: затратные операции аккаунта остановлены после HTTP 402")
		}
		// Повторная платная попытка должна пройти свежую account admission
		// отдельно: успешный 429 не превращает следующий запрос в бесплатный.
		bal, err := t.guard.freshBalance(ctx)
		if err != nil {
			return err
		}
		if bal.Value < cost {
			return fmt.Errorf("firecrawl: остаток аккаунта (%d) меньше стоимости операции (%d)", bal.Value, cost)
		}
		retry, callErr := t.runReservedAttempt(ctx, cost, crawlOp, bal, call)
		if !retry || attempt >= t.guard.maxRetries {
			return callErr
		}
		if err := t.guard.sleepRetry(ctx, attempt, RetryAfter(callErr)); err != nil {
			return err
		}
		_ = op // операция остаётся в одном и том же retry policy path.
	}
}

// runReservedAttempt закрывает reservation для всех не-сетевых исходов и
// переводит panic/неизвестную сеть в pinned unknown. Он возвращает retry=true
// только для явного 429, доказывающего отсутствие списания.
func (t *Task) runReservedAttempt(ctx context.Context, cost int, crawlOp bool, balance usageBalance, call func(context.Context) (int, error)) (retry bool, err error) {
	if err := t.budget.Reserve(cost); err != nil {
		return false, err
	}
	resolved := false
	networkStarted := false
	var release func()
	defer func() {
		if recovered := recover(); recovered != nil {
			if release != nil {
				release()
			}
			if !resolved {
				if networkStarted {
					_ = t.budget.MarkUnknown(cost)
				} else {
					_ = t.budget.Settle(cost, 0)
				}
			}
			panic(recovered)
		}
	}()

	if crawlOp {
		release, err = t.guard.limiter.AcquireCrawl(ctx)
	} else {
		release, err = t.guard.limiter.Acquire(ctx)
	}
	if err != nil {
		_ = t.budget.Settle(cost, 0) // сеть не вызывалась
		resolved = true
		return false, err
	}
	if t.guard.accountStopped.Load() {
		release()
		_ = t.budget.Settle(cost, 0) // аккаунт остановлен до отправки
		resolved = true
		return false, fmt.Errorf("firecrawl: затратные операции аккаунта остановлены после HTTP 402")
	}
	if !t.budget.CanStart() {
		release()
		_ = t.budget.Settle(cost, 0) // task был остановлен/закрыт до сети
		resolved = true
		return false, budget.ErrTaskClosed
	}

	networkStarted = true
	actual, callErr := call(ctx)
	release()
	if callErr == nil {
		err = t.budget.Settle(cost, actual)
		resolved = true
		return false, err
	}
	if IsPaymentRequired(callErr) {
		t.guard.accountStopped.Store(true)
		t.budget.Stop()
		err = t.budget.Settle(cost, 0) // явный 402 — отказ без подтверждённого расхода
		resolved = true
		if err != nil {
			return false, err
		}
		return false, callErr
	}
	if IsRateLimited(callErr) {
		err = t.budget.Settle(cost, 0)
		resolved = true
		if err != nil {
			return false, err
		}
		return true, callErr
	}
	// Транспортная ошибка, 5xx, неизвестный статус/ответ: запрос мог быть
	// обработан провайдером. Резерв остаётся pinned, а новые попытки блокируются.
	err = t.budget.MarkUnknown(cost)
	resolved = true
	if err != nil {
		return false, err
	}
	return false, callErr
}

// sleepRetry — задержка перед повтором: серверное значение Retry-After, если оно
// задано, иначе retryBase * 2^attempt; в обоих случаях плюс случайный разброс в
// пределах [0, задержка).
func (g *Guard) sleepRetry(ctx context.Context, attempt int, serverDelay time.Duration) error {
	delay := serverDelay
	if delay <= 0 {
		delay = g.retryBase << attempt
	}
	jitter := time.Duration(g.jitter(int64(delay)))
	return g.sleep(ctx, delay+jitter)
}
