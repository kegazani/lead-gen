package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// GetContent — запрос контент-пакета по идентификатору.
type GetContent struct {
	AssetID  domain.ContentAssetID
	TenantID domain.TenantID
}

// VersionView — представление версии контента.
type VersionView struct {
	VersionID domain.ContentVersionID
	Revision  int
	Body      string
	CreatedAt time.Time
	CreatedBy domain.ActorID
}

// LandingSectionView — представление блока лендинга.
type LandingSectionView struct {
	Heading string
	Body    string
}

// LandingView — представление наполнения лендинга.
type LandingView struct {
	VersionID domain.ContentVersionID
	Title     string
	Headline  string
	Intro     string
	Sections  []LandingSectionView
}

// DocumentView — представление документа.
type DocumentView struct {
	VersionID domain.ContentVersionID
	Title     string
	Body      string
}

// ContentView — представление контент-пакета для чтения.
type ContentView struct {
	AssetID         domain.ContentAssetID
	TenantID        domain.TenantID
	Language        string
	Region          string
	Locale          string
	Versions        []VersionView
	LatestVersionID domain.ContentVersionID
	Landing         *LandingView
	Document        *DocumentView
}

// GetContentHandler — use case чтения контент-пакета.
type GetContentHandler struct {
	packages ports.ContentPackageRepository
}

// NewGetContentHandler собирает use case чтения пакета.
func NewGetContentHandler(packages ports.ContentPackageRepository) *GetContentHandler {
	return &GetContentHandler{packages: packages}
}

// Handle загружает пакет и строит представление. Пакет другого тенанта
// неотличим от отсутствующего: его существование не раскрывается.
func (h *GetContentHandler) Handle(ctx context.Context, q GetContent) (ContentView, error) {
	pkg, err := h.packages.FindByID(ctx, q.AssetID)
	if err != nil {
		return ContentView{}, err
	}
	if pkg.TenantID() != q.TenantID {
		return ContentView{}, fmt.Errorf("%w: %s", ports.ErrContentPackageNotFound, q.AssetID)
	}
	return buildContent(pkg), nil
}

// buildContent переводит агрегат в представление чтения.
func buildContent(pkg *domain.ContentPackage) ContentView {
	view := ContentView{
		AssetID:  pkg.ID(),
		TenantID: pkg.TenantID(),
		Language: pkg.Locale().Language(),
		Region:   pkg.Locale().Region(),
		Locale:   pkg.Locale().String(),
	}
	for _, version := range pkg.Versions() {
		view.Versions = append(view.Versions, VersionView{
			VersionID: version.ID(),
			Revision:  version.Revision(),
			Body:      version.Body(),
			CreatedAt: version.CreatedAt(),
			CreatedBy: version.CreatedBy(),
		})
	}
	if latest, ok := pkg.LatestVersion(); ok {
		view.LatestVersionID = latest.ID()
	}
	if landing, ok := pkg.LandingContent(); ok {
		view.Landing = landingView(landing)
	}
	if document, ok := pkg.Document(); ok {
		view.Document = documentView(document)
	}
	return view
}

// landingView переводит наполнение лендинга в представление чтения.
func landingView(content domain.LandingContent) *LandingView {
	view := &LandingView{
		VersionID: content.VersionID(),
		Title:     content.Title(),
		Headline:  content.Headline(),
		Intro:     content.Intro(),
	}
	for _, section := range content.Sections() {
		view.Sections = append(view.Sections, LandingSectionView{Heading: section.Heading(), Body: section.Body()})
	}
	return view
}

// documentView переводит документ в представление чтения.
func documentView(document domain.Document) *DocumentView {
	return &DocumentView{VersionID: document.VersionID(), Title: document.Title(), Body: document.Body()}
}
