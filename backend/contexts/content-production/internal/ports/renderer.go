package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// Ошибки порта рендеринга.
var (
	// ErrRenderUnavailable — движок рендеринга недоступен: он не подключён или
	// произошёл сбой транспорта. Рендер не выдаётся за успех.
	ErrRenderUnavailable = errors.New("порты: движок рендеринга недоступен")

	// ErrRenderRejected — движок рендеринга отказал по причине качества входных
	// данных (пустой шаблон, непригодный текст). Отличается от сбоя транспорта.
	ErrRenderRejected = errors.New("порты: движок рендеринга отклонил задание")
)

// RenderRequest — вход порта рендеринга: точная версия контента, шаблон и
// целевой формат. Порт не знает, каким движком и какими capability
// (linguistics, presentation-generator, landing-page-generator) он реализован.
type RenderRequest struct {
	AssetID    domain.ContentAssetID
	VersionID  domain.ContentVersionID
	TemplateID domain.TemplateID
	Format     domain.RenderFormat
	Locale     domain.ContentLocale
	Body       string
}

// RenderResult — результат рендеринга: наполнение, пригодное для сборки артефакта
// нужного формата. Заполняется ровно то подмножество полей, которое соответствует
// формату запроса.
type RenderResult struct {
	// Title — заголовок артефакта.
	Title string
	// Headline — главный заголовок лендинга (формат landing-page).
	Headline string
	// Intro — вводный абзац лендинга (формат landing-page).
	Intro string
	// Sections — блоки лендинга (формат landing-page).
	Sections []domain.LandingSection
	// Slides — слайды презентации (формат presentation).
	Slides []domain.Slide
	// Body — тело документа (формат document).
	Body string
}

// Renderer — порт внешнего движка производства контента.
//
// Рендер — внешняя операция, поэтому домен и app его не выполняют: движок
// подключается адаптером. Реализация обязана принимать context.Context и уважать
// дедлайн и отмену. В срезе порт реализует детерминированный адаптер-заглушка,
// названный честно (internal/adapters/renderer/deterministic): он не обращается к
// реальному движку и не является им.
type Renderer interface {
	Render(ctx context.Context, req RenderRequest) (RenderResult, error)
}
