// Package forum — адаптер форумного источника: поиск обезличенных сигналов
// (тем, болей, обсуждений) по запросу оператора.
//
// Адаптер не тянет собственный SDK форумного API: по умолчанию он оборачивает
// поиск (search.Port) и ограничивает его доменами форумов, отображая результаты
// на []forum.Signal. Источник истины для полей — DTO контракта
// lead-gen/contracts, а не сырой ответ поисковика. Ошибки провайдера
// возвращаются обычными ошибками Go, без утечки типов SDK наружу.
package forum

import (
	"context"
	"strings"
	"time"

	"lead-gen/contracts/integration-platform/forum"
	"lead-gen/contracts/integration-platform/search"
)

// defaultDomains — домены форумов, по которым ограничивается поиск, если явный
// список не задан. Список заведомо мал и публичен; расширение — конфигурация,
// а не правка кода.
var defaultDomains = []string{"reddit.com", "news.ycombinator.com"}

// Config — настройки адаптера форумного источника.
type Config struct {
	// Search — поисковый движок, которым ищутся темы и обсуждения. Обязателен.
	Search search.Port
	// Domains — домены форумов, по которым ограничивается поиск. Пустой список
	// заменяется на defaultDomains.
	Domains []string
}

// Client — адаптер форумного источника, реализующий forum.Port.
type Client struct {
	search  search.Port
	domains []string
}

// Проверка на этапе компиляции, что Client реализует forum.Port.
var _ forum.Port = (*Client)(nil)

// New собирает адаптер, применяя список доменов по умолчанию. Конструктор не
// валидирует поисковый движок: его наличие проверяет composition root.
func New(cfg Config) *Client {
	domains := cfg.Domains
	if len(domains) == 0 {
		domains = append([]string(nil), defaultDomains...)
	}
	return &Client{
		search:  cfg.Search,
		domains: domains,
	}
}

// SearchSignals ищет сигналы по запросу, ограничивая поиск доменами форумов, и
// отображает результаты поиска на []forum.Signal. Контекст уважается: отмена и
// дедлайн прерывают вызов. Дата сигнала — момент сбора, так как поисковый
// движок не возвращает дату публикации.
func (c *Client) SearchSignals(ctx context.Context, q forum.Query) ([]forum.Signal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}

	results, err := c.search.Search(ctx, search.Query{Text: c.restrict(q.Text)})
	if err != nil {
		return nil, err
	}

	at := time.Now().UTC()
	signals := make([]forum.Signal, 0, len(results))
	for _, r := range results {
		signals = append(signals, forum.Signal{
			Topic:     r.Title,
			Summary:   r.Snippet,
			SourceURL: r.URL,
			Date:      at,
		})
	}
	return signals, nil
}

// restrict добавляет к тексту запроса ограничение по доменам форумов через
// операторы site:. Пустой список доменов оставляет запрос без ограничения.
func (c *Client) restrict(text string) string {
	sites := make([]string, 0, len(c.domains))
	for _, domain := range c.domains {
		if domain = strings.TrimSpace(domain); domain != "" {
			sites = append(sites, "site:"+domain)
		}
	}
	if len(sites) == 0 {
		return text
	}
	return text + " " + strings.Join(sites, " OR ")
}
