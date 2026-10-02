package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// GetLanding — запрос наполнения воспроизведённого лендинга по ссылке. Ссылка
// лендинга — контент-пакет, из которого он построен.
type GetLanding struct {
	Ref domain.ArtifactRef
}

// LandingPageView — наполнение лендинга для контракта rendered-artifact.
type LandingPageView struct {
	Ref      domain.ArtifactRef
	Title    string
	Headline string
	Intro    string
	Sections []LandingSectionView
}

// GetLandingHandler — use case чтения наполнения лендинга.
type GetLandingHandler struct {
	packages ports.ContentPackageRepository
}

// NewGetLandingHandler собирает use case чтения лендинга.
func NewGetLandingHandler(packages ports.ContentPackageRepository) *GetLandingHandler {
	return &GetLandingHandler{packages: packages}
}

// Handle загружает пакет по ссылке и отдаёт наполнение лендинга. Если пакета нет
// — ошибка «не найдено»; если рендер ещё не завершён — ErrArtifactNotReady.
func (h *GetLandingHandler) Handle(ctx context.Context, q GetLanding) (LandingPageView, error) {
	pkg, err := h.packages.FindByID(ctx, domain.ContentAssetID(q.Ref))
	if err != nil {
		return LandingPageView{}, err
	}
	landing, ok := pkg.LandingContent()
	if !ok {
		return LandingPageView{}, fmt.Errorf("%w: %s", ErrArtifactNotReady, q.Ref)
	}
	view := LandingPageView{
		Ref:      q.Ref,
		Title:    landing.Title(),
		Headline: landing.Headline(),
		Intro:    landing.Intro(),
	}
	for _, section := range landing.Sections() {
		view.Sections = append(view.Sections, LandingSectionView{Heading: section.Heading(), Body: section.Body()})
	}
	return view, nil
}

// GetPresentation — запрос наполнения воспроизведённой презентации по ссылке.
// Ссылка презентации — задание рендеринга, которым она построена.
type GetPresentation struct {
	Ref domain.ArtifactRef
}

// SlideView — представление слайда презентации.
type SlideView struct {
	Heading string
	Bullets []string
}

// PresentationView — наполнение презентации для контракта rendered-artifact.
type PresentationView struct {
	Ref    domain.ArtifactRef
	Title  string
	Slides []SlideView
}

// GetPresentationHandler — use case чтения наполнения презентации.
type GetPresentationHandler struct {
	renderJobs ports.RenderJobRepository
}

// NewGetPresentationHandler собирает use case чтения презентации.
func NewGetPresentationHandler(renderJobs ports.RenderJobRepository) *GetPresentationHandler {
	return &GetPresentationHandler{renderJobs: renderJobs}
}

// Handle загружает задание по ссылке и отдаёт наполнение презентации. Если
// задания нет — ошибка «не найдено»; если рендер ещё не завершён или артефакт не
// презентация — ErrArtifactNotReady.
func (h *GetPresentationHandler) Handle(ctx context.Context, q GetPresentation) (PresentationView, error) {
	job, err := h.renderJobs.FindByID(ctx, domain.RenderJobID(q.Ref))
	if err != nil {
		return PresentationView{}, err
	}
	artifact, ok := job.Artifact()
	if !ok {
		return PresentationView{}, fmt.Errorf("%w: %s", ErrArtifactNotReady, q.Ref)
	}
	presentation, ok := artifact.Presentation()
	if !ok {
		return PresentationView{}, fmt.Errorf("%w: %s", ErrArtifactNotReady, q.Ref)
	}
	view := PresentationView{Ref: q.Ref, Title: presentation.Title()}
	for _, slide := range presentation.Slides() {
		view.Slides = append(view.Slides, SlideView{Heading: slide.Heading(), Bullets: slide.Bullets()})
	}
	return view, nil
}
