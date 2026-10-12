// Package crawl — порт обхода: превращение сайта в набор страниц (markdown) и
// структурированная экстракция контактов.
//
// DTO порта фиксируют понятия бюджета, глубины и фильтров путей, а не
// конфигурацию конкретного движка: замена движка — правка адаптера, не ядра.
// Реализация обязана уважать context.Context, допускать повторные вызовы и не
// протекать ошибками SDK наружу.
package crawl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// ErrCancelled — обход прерван отменой контекста. Возвращается вместе с уже
// собранными страницами: вызывающий обязан обработать Outcome.Pages (данные,
// добытые до отмены) и лишь затем завершиться как отменённый.
var ErrCancelled = errors.New("crawl: обход отменён")

// ErrTimeout — обход не завершился за отведённое время: истёк
// Request.MaxTimeSeconds либо дедлайн контекста вызова. Возвращается вместе с уже
// собранными страницами (данные, добытые до истечения времени, не теряются).
// Отличается от ErrCancelled: таймаут — транзиентный исход, который вызывающий
// вправе повторить, тогда как отмена повтором не воскрешается.
var ErrTimeout = errors.New("crawl: обход не завершился за отведённое время")

// Port — порт обхода. Реализуется адаптером движка краулинга
// (в срезе — Firecrawl /crawl, /map, /scrape, extract).
type Port interface {
	// Crawl обходит сайт от стартового URL и возвращает обойдённые страницы.
	Crawl(ctx context.Context, request Request) (CrawlOutcome, error)
	// Extract выполняет структурированную экстракцию контактов по схеме
	// name/email/phone/role.
	Extract(ctx context.Context, request ExtractRequest) ([]ContactCandidate, error)
}

// Request — параметры обхода сайта.
type Request struct {
	// StartURL — стартовый адрес. Обязателен.
	StartURL string
	// MaxPages — бюджет страниц. Ноль означает «явный лимит не задан»: адаптер не
	// отправляет предел движку, а границу обхода задаёт бюджетная обёртка (доступный
	// бюджет задачи либо подтверждённый остаток аккаунта).
	MaxPages int
	// MaxDepth — максимальная глубина обхода: число переходов по ссылкам от
	// стартовой страницы (в Firecrawl v1 — maxDiscoveryDepth). Ноль означает
	// только стартовую страницу. Не путать с абсолютной глубиной пути URL
	// (maxDepth Firecrawl), которую адаптер не использует.
	MaxDepth int
	// MaxTimeSeconds — предельное время обхода в секундах. Ноль означает «лимит
	// времени не задан» (поведение определяет вызывающий); отрицательное недопустимо.
	// Адаптер SHALL применять это значение как жёсткий предел всего обхода (не
	// одного HTTP-запроса); по истечении обход завершается ErrTimeout с уже
	// собранными страницами.
	MaxTimeSeconds int
	// IncludePaths — regex-паттерны путей (RE2-синтаксис), которые обходятся
	// (релевантные разделы, например contact/team/about/staff/impressum).
	IncludePaths []string
	// ExcludePaths — regex-паттерны путей (RE2-синтаксис), которые отсекаются
	// до обхода (блог, новости, каталог).
	ExcludePaths []string
}

// Page — одна обойдённая страница.
type Page struct {
	// URL — адрес страницы. Обязателен.
	URL string
	// Markdown — чистое содержимое страницы.
	Markdown string
	// Metadata — метаданные страницы (заголовок, описание, разметка).
	Metadata map[string]string
}

// CrawlOutcome — исход обхода: набор страниц.
type CrawlOutcome struct {
	Pages []Page
}

// ExtractRequest — запрос структурированной экстракции контактов.
type ExtractRequest struct {
	// URL — адрес страницы, из которой извлекаются контакты. Обязателен.
	URL string
}

// ContactCandidate — извлечённый контакт по схеме name/email/phone/role.
type ContactCandidate struct {
	Name  string
	Email string
	Phone string
	Role  string
}

// Validate проверяет обязательные поля запроса и границы бюджета.
func (r Request) Validate() error {
	var invalid []string
	if strings.TrimSpace(r.StartURL) == "" {
		invalid = append(invalid, "start_url")
	}
	if r.MaxPages < 0 {
		invalid = append(invalid, "max_pages")
	}
	if r.MaxDepth < 0 {
		invalid = append(invalid, "max_depth")
	}
	if r.MaxTimeSeconds < 0 {
		invalid = append(invalid, "max_time_seconds")
	}
	for _, p := range r.IncludePaths {
		if strings.TrimSpace(p) == "" {
			invalid = append(invalid, "include_paths")
			break
		}
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("crawl: request: include_paths %q не является корректным regex: %w", p, err)
		}
	}
	for _, p := range r.ExcludePaths {
		if strings.TrimSpace(p) == "" {
			invalid = append(invalid, "exclude_paths")
			break
		}
		if _, err := regexp.Compile(p); err != nil {
			return fmt.Errorf("crawl: request: exclude_paths %q не является корректным regex: %w", p, err)
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("crawl: request: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// Validate проверяет обязательные поля страницы.
func (p Page) Validate() error {
	if strings.TrimSpace(p.URL) == "" {
		return fmt.Errorf("crawl: page: URL обязателен")
	}
	return nil
}

// Validate проверяет обязательные поля запроса экстракции.
func (e ExtractRequest) Validate() error {
	if strings.TrimSpace(e.URL) == "" {
		return fmt.Errorf("crawl: extract request: URL обязателен")
	}
	return nil
}
