package firecrawl

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
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
}

// Guard — разделяемое состояние аккаунта Firecrawl для всех воркеров процесса:
// raw-клиент, лимитер, кэш остатка и реестр бюджетов задач. Создаёт бюджетную
// задачу через Task.
type Guard struct {
	client     *Client
	limiter    *Limiter
	usage      *Usage
	registry   *Registry
	balanceTTL time.Duration
	maxRetries int
	retryBase  time.Duration
	clock      Clock
	sleep      func(ctx context.Context, d time.Duration) error
	jitter     func(max int64) int64
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
		registry:   NewRegistry(),
		balanceTTL: balanceTTL,
		maxRetries: maxRetries,
		retryBase:  retryBase,
		clock:      cfg.Clock,
		sleep:      sleep,
		jitter:     jitter,
	}
}

// Task создаёт (или возвращает существующий) бюджет задачи с заданным лимитом
// кредитов и возвращает бюджетную обёртку операций. maxCredits == 0 означает
// «бюджет не задан»: кредитный лимит не применяется, но фактический расход
// измеряется; перед каждой затратной операцией всё равно запрашивается свежий
// остаток аккаунта, и без него операция не запускается.
func (g *Guard) Task(id string, maxCredits int) (*Task, error) {
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
// search.Port и crawl.Port; каждый затратный вызов резервирует кредиты атомарно,
// выдерживает лимитер, при 429 повторяет с backoff, при 402 останавливает
// бюджет, и после вызова сверяет фактический расход.
type Task struct {
	id     string
	budget *TaskBudget
	guard  *Guard
}

// Проверка на этапе компиляции, что Task реализует оба порта и контракт бюджета.
var (
	_ search.Port          = (*Task)(nil)
	_ crawl.Port           = (*Task)(nil)
	_ budget.Task          = (*Task)(nil)
	_ budget.Factory       = (*Guard)(nil)
	_ budget.AccountReader = (*Guard)(nil)
)

// Snapshot возвращает снимок бюджета задачи по контракту budget.
func (t *Task) Snapshot() budget.Snapshot {
	s := t.budget.Snapshot()
	return budget.Snapshot{
		MaxCredits:            s.MaxCredits,
		ConfirmedSpentCredits: s.ConfirmedSpentCredits,
		ReservedCredits:       s.ReservedCredits,
		Available:             s.Available,
		Status:                string(s.Status),
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
	var contacts []crawl.ContactCandidate
	err := t.runFixed(ctx, OpScrape, 1, func(ctx context.Context) (int, error) {
		got, err := t.guard.client.Extract(ctx, r)
		contacts = got
		if err != nil {
			return 0, err
		}
		return 1, nil
	})
	return contacts, err
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

// runReserved — общий путь затратной операции: резервирование верхнего предела,
// лимитер, вызов с повтором 429 и сверка фактического расхода. При 402 бюджет
// задачи останавливается. Неиспользованный резерв освобождается всегда.
func (t *Task) runReserved(ctx context.Context, op Operation, cost int, crawlOp bool, call func(context.Context) (int, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.budget.Reserve(cost); err != nil {
		return err
	}
	actual := 0
	defer func() { t.budget.Settle(cost, actual) }()

	var release func()
	var err error
	if crawlOp {
		release, err = t.guard.limiter.AcquireCrawl(ctx)
	} else {
		release, err = t.guard.limiter.Acquire(ctx)
	}
	if err != nil {
		return err
	}
	defer release()

	actual, err = t.callWithRetry(ctx, call)
	if err != nil {
		if IsPaymentRequired(err) {
			t.budget.Stop()
		}
		return err
	}
	return nil
}

// callWithRetry повторяет вызов при HTTP 429 и при транзиентных сбоях транспорта
// (обрыв соединения, таймаут сети, 5xx). Задержка — заголовок Retry-After,
// если он есть; иначе экспоненциальный бэкофф; число повторов ограничено.
// Отмена и дедлайн контекста повторами не считаются: IsTransient их исключает.
func (t *Task) callWithRetry(ctx context.Context, call func(context.Context) (int, error)) (int, error) {
	for attempt := 0; ; attempt++ {
		actual, err := call(ctx)
		if err == nil || !retryable(err) || attempt >= t.guard.maxRetries {
			return actual, err
		}
		if err := t.guard.sleepRetry(ctx, attempt, RetryAfter(err)); err != nil {
			return 0, err
		}
	}
}

// retryable сообщает, повторяем ли вызов: превышение лимита (429) или
// транзиентный сбой транспорта/сервера.
func retryable(err error) bool {
	return IsRateLimited(err) || IsTransient(err)
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
