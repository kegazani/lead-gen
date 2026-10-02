package domain

import (
	"fmt"
	"strings"
)

// Артефакты производства контента: наполнение лендинга (landing-content),
// документ (document) и презентация. Лендинг и документ — сущности контент-пакета
// (каталог: aggregates.content-package.entities), презентация — форма рендера,
// объявленная контрактом rendered-artifact.
//
// Разметка в текстах не допускается: потребитель экранирует значения сам.

// LandingSection — блок наполнения лендинга.
type LandingSection struct {
	heading string
	body    string
}

// NewLandingSection собирает блок наполнения. Заголовок может быть пустым,
// текст непустой.
func NewLandingSection(heading, body string) (LandingSection, error) {
	text := strings.TrimSpace(body)
	if text == "" {
		return LandingSection{}, fmt.Errorf("%w: блок лендинга без текста", ErrEmptyContent)
	}
	return LandingSection{heading: strings.TrimSpace(heading), body: text}, nil
}

// Heading возвращает заголовок блока.
func (s LandingSection) Heading() string { return s.heading }

// Body возвращает текст блока.
func (s LandingSection) Body() string { return s.body }

// LandingContent — наполнение сгенерированного лендинга для конкретной версии
// контента. Неизменяемо: правка порождает новое наполнение.
type LandingContent struct {
	id        LandingContentID
	versionID ContentVersionID
	title     string
	headline  string
	intro     string
	sections  []LandingSection
}

// NewLandingContent собирает наполнение лендинга. Версия обязана существовать в
// контент-пакете; проверку ссылки выполняет ContentPackage.AttachLandingContent.
func NewLandingContent(id LandingContentID, versionID ContentVersionID, title, headline, intro string, sections []LandingSection) (LandingContent, error) {
	if err := id.validate(); err != nil {
		return LandingContent{}, err
	}
	if err := versionID.validate(); err != nil {
		return LandingContent{}, err
	}
	if strings.TrimSpace(title) == "" {
		return LandingContent{}, fmt.Errorf("%w: заголовок лендинга пуст", ErrEmptyContent)
	}
	if len(sections) == 0 {
		return LandingContent{}, fmt.Errorf("%w: лендинг без блоков", ErrEmptyContent)
	}
	return LandingContent{
		id:        id,
		versionID: versionID,
		title:     strings.TrimSpace(title),
		headline:  strings.TrimSpace(headline),
		intro:     strings.TrimSpace(intro),
		sections:  append([]LandingSection(nil), sections...),
	}, nil
}

// ID возвращает идентификатор наполнения.
func (c LandingContent) ID() LandingContentID { return c.id }

// VersionID возвращает версию контента, из которой построено наполнение.
func (c LandingContent) VersionID() ContentVersionID { return c.versionID }

// Title возвращает заголовок окна.
func (c LandingContent) Title() string { return c.title }

// Headline возвращает главный заголовок страницы.
func (c LandingContent) Headline() string { return c.headline }

// Intro возвращает вводный абзац.
func (c LandingContent) Intro() string { return c.intro }

// Sections возвращает блоки наполнения в порядке показа.
func (c LandingContent) Sections() []LandingSection {
	return append([]LandingSection(nil), c.sections...)
}

// Document — сгенерированный документ для конкретной версии контента.
// Неизменяем.
type Document struct {
	id        DocumentID
	versionID ContentVersionID
	title     string
	body      string
}

// NewDocument собирает документ. Ссылку на версию проверяет
// ContentPackage.AttachDocument.
func NewDocument(id DocumentID, versionID ContentVersionID, title, body string) (Document, error) {
	if err := id.validate(); err != nil {
		return Document{}, err
	}
	if err := versionID.validate(); err != nil {
		return Document{}, err
	}
	if strings.TrimSpace(title) == "" {
		return Document{}, fmt.Errorf("%w: заголовок документа пуст", ErrEmptyContent)
	}
	text := strings.TrimSpace(body)
	if text == "" {
		return Document{}, fmt.Errorf("%w: тело документа пусто", ErrEmptyContent)
	}
	return Document{id: id, versionID: versionID, title: strings.TrimSpace(title), body: text}, nil
}

// ID возвращает идентификатор документа.
func (d Document) ID() DocumentID { return d.id }

// VersionID возвращает версию контента, из которой построен документ.
func (d Document) VersionID() ContentVersionID { return d.versionID }

// Title возвращает заголовок документа.
func (d Document) Title() string { return d.title }

// Body возвращает тело документа.
func (d Document) Body() string { return d.body }

// Slide — слайд презентации.
type Slide struct {
	heading string
	bullets []string
}

// NewSlide собирает слайд. Пункты непусты.
func NewSlide(heading string, bullets []string) (Slide, error) {
	clean := make([]string, 0, len(bullets))
	for _, bullet := range bullets {
		if trimmed := strings.TrimSpace(bullet); trimmed != "" {
			clean = append(clean, trimmed)
		}
	}
	if len(clean) == 0 {
		return Slide{}, fmt.Errorf("%w: слайд без пунктов", ErrEmptyContent)
	}
	return Slide{heading: strings.TrimSpace(heading), bullets: clean}, nil
}

// Heading возвращает заголовок слайда.
func (s Slide) Heading() string { return s.heading }

// Bullets возвращает пункты слайда в порядке показа.
func (s Slide) Bullets() []string { return append([]string(nil), s.bullets...) }

// Presentation — презентация для публичной страницы.
type Presentation struct {
	title  string
	slides []Slide
}

// NewPresentation собирает презентацию.
func NewPresentation(title string, slides []Slide) (Presentation, error) {
	if strings.TrimSpace(title) == "" {
		return Presentation{}, fmt.Errorf("%w: заголовок презентации пуст", ErrEmptyContent)
	}
	if len(slides) == 0 {
		return Presentation{}, fmt.Errorf("%w: презентация без слайдов", ErrEmptyContent)
	}
	return Presentation{title: strings.TrimSpace(title), slides: append([]Slide(nil), slides...)}, nil
}

// Title возвращает заголовок презентации.
func (p Presentation) Title() string { return p.title }

// Slides возвращает слайды в порядке показа.
func (p Presentation) Slides() []Slide { return append([]Slide(nil), p.slides...) }

// RenderedArtifact — воспроизведённый артефакт задания рендеринга. Ровно одно из
// полей-наполнений соответствует формату; проверку выполняет Validate.
type RenderedArtifact struct {
	ref          ArtifactRef
	format       RenderFormat
	title        string
	landing      *LandingContent
	document     *Document
	presentation *Presentation
}

// NewLandingArtifact собирает артефакт-лендинг.
func NewLandingArtifact(ref ArtifactRef, content LandingContent) RenderedArtifact {
	return RenderedArtifact{ref: ref, format: RenderFormatLandingPage, title: content.Title(), landing: &content}
}

// NewDocumentArtifact собирает артефакт-документ.
func NewDocumentArtifact(ref ArtifactRef, document Document) RenderedArtifact {
	return RenderedArtifact{ref: ref, format: RenderFormatDocument, title: document.Title(), document: &document}
}

// NewPresentationArtifact собирает артефакт-презентацию.
func NewPresentationArtifact(ref ArtifactRef, presentation Presentation) RenderedArtifact {
	return RenderedArtifact{ref: ref, format: RenderFormatPresentation, title: presentation.Title(), presentation: &presentation}
}

// Ref возвращает непрозрачную ссылку на артефакт.
func (a RenderedArtifact) Ref() ArtifactRef { return a.ref }

// Format возвращает формат артефакта.
func (a RenderedArtifact) Format() RenderFormat { return a.format }

// Title возвращает заголовок артефакта.
func (a RenderedArtifact) Title() string { return a.title }

// Landing возвращает наполнение лендинга и признак его наличия.
func (a RenderedArtifact) Landing() (LandingContent, bool) {
	if a.landing == nil {
		return LandingContent{}, false
	}
	return *a.landing, true
}

// Document возвращает документ и признак его наличия.
func (a RenderedArtifact) Document() (Document, bool) {
	if a.document == nil {
		return Document{}, false
	}
	return *a.document, true
}

// Presentation возвращает презентацию и признак её наличия.
func (a RenderedArtifact) Presentation() (Presentation, bool) {
	if a.presentation == nil {
		return Presentation{}, false
	}
	return *a.presentation, true
}

// Validate проверяет, что артефакт несёт ровно то наполнение, которое требует его
// формат. Порождается форматом, а не вызывающим.
func (a RenderedArtifact) Validate() error {
	if err := a.format.validate(); err != nil {
		return err
	}
	switch a.format {
	case RenderFormatLandingPage:
		if a.landing == nil || a.document != nil || a.presentation != nil {
			return fmt.Errorf("%w: %s", ErrArtifactFormatMismatch, a.format)
		}
	case RenderFormatDocument:
		if a.document == nil || a.landing != nil || a.presentation != nil {
			return fmt.Errorf("%w: %s", ErrArtifactFormatMismatch, a.format)
		}
	case RenderFormatPresentation:
		if a.presentation == nil || a.landing != nil || a.document != nil {
			return fmt.Errorf("%w: %s", ErrArtifactFormatMismatch, a.format)
		}
	}
	return nil
}
