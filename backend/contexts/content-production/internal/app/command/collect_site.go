package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
)

// CollectSite — команда сбора сведений с сайта клиента средствами движка сбора
// данных (capability landing-page-generator, требование «Извлечение содержания с
// сайта клиента»).
//
// Движок сбора (Firecrawl) живёт вне этого контекста за портом crawl.Port и
// впрыскивается точкой сборки. Без порта сбор честно недоступен: генерация
// продолжается на предоставленных вручную материалах, а недоступность сообщается.
// Собранный текст очищается от сведений о физических лицах. Авторизация не
// выполняется: движок вызывается без учётных данных, поэтому разделы под защитой
// не читаются.
type CollectSite struct {
	// StartURL — адрес сайта клиента. Обязателен.
	StartURL string
	// MaxPages — бюджет страниц; ноль — явный лимит не задан.
	MaxPages int
	// MaxDepth — максимальная глубина обхода; ноль — только стартовая страница.
	MaxDepth int
	// MaxTimeSeconds — предельное время обхода; ноль — лимит не задан.
	MaxTimeSeconds int
}

// CollectedPage — очищенная страница сайта.
type CollectedPage struct {
	URL  string `json:"url"`
	Text string `json:"text"`
}

// CollectSiteResult — исход сбора. Available=false означает честную
// недоступность: сбор не выполнен, работа продолжается на ручных материалах.
type CollectSiteResult struct {
	Available bool            `json:"available"`
	Reason    string          `json:"reason,omitempty"`
	Pages     []CollectedPage `json:"pages,omitempty"`
	MaxPages  int             `json:"max_pages"`
	MaxDepth  int             `json:"max_depth"`
	// ExcludedPersonal — исключённые сведения о физических лицах: факт исключения
	// сообщается.
	ExcludedPersonal []string `json:"excluded_personal,omitempty"`
}

// CollectSiteHandler — use case сбора сведений с сайта.
type CollectSiteHandler struct {
	collector crawl.Port
}

// NewCollectSiteHandler собирает use case поверх порта движка сбора.
func NewCollectSiteHandler(collector crawl.Port) *CollectSiteHandler {
	return &CollectSiteHandler{collector: collector}
}

// Handle проверяет границы обхода и собирает страницы. Недоступность движка или
// сайта не прерывает работу: результат честно помечается недоступным, а собранный
// текст очищается от сведений о физических лицах.
func (h *CollectSiteHandler) Handle(ctx context.Context, cmd CollectSite) (CollectSiteResult, error) {
	request := crawl.Request{
		StartURL:       strings.TrimSpace(cmd.StartURL),
		MaxPages:       cmd.MaxPages,
		MaxDepth:       cmd.MaxDepth,
		MaxTimeSeconds: cmd.MaxTimeSeconds,
	}
	if err := request.Validate(); err != nil {
		return CollectSiteResult{}, fmt.Errorf("%w: %v", ErrCollectionInput, err)
	}

	result := CollectSiteResult{MaxPages: cmd.MaxPages, MaxDepth: cmd.MaxDepth}
	if h.collector == nil {
		result.Reason = "движок сбора сведений не сконфигурирован: работа продолжается на предоставленных вручную материалах"
		return result, nil
	}

	outcome, err := h.collector.Crawl(ctx, request)
	if err != nil {
		result.Reason = collectionFailureReason(err)
		return result, nil
	}
	result.Available = true
	for _, page := range outcome.Pages {
		clean, excluded := domain.ExcludePersonalData(page.Markdown)
		result.ExcludedPersonal = append(result.ExcludedPersonal, excluded...)
		result.Pages = append(result.Pages, CollectedPage{URL: page.URL, Text: clean})
	}
	return result, nil
}

// collectionFailureReason объясняет недоступность сбора, отделяя раздел под
// авторизацией от прочего сбоя: обход защиты не выполняется.
func collectionFailureReason(err error) string {
	lower := strings.ToLower(err.Error())
	for _, marker := range []string{"auth", "unauthor", "forbidden", "401", "403"} {
		if strings.Contains(lower, marker) {
			return "раздел требует авторизации или обхода защиты: сбор по нему не выполняется"
		}
	}
	return "сайт недоступен или обход завершился без результата: " + err.Error()
}
