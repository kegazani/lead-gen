package contentproduction

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/content-production/renderedartifact"
)

// NewContentRequest собирает опубликованный контракт content-request поверх
// собранного среза контекста: команду производства контента.
//
// Это публичная фабрика владельца контента: точка сборки (cmd/lead-gen) не имеет
// доступа к внутренним пакетам контекста, поэтому прямой адаптер контракта
// объявлен здесь. При co-location он вызывается в процессе; при разделении
// сервисов та же форма даётся сетевым клиентом.
func NewContentRequest(slice *Slice) contentrequest.Port {
	if slice == nil {
		return contentrequest.InProc(contentrequest.InProcHandler{})
	}
	return contentrequest.InProc(contentrequest.InProcHandler{
		RequestFunc: func(ctx context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
			subject := ""
			if in.Subject != nil {
				subject = *in.Subject
			}
			format, err := domain.ParseRenderFormat(in.Format)
			if in.Mode != contentrequest.ModeVersionOnly && err != nil {
				return contentrequest.Result{}, fmt.Errorf("%w: %v", contentrequest.ErrRejected, err)
			}
			result, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
				RequestID:     in.RequestID,
				AssetID:       domain.ContentAssetID(in.AssetID),
				TenantID:      domain.TenantID(in.TenantID),
				Language:      in.Language,
				Region:        in.Region,
				Format:        format,
				TemplateID:    domain.TemplateID(in.TemplateID),
				Body:          in.Body,
				Subject:       subject,
				Mode:          in.Mode,
				Actor:         domain.ActorID(in.Actor),
				CorrelationID: domain.CorrelationID(in.CorrelationID),
			})
			if err != nil {
				return contentrequest.Result{}, contentRequestError(err)
			}
			return contentrequest.Result{
				RequestID:   in.RequestID,
				AssetID:     result.AssetID.String(),
				VersionID:   result.VersionID.String(),
				RenderJobID: result.RenderJobID.String(),
				State:       string(result.State),
			}, nil
		},
	})
}

// NewContentVersion собирает опубликованный контракт content-version поверх
// собранного среза контекста: чтение неизменяемой версии контента.
func NewContentVersion(slice *Slice) contentversion.Source {
	if slice == nil {
		return contentversion.InProc(contentversion.InProcHandler{})
	}
	return contentversion.InProc(contentversion.InProcHandler{
		VersionFunc: func(ctx context.Context, q contentversion.Query) (contentversion.Version, error) {
			view, err := slice.GetContent.Handle(ctx, query.GetContent{
				AssetID:  domain.ContentAssetID(q.AssetID),
				TenantID: domain.TenantID(q.TenantID),
			})
			if err != nil {
				return contentversion.Version{}, contentVersionError(err)
			}
			chosen, ok := selectVersion(view, q.VersionID)
			if !ok {
				return contentversion.Version{}, fmt.Errorf("%w: %s", contentversion.ErrNotFound, q.AssetID)
			}
			subject := chosen.Subject
			if subject == "" {
				subject = contentVersionSubject(view, chosen.VersionID)
			}
			return contentversion.Version{
				AssetID:   view.AssetID.String(),
				VersionID: chosen.VersionID.String(),
				Revision:  chosen.Revision,
				Language:  view.Language,
				Region:    view.Region,
				Subject:   subject,
				Body:      chosen.Body,
			}, nil
		},
	})
}

// RenderedArtifactAdapter реализует источники контракта rendered-artifact поверх
// собранного среза контекста: наполнение лендинга и презентации.
type RenderedArtifactAdapter struct {
	landing      *query.GetLandingHandler
	presentation *query.GetPresentationHandler
}

// NewRenderedArtifact собирает источники контракта rendered-artifact. Возвращённое
// значение пригодно и как LandingSource, и как PresentationSource.
func NewRenderedArtifact(slice *Slice) RenderedArtifactAdapter {
	if slice == nil {
		return RenderedArtifactAdapter{}
	}
	return RenderedArtifactAdapter{landing: slice.GetLanding, presentation: slice.GetPresentation}
}

// Landing реализует renderedartifact.LandingSource. Ссылка — контент-пакет, из
// которого построен лендинг.
func (a RenderedArtifactAdapter) Landing(ctx context.Context, ref renderedartifact.ArtifactRef) (renderedartifact.LandingPage, error) {
	if a.landing == nil {
		return renderedartifact.LandingPage{}, renderedartifact.ErrNotFound
	}
	view, err := a.landing.Handle(ctx, query.GetLanding{Ref: domain.ArtifactRef(ref)})
	if err != nil {
		return renderedartifact.LandingPage{}, renderedArtifactError(err)
	}
	page := renderedartifact.LandingPage{
		Ref:      renderedartifact.ArtifactRef(view.Ref),
		Title:    view.Title,
		Headline: view.Headline,
		Intro:    view.Intro,
	}
	for _, section := range view.Sections {
		page.Sections = append(page.Sections, renderedartifact.Section{Heading: section.Heading, Body: section.Body})
	}
	return page, nil
}

// Presentation реализует renderedartifact.PresentationSource. Ссылка — задание
// рендеринга, которым построена презентация.
func (a RenderedArtifactAdapter) Presentation(ctx context.Context, ref renderedartifact.ArtifactRef) (renderedartifact.Presentation, error) {
	if a.presentation == nil {
		return renderedartifact.Presentation{}, renderedartifact.ErrNotFound
	}
	view, err := a.presentation.Handle(ctx, query.GetPresentation{Ref: domain.ArtifactRef(ref)})
	if err != nil {
		return renderedartifact.Presentation{}, renderedArtifactError(err)
	}
	deck := renderedartifact.Presentation{Ref: renderedartifact.ArtifactRef(view.Ref), Title: view.Title}
	for _, slide := range view.Slides {
		deck.Slides = append(deck.Slides, renderedartifact.Slide{Heading: slide.Heading, Bullets: append([]string(nil), slide.Bullets...)})
	}
	return deck, nil
}

// contentVersionSubject возвращает тему материала письма — заголовок материала,
// прикреплённого к выбранной версии контента. Материал письма — текст версии,
// порождённый рендером документ (или лендинг); заголовок берётся у доступного
// представления материала с той же версией. Материала с заголовком у версии нет —
// пустая строка: тема не выдумывается (AGENTS.md §3).
func contentVersionSubject(view query.ContentView, versionID domain.ContentVersionID) string {
	if view.Document != nil && view.Document.VersionID == versionID {
		return view.Document.Title
	}
	if view.Landing != nil && view.Landing.VersionID == versionID {
		return view.Landing.Title
	}
	return ""
}

// selectVersion выбирает версию по запросу: конкретную при непустом VersionID,
// иначе последнюю версию пакета.
func selectVersion(view query.ContentView, versionID string) (query.VersionView, bool) {
	if versionID != "" {
		for _, version := range view.Versions {
			if version.VersionID.String() == versionID {
				return version, true
			}
		}
		return query.VersionView{}, false
	}
	for _, version := range view.Versions {
		if version.VersionID == view.LatestVersionID {
			return version, true
		}
	}
	if len(view.Versions) > 0 {
		return view.Versions[len(view.Versions)-1], true
	}
	return query.VersionView{}, false
}

// contentRequestError переводит ошибку сценария производства в исход контракта
// content-request: доменный отказ — ErrRejected, прочее — ErrUnavailable.
func contentRequestError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, command.ErrPackageRequired),
		errors.Is(err, command.ErrUnknownContentMode),
		errors.Is(err, command.ErrVersionOnlyConflict),
		errors.Is(err, command.ErrVersionRequired),
		errors.Is(err, command.ErrTenantMismatch),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrEmptyContent),
		errors.Is(err, domain.ErrInvalidLocale),
		errors.Is(err, domain.ErrUnknownRenderFormat),
		errors.Is(err, domain.ErrTemplateRequired),
		errors.Is(err, domain.ErrVersionRequired):
		return fmt.Errorf("%w: %v", contentrequest.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", contentrequest.ErrUnavailable, err)
	}
}

// contentVersionError переводит ошибку чтения в исход контракта content-version:
// отсутствие пакета — ErrNotFound, прочее — ErrUnavailable.
func contentVersionError(err error) error {
	switch {
	case errors.Is(err, ports.ErrContentPackageNotFound):
		return fmt.Errorf("%w: %v", contentversion.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", contentversion.ErrUnavailable, err)
	}
}

// renderedArtifactError переводит ошибку чтения артефакта в исход контракта
// rendered-artifact: ненайденное или ещё не готовое — ErrNotFound.
func renderedArtifactError(err error) error {
	switch {
	case errors.Is(err, ports.ErrContentPackageNotFound),
		errors.Is(err, ports.ErrRenderJobNotFound),
		errors.Is(err, query.ErrArtifactNotReady):
		return fmt.Errorf("%w: %v", renderedartifact.ErrNotFound, err)
	default:
		return err
	}
}

// Проверка соответствия контрактам на этапе компиляции.
var (
	_ renderedartifact.LandingSource      = RenderedArtifactAdapter{}
	_ renderedartifact.PresentationSource = RenderedArtifactAdapter{}
)
