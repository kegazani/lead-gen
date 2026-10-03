// Package firecrawl — адаптер внешнего движка Firecrawl (v1 HTTP API).
//
// Адаптер реализует сразу два порта интеграционной платформы — search.Port
// (поиск по запросу) и crawl.Port (обход сайта и структурированная экстракция
// контактов). Источник истины для полей — DTO контрактов lead-gen/contracts,
// а не сырой JSON Firecrawl: здесь только прагматичное отображение ответов API
// на типы портов. Ошибки провайдера возвращаются обычными ошибками Go, без
// утечки типов HTTP/SDK наружу.
package firecrawl

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// defaultBaseURL — базовый адрес Firecrawl v1 API.
const defaultBaseURL = "https://api.firecrawl.dev"

// Значение по умолчанию для поиска. Порт объявляет «ноль означает значение по
// умолчанию адаптера»; здесь это несырьевой, но детерминированный лимит, чтобы
// случайно не выжечь кредит вызова движка без явного бюджета. Глубина обхода —
// исключение: ноль для неё осмыслен («только стартовая страница»), поэтому
// значение по умолчанию для глубины не задаётся, а передаётся как есть. Предел
// страниц обхода тоже не подставляется: при нулевом MaxPages поле limit не
// отправляется вовсе, а денежной границей служит обёртка бюджета (guard).
const defaultSearchLimit = 5

// pollInterval — пауза между опросами статуса асинхронного задания обхода.
const pollInterval = 500 * time.Millisecond

// finalSnapshotTimeout — дедлайн одной best-effort попытки снять снимок
// отменённого задания обхода.
const finalSnapshotTimeout = 5 * time.Second

// defaultHTTPTimeout — верхняя граница одного HTTP-запроса к Firecrawl, когда
// Config.HTTPClient не задан. Один вызов (search, scrape, шаг опроса crawl) —
// синхронная операция движка, где рендер страниц обычно укладывается в десятки
// секунд; минута даёт запас на медленный рендер и при этом не позволяет зависшему
// соединению держать activity до её серверного таймаута. Сам обход это значение не
// ограничивает: crawl асинхронен, а таймаут покрывает каждый запрос по отдельности.
const defaultHTTPTimeout = 60 * time.Second

// Config — настройки адаптера Firecrawl.
type Config struct {
	// APIKey — ключ доступа Firecrawl. Пустой ключ допустим для тестов с
	// локальным httptest-сервером, который не проверяет авторизацию.
	APIKey string
	// BaseURL — базовый адрес API. Пустое значение заменяется на defaultBaseURL.
	BaseURL string
	// HTTPClient — клиент для исходящих запросов. nil означает клиент адаптера с
	// конечным таймаутом (defaultHTTPTimeout), а не http.DefaultClient без таймаута.
	HTTPClient *http.Client
	// Logger — опциональный структурированный логгер безопасных параметров
	// запроса. nil означает «без логов».
	Logger observ.Logger
}

// Client — HTTP-клиент Firecrawl, реализующий search.Port и crawl.Port.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	logger     observ.Logger
}

// Проверка на этапе компиляции, что Client реализует оба порта.
var (
	_ search.Port = (*Client)(nil)
	_ crawl.Port  = (*Client)(nil)
)

// New собирает клиент, применяя значения по умолчанию. Конструктор не валидирует
// ключ: валидация конфигурации — забота корневого пакета, который решает,
// допустима ли сборка без ключа (например, в тестах).
func New(cfg Config) *Client {
	baseURL := strings.TrimRight(cfg.BaseURL, "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		apiKey:     cfg.APIKey,
		baseURL:    baseURL,
		httpClient: client,
		logger:     cfg.Logger,
	}
}

// Search выполняет поиск по запросу через /v1/search и отображает результаты на
// []search.Result. Контекст уважается: отмена/дедлайн прерывают запрос.
func (c *Client) Search(ctx context.Context, q search.Query) ([]search.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := q.Validate(); err != nil {
		return nil, err
	}

	limit := q.MaxResults
	if limit <= 0 {
		limit = defaultSearchLimit
	}

	var env envelope
	err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/search"), map[string]any{
		"query": q.Text,
		"limit": limit,
	}, &env)
	if err != nil {
		return nil, err
	}
	if err := env.check(); err != nil {
		return nil, err
	}

	var raw []searchResultJSON
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return nil, fmt.Errorf("firecrawl: поиск: декодирование результатов: %w", err)
	}

	results := make([]search.Result, 0, len(raw))
	for _, r := range raw {
		snippet := r.Description
		if snippet == "" {
			snippet = r.Markdown
		}
		if snippet == "" {
			snippet = r.Content
		}
		results = append(results, search.Result{
			URL:     r.URL,
			Title:   r.Title,
			Snippet: snippet,
		})
	}
	return results, nil
}

// Crawl обходит сайт через /v1/crawl. Если движок вернул асинхронное задание
// (id без данных), адаптер опрашивает статус задания до завершения, уважая
// отмену контекста. При отмене возвращаются уже собранные страницы вместе с
// crawl.ErrCancelled: данные, добытые до отмены, не теряются.
func (c *Client) Crawl(ctx context.Context, r crawl.Request) (crawl.CrawlOutcome, error) {
	if ctx.Err() != nil {
		return crawl.CrawlOutcome{}, crawl.ErrCancelled
	}
	if err := r.Validate(); err != nil {
		return crawl.CrawlOutcome{}, err
	}

	discoveryDepth := r.MaxDepth

	body := map[string]any{
		"url":               r.StartURL,
		"maxDiscoveryDepth": discoveryDepth,
		// ignoreSitemap заставляет движок считать глубину переходами именно от
		// стартовой страницы, а не от страниц из sitemap (для них глубина 0).
		"ignoreSitemap": true,
	}
	// При нулевом MaxPages поле limit не отправляется: движок обходит в своём
	// режиме, а границу задаёт вызывающий (денежный тормоз — обёртка бюджета).
	if r.MaxPages > 0 {
		body["limit"] = r.MaxPages
	}
	if len(r.IncludePaths) > 0 {
		body["includePaths"] = withStartPagePath(r.IncludePaths, r.StartURL)
	}
	if len(r.ExcludePaths) > 0 {
		body["excludePaths"] = r.ExcludePaths
	}

	// Логируем безопасные параметры запроса без ключа авторизации. maxDepth
	// (абсолютная глубина пути URL) намеренно не отправляется: для вложенных
	// стартовых URL Firecrawl v1 отвергает запрос с 400, если глубина пути
	// превышает maxDepth.
	if c.logger != nil {
		c.logger.Info("firecrawl: обход: параметры запроса", observ.Fields{},
			"url", r.StartURL,
			"limit", r.MaxPages,
			"maxDiscoveryDepth", discoveryDepth,
			"maxDepth", "",
		)
	}

	var env envelope
	err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/crawl"), body, &env)
	if err != nil {
		if ctx.Err() != nil {
			return crawl.CrawlOutcome{}, crawl.ErrCancelled
		}
		return crawl.CrawlOutcome{}, err
	}
	if err := env.check(); err != nil {
		return crawl.CrawlOutcome{}, err
	}

	// Асинхронное задание: движок вернул id, данные появятся после опроса статуса.
	if env.ID != "" {
		var cancelled bool
		env, cancelled, err = c.pollCrawl(ctx, env.ID)
		if err != nil {
			return crawl.CrawlOutcome{}, err
		}
		if cancelled {
			pages, perr := decodePages(env.Data)
			if perr != nil {
				return crawl.CrawlOutcome{}, perr
			}
			return crawl.CrawlOutcome{Pages: pages}, crawl.ErrCancelled
		}
	}

	pages, err := decodePages(env.Data)
	if err != nil {
		return crawl.CrawlOutcome{}, err
	}
	return crawl.CrawlOutcome{Pages: pages}, nil
}

// Extract выполняет структурированную экстракцию контактов через /v1/scrape в
// режиме JSON: движок обходит страницу LLM-моделью по заданной схеме
// name/email/phone/role, а адаптер отбирает из ответа объект contacts и отображает
// его на []crawl.ContactCandidate.
//
// Firecrawl v1 не знает формата «extract»: структурированный результат запрашивается
// форматом «json» с параметрами в jsonOptions, а сам извлечённый объект возвращается
// под data.json (в части сборок v1 — под устаревшим data.llm_extraction). Раньше
// адаптер слал formats:["extract"] и читал data как сам объект контактов, поэтому
// эскалация на LLM всегда отдавала пустой список — email, видимый только модели,
// не находился.
func (c *Client) Extract(ctx context.Context, r crawl.ExtractRequest) ([]crawl.ContactCandidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := r.Validate(); err != nil {
		return nil, err
	}

	var env envelope
	err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/scrape"), map[string]any{
		"url":     r.URL,
		"formats": []string{"json"},
		"jsonOptions": map[string]any{
			"schema":       contactSchema(),
			"systemPrompt": "Извлеки контакты (имя, email, телефон, роль) со страницы. Верни их массивом contacts.",
		},
	}, &env)
	if err != nil {
		return nil, err
	}
	if err := env.check(); err != nil {
		return nil, err
	}

	payload := extractionPayload(env.Data)
	if payload == nil {
		return nil, nil
	}

	var extracted struct {
		Contacts []contactJSON `json:"contacts"`
	}
	if err := json.Unmarshal(payload, &extracted); err != nil {
		return nil, fmt.Errorf("firecrawl: экстракция: декодирование контактов: %w", err)
	}

	contacts := make([]crawl.ContactCandidate, 0, len(extracted.Contacts))
	for _, ct := range extracted.Contacts {
		contacts = append(contacts, crawl.ContactCandidate{
			Name:  ct.Name,
			Email: ct.Email,
			Phone: ct.Phone,
			Role:  ct.Role,
		})
	}
	return contacts, nil
}

// extractionPayload выделяет из data ответа /v1/scrape объект структурированного
// извлечения. Firecrawl кладёт его в data.json (актуальная сборка v1) либо в
// устаревшее data.llm_extraction; если ни одного из полей нет, data и есть сам
// объект (например, ответ упрощённого мока). Возвращает nil, когда извлечения нет.
func extractionPayload(data json.RawMessage) json.RawMessage {
	var wrapper struct {
		JSON          json.RawMessage `json:"json"`
		LLMExtraction json.RawMessage `json:"llm_extraction"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if len(bytes.TrimSpace(wrapper.JSON)) > 0 && !isJSONNull(wrapper.JSON) {
			return wrapper.JSON
		}
		if len(bytes.TrimSpace(wrapper.LLMExtraction)) > 0 && !isJSONNull(wrapper.LLMExtraction) {
			return wrapper.LLMExtraction
		}
	}
	if isJSONNull(data) {
		return nil
	}
	return data
}

// isJSONNull сообщает, что сырое значение — JSON null или пусто.
func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// Scrape обходит одну страницу через /v1/scrape в режиме markdown и возвращает
// её содержимое. Используется как базовая операция скрейпа (1 кредит).
func (c *Client) Scrape(ctx context.Context, url string) (ScrapeResult, error) {
	if err := ctx.Err(); err != nil {
		return ScrapeResult{}, err
	}

	var env envelope
	err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/scrape"), map[string]any{
		"url":     url,
		"formats": []string{"markdown"},
	}, &env)
	if err != nil {
		return ScrapeResult{}, err
	}
	if err := env.check(); err != nil {
		return ScrapeResult{}, err
	}

	var raw scrapeJSON
	if err := json.Unmarshal(env.Data, &raw); err != nil {
		return ScrapeResult{}, fmt.Errorf("firecrawl: scrape: декодирование страницы: %w", err)
	}
	meta := stringifyMetadata(raw.Metadata)
	return ScrapeResult{
		URL:      pageURL(raw.URL, meta),
		Markdown: raw.Markdown,
		Metadata: meta,
	}, nil
}

// Map получает список URL сайта через /v1/map без загрузки контента (1 кредит
// за вызов). Возвращает адреса, которые затем можно скрейпить точечно.
func (c *Client) Map(ctx context.Context, url string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var env envelope
	err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/map"), map[string]any{
		"url": url,
	}, &env)
	if err != nil {
		return nil, err
	}
	if err := env.check(); err != nil {
		return nil, err
	}

	// Ответ /v1/map — список URL либо объект с полем links.
	var raw struct {
		Links []string `json:"links"`
	}
	if err := json.Unmarshal(env.Data, &raw); err == nil && raw.Links != nil {
		return raw.Links, nil
	}
	var list []string
	if err := json.Unmarshal(env.Data, &list); err != nil {
		return nil, fmt.Errorf("firecrawl: map: декодирование ссылок: %w", err)
	}
	return list, nil
}

// pollCrawl опрашивает статус асинхронного задания до завершения. Контекст
// уважается на каждой итерации: при отмене движку отправляется запрос остановки
// удалённого обхода, и возвращается признак cancelled вместе с best-effort снимком
// уже собранных страниц.
func (c *Client) pollCrawl(ctx context.Context, id string) (envelope, bool, error) {
	statusURL := c.endpoint("/v1/crawl/" + id)
	for {
		if ctx.Err() != nil {
			return c.stopCrawl(ctx, id, statusURL), true, nil
		}

		var env envelope
		if err := c.doJSON(ctx, http.MethodGet, statusURL, nil, &env); err != nil {
			if ctx.Err() != nil {
				return c.stopCrawl(ctx, id, statusURL), true, nil
			}
			return envelope{}, false, err
		}

		switch env.Status {
		case "completed":
			return env, false, nil
		case "failed", "cancelled":
			return envelope{}, false, fmt.Errorf("firecrawl: обход %s завершился статусом %q", id, env.Status)
		}

		if !wait(ctx, pollInterval) {
			return c.stopCrawl(ctx, id, statusURL), true, nil
		}
	}
}

// stopCrawl останавливает удалённое задание обхода и возвращает best-effort снимок
// уже собранных страниц. Порядок важен: сначала сигнал остановки движку (освободить
// ресурсы и прекратить списание кредитов), затем снимок добытого.
func (c *Client) stopCrawl(ctx context.Context, id, statusURL string) envelope {
	c.cancelCrawl(ctx, id)
	return c.finalSnapshot(ctx, statusURL)
}

// cancelCrawl отправляет движку запрос остановки удалённого обхода. Вызов —
// best-effort: ошибка не прерывает отмену задания (локальный исход уже cancelled).
// Отсоединённый контекст с коротким дедлайном не даёт зависнуть на отменённом
// контексте задания.
func (c *Client) cancelCrawl(ctx context.Context, id string) {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalSnapshotTimeout)
	defer cancel()
	_ = c.doJSON(cctx, http.MethodDelete, c.endpoint("/v1/crawl/"+id), nil, nil)
}

// finalSnapshot — одна best-effort попытка снять текущий снимок отменённого
// задания через отсоединённый от отмены контекст с коротким дедлайном. Отдаёт
// уже собранные движком страницы, если они есть; иначе — пустой конверт.
func (c *Client) finalSnapshot(ctx context.Context, statusURL string) envelope {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finalSnapshotTimeout)
	defer cancel()
	var env envelope
	if err := c.doJSON(fctx, http.MethodGet, statusURL, nil, &env); err != nil {
		return envelope{}
	}
	return env
}

// decodePages разбирает data ответа обхода в страницы порта.
func decodePages(raw json.RawMessage) ([]crawl.Page, error) {
	if len(raw) == 0 {
		return []crawl.Page{}, nil
	}
	var list []pageJSON
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("firecrawl: обход: декодирование страниц: %w", err)
	}
	pages := make([]crawl.Page, 0, len(list))
	for _, p := range list {
		meta := stringifyMetadata(p.Metadata)
		pages = append(pages, crawl.Page{
			URL:      pageURL(p.URL, meta),
			Markdown: p.Markdown,
			Metadata: meta,
		})
	}
	return pages, nil
}

// pageURL определяет адрес страницы обхода. Firecrawl в ответе обхода отдаёт URL
// не верхнеуровневым полем, а в metadata (sourceURL, реже url); пустой URL
// ломает дедупликацию компаний по домену, поэтому берём первое доступное значение.
func pageURL(fieldURL string, meta map[string]string) string {
	if strings.TrimSpace(fieldURL) != "" {
		return fieldURL
	}
	if u := meta["sourceURL"]; strings.TrimSpace(u) != "" {
		return u
	}
	return meta["url"]
}

// doJSON отправляет JSON-запрос и декодирует ответ. Неуспешный статус и ошибка
// транспорта возвращаются обычной ошибкой Go без утечки типов http.Response.
func (c *Client) doJSON(ctx context.Context, method, url string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("firecrawl: сериализация запроса: %w", err)
		}
		reader = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return fmt.Errorf("firecrawl: создание запроса: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("firecrawl: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("firecrawl: чтение ответа: %w", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &HTTPError{
			StatusCode: resp.StatusCode,
			Method:     method,
			URL:        url,
			Body:       truncate(string(data)),
			RetryAfter: parseRetryAfterHeader(resp.Header.Get("Retry-After")),
		}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("firecrawl: декодирование ответа: %w", err)
	}
	return nil
}

// endpoint склеивает базовый адрес и путь ресурса.
func (c *Client) endpoint(path string) string {
	return c.baseURL + path
}

// withStartPagePath дополняет include-пути якорным паттерном пути стартового URL.
// Firecrawl матчит includePaths по пути и отсекает всё, что не совпало, включая
// саму стартовую страницу: если её путь не подходит ни под один паттерн, движок
// возвращает ноль страниц и сайт не обходится вовсе. Паттерн стартовой страницы
// гарантирует начало обхода, после чего движок может найти перечисленные разделы.
func withStartPagePath(includePaths []string, startURL string) []string {
	path := ""
	if u, err := url.Parse(startURL); err == nil {
		path = strings.TrimRight(u.Path, "/")
	}
	pattern := "^" + regexp.QuoteMeta(path) + `/?$`
	out := make([]string, 0, len(includePaths)+1)
	out = append(out, includePaths...)
	return append(out, pattern)
}

// urlPathDepth возвращает абсолютную глубину пути URL в терминах Firecrawl v1
// maxDepth — число непустых сегментов пути. Например, путь "/a/b/c" имеет
// глубину 3, корень "/" — 0. Возвращает ошибку, если URL не является абсолютным.
func urlPathDepth(rawURL string) (int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("firecrawl: разбор URL %q: %w", rawURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return 0, fmt.Errorf("firecrawl: URL %q не является абсолютным", rawURL)
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return 0, nil
	}
	return len(strings.Split(path, "/")), nil
}

// validateMaxDepth проверяет до отправки, что стартовый URL укладывается в
// ограничение абсолютной глубины пути maxDepth (число сегментов пути не больше
// maxDepth). Firecrawl v1 возвращает 400, если глубина стартового пути превышает
// maxDepth. Текущий обход maxDepth не отправляет, поэтому функция не вызывается
// в горячем пути; она оставлена как локальная защита на случай, если ограничение
// абсолютной глубины URL понадобится.
func validateMaxDepth(rawURL string, maxDepth int) error {
	if maxDepth < 0 {
		return fmt.Errorf("firecrawl: maxDepth не может быть отрицательным (%d)", maxDepth)
	}
	depth, err := urlPathDepth(rawURL)
	if err != nil {
		return err
	}
	if depth > maxDepth {
		return fmt.Errorf("firecrawl: стартовый URL %q имеет глубину пути %d, что превышает maxDepth %d", rawURL, depth, maxDepth)
	}
	return nil
}

// wait ожидает duration либо отмену контекста; false означает отмену.
func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// stringifyMetadata превращает map[string]any в map[string]string, пропуская nil.
func stringifyMetadata(meta map[string]any) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	out := make(map[string]string, len(meta))
	for k, v := range meta {
		if v == nil {
			continue
		}
		out[k] = fmt.Sprint(v)
	}
	return out
}

// truncate ограничивает тело ответа для читаемой ошибки.
func truncate(s string) string {
	const max = 200
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// contactSchema — схема структурированной экстракции контактов. Служит
// источником истины для порта crawl.Extract: движок обязуется вернуть объект с
// массивом contacts, каждый элемент которого несёт name/email/phone/role.
func contactSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"contacts": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"name":  map[string]any{"type": "string"},
						"email": map[string]any{"type": "string"},
						"phone": map[string]any{"type": "string"},
						"role":  map[string]any{"type": "string"},
					},
					"required": []string{"name", "email", "phone", "role"},
				},
			},
		},
		"required": []string{"contacts"},
	}
}

// envelope — общая обёртка ответов Firecrawl. Data хранится сырым JSON, потому
// что его форма различается по операциям (массив результатов/страниц или объект).
type envelope struct {
	Success bool            `json:"success"`
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// check возвращает ошибку, если движок явно сообщил об отказе (success:false).
func (e envelope) check() error {
	if !e.Success {
		if e.Error != "" {
			return fmt.Errorf("firecrawl: движок вернул ошибку: %s", e.Error)
		}
		return fmt.Errorf("firecrawl: движок вернул безуспешный ответ")
	}
	return nil
}

// searchResultJSON — элемент результата /v1/search. Поля — подмножество ответа
// Firecrawl, достаточное для отображения на search.Result.
type searchResultJSON struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Markdown    string `json:"markdown"`
	Content     string `json:"content"`
}

// pageJSON — элемент результата /v1/crawl. Metadata приходит свободной картой,
// поэтому декодируется как map[string]any и сужается до map[string]string.
type pageJSON struct {
	URL      string         `json:"url"`
	Markdown string         `json:"markdown"`
	Metadata map[string]any `json:"metadata"`
}

// ScrapeResult — результат /v1/scrape в режиме markdown.
type ScrapeResult struct {
	URL      string
	Markdown string
	Metadata map[string]string
}

// scrapeJSON — форма ответа /v1/scrape. Metadata — свободная карта, сужается
// до map[string]string как в pageJSON.
type scrapeJSON struct {
	URL      string         `json:"url"`
	Markdown string         `json:"markdown"`
	Metadata map[string]any `json:"metadata"`
}

// contactJSON — элемент схемы экстракции контактов.
type contactJSON struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Phone string `json:"phone"`
	Role  string `json:"role"`
}
