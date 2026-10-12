package firecrawl

import (
	"context"
	"sync"
	"time"
)

// Значения по умолчанию предохранителей частоты. Начальная внутренняя настройка
// строже документированных лимитов Free-плана (10 запросов/мин на обычные и
// 2 запроса/мин на crawl): не более одного затратного запроса одновременно и
// пауза не менее 10/40 секунд. Значения выносятся в конфигурацию.
const (
	defaultMaxConcurrent = 1
	defaultMinInterval   = 10 * time.Second
	defaultCrawlInterval = 40 * time.Second
)

// LimiterConfig — настройки лимитера частоты и параллелизма.
type LimiterConfig struct {
	// MaxConcurrent — максимум одновременных затратных запросов. Ноль — defaultMaxConcurrent.
	MaxConcurrent int
	// MinInterval — минимальная пауза между обычными запросами (scrape/map/search).
	// Ноль — defaultMinInterval.
	MinInterval time.Duration
	// CrawlInterval — минимальная пауза между запусками crawl. Ноль — defaultCrawlInterval.
	CrawlInterval time.Duration
	// Clock — часы; nil — системное время.
	Clock Clock
	// Sleep — впрыскиваемая задержка (для тестов); nil — реальный таймер.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Limiter — общий лимитер аккаунта Firecrawl: сериализует затратные запросы и
// выдерживает минимальные паузы между запросами одного класса. Разделяется
// всеми воркерами процесса.
type Limiter struct {
	sem           chan struct{}
	minInterval   time.Duration
	crawlInterval time.Duration
	clock         Clock
	sleep         func(ctx context.Context, d time.Duration) error

	mu         sync.Mutex
	lastNormal time.Time
	lastCrawl  time.Time
}

// NewLimiter собирает лимитер, применяя значения по умолчанию.
func NewLimiter(cfg LimiterConfig) *Limiter {
	maxConcurrent := cfg.MaxConcurrent
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	minInterval := cfg.MinInterval
	if minInterval <= 0 {
		minInterval = defaultMinInterval
	}
	crawlInterval := cfg.CrawlInterval
	if crawlInterval <= 0 {
		crawlInterval = defaultCrawlInterval
	}
	sleep := cfg.Sleep
	if sleep == nil {
		sleep = realSleep
	}
	return &Limiter{
		sem:           make(chan struct{}, maxConcurrent),
		minInterval:   minInterval,
		crawlInterval: crawlInterval,
		clock:         cfg.Clock,
		sleep:         sleep,
	}
}

// Acquire занимает слот лимитера для обычного затратного запроса, выдерживая
// минимальную паузу с предыдущего обычного запроса. Возвращённая функция
// освобождает слот.
func (l *Limiter) Acquire(ctx context.Context) (func(), error) {
	return l.acquire(ctx, false)
}

// AcquireCrawl занимает слот для запуска crawl, выдерживая минимальную паузу
// между запусками обхода.
func (l *Limiter) AcquireCrawl(ctx context.Context) (func(), error) {
	return l.acquire(ctx, true)
}

func (l *Limiter) acquire(ctx context.Context, crawl bool) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Сериализация: не более maxConcurrent затратных запросов одновременно.
	select {
	case l.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		<-l.sem
	}
	completed := false
	defer func() {
		if !completed {
			release()
		}
	}()

	// Выдержка минимальной паузы с предыдущего запроса этого класса.
	minInterval := l.minInterval
	last := l.lastNormal
	if crawl {
		minInterval = l.crawlInterval
		last = l.lastCrawl
	}
	if waitFor := last.Add(minInterval).Sub(now(l.clock)); waitFor > 0 {
		if err := l.sleep(ctx, waitFor); err != nil {
			release()
			return nil, err
		}
	}

	l.mu.Lock()
	if crawl {
		l.lastCrawl = now(l.clock)
	} else {
		l.lastNormal = now(l.clock)
	}
	l.mu.Unlock()
	completed = true
	return release, nil
}

// realSleep — задержка на реальном таймере с уважением отмены контекста.
func realSleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
