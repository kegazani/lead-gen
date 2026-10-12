package firecrawl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"lead-gen/contracts/integration-platform/documentparse"
)

// Проверка на этапе компиляции, что клиент реализует порт разбора документов.
var _ documentparse.Port = (*Client)(nil)

// ParseDocument реализует documentparse.Port: разбирает предоставленный документ
// (например, коммерческое предложение в PDF) через /v1/scrape в режиме markdown и
// возвращает извлечённый текст. Порт ходит в реальный HTTP-эндпоинт движка, а не
// подменяет результат: недоступность движка или неподдерживаемый документ —
// честная ошибка вызывающему.
//
// Подсказка формата передаётся движку полем parsers (например, "pdf"): Firecrawl
// включает соответствующий разборщик документа. Без подсказки формат определяет
// движок.
func (c *Client) ParseDocument(ctx context.Context, r documentparse.Request) (documentparse.Document, error) {
	if err := ctx.Err(); err != nil {
		return documentparse.Document{}, err
	}
	if err := r.Validate(); err != nil {
		return documentparse.Document{}, err
	}

	var parsers []string
	if format := strings.TrimSpace(r.Format); format != "" {
		parsers = []string{format}
	}
	scraped, err := c.scrape(ctx, r.Source, parsers)
	if err != nil {
		return documentparse.Document{}, err
	}
	return documentparse.Document{
		Source:   scraped.URL,
		Text:     scraped.Markdown,
		Format:   strings.TrimSpace(r.Format),
		Metadata: scraped.Metadata,
	}, nil
}

// scrape — общий путь операции /v1/scrape в режиме markdown: используется базовым
// скрейпом одной страницы и разбором документа. parsers необязателен и задаёт
// разборщики формата (например, pdf). Возвращает извлечённые текст и метаданные.
func (c *Client) scrape(ctx context.Context, url string, parsers []string) (ScrapeResult, error) {
	if err := ctx.Err(); err != nil {
		return ScrapeResult{}, err
	}

	body := map[string]any{
		"url":     url,
		"formats": []string{"markdown"},
	}
	if len(parsers) > 0 {
		body["parsers"] = parsers
	}

	var env envelope
	if err := c.doJSON(ctx, http.MethodPost, c.endpoint("/v1/scrape"), body, &env); err != nil {
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
