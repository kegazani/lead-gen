package domain

import (
	"fmt"
	"strings"
	"time"
)

// Сохранённое состояние агрегатов и их сущностей.
//
// Адаптер репозитория не имеет доступа к закрытым полям агрегатов, поэтому он
// читает и пишет снимки с экспортированными полями. Доменный объект из снимка
// собирается только через Rehydrate-функции, которые проверяют инварианты и не
// эмитят события: восстановление не является новым фактом домена.

// ContentVersionSnapshot — сохранённое состояние версии контента.
type ContentVersionSnapshot struct {
	ID        ContentVersionID
	Revision  int
	Body      string
	Locale    ContentLocale
	CreatedAt time.Time
	CreatedBy ActorID
}

// LandingSectionSnapshot — сохранённое состояние блока лендинга.
type LandingSectionSnapshot struct {
	Heading string
	Body    string
}

// LandingContentSnapshot — сохранённое состояние наполнения лендинга.
type LandingContentSnapshot struct {
	ID        LandingContentID
	VersionID ContentVersionID
	Title     string
	Headline  string
	Intro     string
	Sections  []LandingSectionSnapshot
}

// DocumentSnapshot — сохранённое состояние документа.
type DocumentSnapshot struct {
	ID        DocumentID
	VersionID ContentVersionID
	Title     string
	Body      string
}

// SlideSnapshot — сохранённое состояние слайда презентации.
type SlideSnapshot struct {
	Heading string
	Bullets []string
}

// PresentationSnapshot — сохранённое состояние презентации.
type PresentationSnapshot struct {
	Title  string
	Slides []SlideSnapshot
}

// RenderedArtifactSnapshot — сохранённое состояние артефакта рендера.
type RenderedArtifactSnapshot struct {
	Format       RenderFormat
	Ref          ArtifactRef
	Title        string
	Landing      *LandingContentSnapshot
	Document     *DocumentSnapshot
	Presentation *PresentationSnapshot
}

// ContentPackageSnapshot — полное сохранённое состояние aggregate root
// content-package.
type ContentPackageSnapshot struct {
	ID       ContentAssetID
	TenantID TenantID
	Locale   ContentLocale
	Versions []ContentVersionSnapshot
	Landing  *LandingContentSnapshot
	Document *DocumentSnapshot
	Version  int
}

// RenderJobSnapshot — полное сохранённое состояние aggregate root render-job.
type RenderJobSnapshot struct {
	ID         RenderJobID
	TenantID   TenantID
	AssetID    ContentAssetID
	VersionID  ContentVersionID
	TemplateID TemplateID
	Format     RenderFormat
	State      RenderState
	Artifact   *RenderedArtifactSnapshot
	Reason     string
	Version    int
}

// Snapshot возвращает состояние контент-пакета для сохранения.
func (p *ContentPackage) Snapshot() ContentPackageSnapshot {
	versions := make([]ContentVersionSnapshot, 0, len(p.versions))
	for _, version := range p.versions {
		versions = append(versions, version.Snapshot())
	}
	return ContentPackageSnapshot{
		ID:       p.id,
		TenantID: p.tenantID,
		Locale:   p.locale,
		Versions: versions,
		Landing:  landingSnapshot(p.landing),
		Document: documentSnapshot(p.document),
		Version:  p.version,
	}
}

// Snapshot возвращает состояние задания рендеринга для сохранения.
func (j *RenderJob) Snapshot() RenderJobSnapshot {
	return RenderJobSnapshot{
		ID:         j.id,
		TenantID:   j.tenantID,
		AssetID:    j.assetID,
		VersionID:  j.versionID,
		TemplateID: j.templateID,
		Format:     j.format,
		State:      j.state,
		Artifact:   artifactSnapshot(j.artifact),
		Reason:     j.reason,
		Version:    j.version,
	}
}

// landingSnapshot переводит наполнение лендинга в снимок, сохраняя отсутствие как
// nil.
func landingSnapshot(content *LandingContent) *LandingContentSnapshot {
	if content == nil {
		return nil
	}
	sections := make([]LandingSectionSnapshot, 0, len(content.sections))
	for _, section := range content.sections {
		sections = append(sections, LandingSectionSnapshot{Heading: section.heading, Body: section.body})
	}
	return &LandingContentSnapshot{
		ID:        content.id,
		VersionID: content.versionID,
		Title:     content.title,
		Headline:  content.headline,
		Intro:     content.intro,
		Sections:  sections,
	}
}

// documentSnapshot переводит документ в снимок, сохраняя отсутствие как nil.
func documentSnapshot(document *Document) *DocumentSnapshot {
	if document == nil {
		return nil
	}
	return &DocumentSnapshot{
		ID:        document.id,
		VersionID: document.versionID,
		Title:     document.title,
		Body:      document.body,
	}
}

// artifactSnapshot переводит артефакт в снимок, сохраняя отсутствие как nil.
func artifactSnapshot(artifact *RenderedArtifact) *RenderedArtifactSnapshot {
	if artifact == nil {
		return nil
	}
	snapshot := &RenderedArtifactSnapshot{
		Format: artifact.format,
		Ref:    artifact.ref,
		Title:  artifact.title,
	}
	if artifact.landing != nil {
		snapshot.Landing = landingSnapshot(artifact.landing)
	}
	if artifact.document != nil {
		snapshot.Document = documentSnapshot(artifact.document)
	}
	if artifact.presentation != nil {
		slides := make([]SlideSnapshot, 0, len(artifact.presentation.slides))
		for _, slide := range artifact.presentation.slides {
			slides = append(slides, SlideSnapshot{Heading: slide.heading, Bullets: append([]string(nil), slide.bullets...)})
		}
		snapshot.Presentation = &PresentationSnapshot{Title: artifact.presentation.title, Slides: slides}
	}
	return snapshot
}

// RehydrateContentPackage восстанавливает aggregate root из сохранённого
// состояния, проверяя инварианты: локаль задана, версии непусты и пронумерованы
// подряд с единицы, текст версий непуст, прикреплённое наполнение ссылается на
// существующую версию, версия агрегата положительна. События не эмитятся.
func RehydrateContentPackage(snapshot ContentPackageSnapshot) (*ContentPackage, error) {
	if err := snapshot.ID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.TenantID.validate(); err != nil {
		return nil, err
	}
	if snapshot.Locale.IsZero() {
		return nil, fmt.Errorf("%w: локаль пакета %s не задана", ErrInvalidSnapshot, snapshot.ID)
	}
	if len(snapshot.Versions) == 0 {
		return nil, fmt.Errorf("%w: пакет %s без версий", ErrInvalidSnapshot, snapshot.ID)
	}
	if snapshot.Version <= 0 {
		return nil, fmt.Errorf("%w: пакет %s версии %d", ErrInvalidSnapshot, snapshot.ID, snapshot.Version)
	}

	versions := make([]ContentVersion, 0, len(snapshot.Versions))
	seen := make(map[ContentVersionID]struct{}, len(snapshot.Versions))
	for index, version := range snapshot.Versions {
		if err := version.ID.validate(); err != nil {
			return nil, fmt.Errorf("%w: пакет %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if version.Revision != index+1 {
			return nil, fmt.Errorf("%w: пакет %s: ревизия %d вместо %d", ErrInvalidSnapshot, snapshot.ID, version.Revision, index+1)
		}
		if strings.TrimSpace(version.Body) == "" {
			return nil, fmt.Errorf("%w: пакет %s: версия %s без текста", ErrInvalidSnapshot, snapshot.ID, version.ID)
		}
		if version.Locale.IsZero() {
			return nil, fmt.Errorf("%w: пакет %s: версия %s без локали", ErrInvalidSnapshot, snapshot.ID, version.ID)
		}
		if version.CreatedAt.IsZero() {
			return nil, fmt.Errorf("%w: пакет %s: версия %s без времени", ErrInvalidSnapshot, snapshot.ID, version.ID)
		}
		if _, ok := seen[version.ID]; ok {
			return nil, fmt.Errorf("%w: пакет %s: повтор версии %s", ErrInvalidSnapshot, snapshot.ID, version.ID)
		}
		seen[version.ID] = struct{}{}
		versions = append(versions, ContentVersion{
			id:        version.ID,
			revision:  version.Revision,
			body:      version.Body,
			locale:    version.Locale,
			createdAt: version.CreatedAt,
			createdBy: version.CreatedBy,
		})
	}

	pkg := &ContentPackage{
		id:       snapshot.ID,
		tenantID: snapshot.TenantID,
		locale:   snapshot.Locale,
		versions: versions,
		version:  snapshot.Version,
	}
	if snapshot.Landing != nil {
		landing, err := rehydrateLanding(*snapshot.Landing)
		if err != nil {
			return nil, fmt.Errorf("%w: пакет %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if _, ok := seen[landing.VersionID()]; !ok {
			return nil, fmt.Errorf("%w: пакет %s: лендинг ссылается на неизвестную версию %s", ErrInvalidSnapshot, snapshot.ID, landing.VersionID())
		}
		pkg.landing = &landing
	}
	if snapshot.Document != nil {
		document, err := rehydrateDocument(*snapshot.Document)
		if err != nil {
			return nil, fmt.Errorf("%w: пакет %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if _, ok := seen[document.VersionID()]; !ok {
			return nil, fmt.Errorf("%w: пакет %s: документ ссылается на неизвестную версию %s", ErrInvalidSnapshot, snapshot.ID, document.VersionID())
		}
		pkg.document = &document
	}
	return pkg, nil
}

// RehydrateRenderJob восстанавливает aggregate root из сохранённого состояния,
// проверяя инварианты: известность состояния, точную версию и шаблон, наличие
// артефакта ровно в состоянии rendered и его соответствие формату, причину отказа
// в состоянии failed. События не эмитятся.
func RehydrateRenderJob(snapshot RenderJobSnapshot) (*RenderJob, error) {
	if err := snapshot.ID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := snapshot.AssetID.validate(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(snapshot.VersionID)) == "" {
		return nil, ErrVersionRequired
	}
	if strings.TrimSpace(string(snapshot.TemplateID)) == "" {
		return nil, ErrTemplateRequired
	}
	if err := snapshot.Format.validate(); err != nil {
		return nil, err
	}
	if !snapshot.State.isValid() {
		return nil, fmt.Errorf("%w: задание %s: %q", ErrUnknownRenderState, snapshot.ID, snapshot.State)
	}
	if snapshot.Version <= 0 {
		return nil, fmt.Errorf("%w: задание %s версии %d", ErrInvalidSnapshot, snapshot.ID, snapshot.Version)
	}

	job := &RenderJob{
		id:         snapshot.ID,
		tenantID:   snapshot.TenantID,
		assetID:    snapshot.AssetID,
		versionID:  snapshot.VersionID,
		templateID: snapshot.TemplateID,
		format:     snapshot.Format,
		state:      snapshot.State,
		reason:     snapshot.Reason,
		version:    snapshot.Version,
	}

	switch snapshot.State {
	case RenderStateRendered:
		if snapshot.Artifact == nil {
			return nil, fmt.Errorf("%w: задание %s без артефакта", ErrArtifactRequired, snapshot.ID)
		}
		artifact, err := rehydrateArtifact(*snapshot.Artifact)
		if err != nil {
			return nil, fmt.Errorf("%w: задание %s: %v", ErrInvalidSnapshot, snapshot.ID, err)
		}
		if artifact.Format() != snapshot.Format {
			return nil, fmt.Errorf("%w: задание %s: %s ≠ %s", ErrArtifactFormatMismatch, snapshot.ID, snapshot.Format, artifact.Format())
		}
		job.artifact = &artifact
	case RenderStateFailed:
		if strings.TrimSpace(snapshot.Reason) == "" {
			return nil, fmt.Errorf("%w: задание %s", ErrReasonRequired, snapshot.ID)
		}
		if snapshot.Artifact != nil {
			return nil, fmt.Errorf("%w: задание %s: отказ с артефактом", ErrInvalidSnapshot, snapshot.ID)
		}
	default:
		if snapshot.Artifact != nil {
			return nil, fmt.Errorf("%w: задание %s: артефакт до завершения рендера", ErrInvalidSnapshot, snapshot.ID)
		}
	}
	return job, nil
}

// rehydrateLanding восстанавливает наполнение лендинга из снимка.
func rehydrateLanding(snapshot LandingContentSnapshot) (LandingContent, error) {
	sections := make([]LandingSection, 0, len(snapshot.Sections))
	for _, section := range snapshot.Sections {
		rebuilt, err := NewLandingSection(section.Heading, section.Body)
		if err != nil {
			return LandingContent{}, err
		}
		sections = append(sections, rebuilt)
	}
	return NewLandingContent(snapshot.ID, snapshot.VersionID, snapshot.Title, snapshot.Headline, snapshot.Intro, sections)
}

// rehydrateDocument восстанавливает документ из снимка.
func rehydrateDocument(snapshot DocumentSnapshot) (Document, error) {
	return NewDocument(snapshot.ID, snapshot.VersionID, snapshot.Title, snapshot.Body)
}

// rehydrateArtifact восстанавливает артефакт из снимка.
func rehydrateArtifact(snapshot RenderedArtifactSnapshot) (RenderedArtifact, error) {
	switch snapshot.Format {
	case RenderFormatLandingPage:
		if snapshot.Landing == nil {
			return RenderedArtifact{}, fmt.Errorf("%w: артефакт %s без лендинга", ErrArtifactFormatMismatch, snapshot.Format)
		}
		content, err := rehydrateLanding(*snapshot.Landing)
		if err != nil {
			return RenderedArtifact{}, err
		}
		return NewLandingArtifact(snapshot.Ref, content), nil
	case RenderFormatDocument:
		if snapshot.Document == nil {
			return RenderedArtifact{}, fmt.Errorf("%w: артефакт %s без документа", ErrArtifactFormatMismatch, snapshot.Format)
		}
		document, err := rehydrateDocument(*snapshot.Document)
		if err != nil {
			return RenderedArtifact{}, err
		}
		return NewDocumentArtifact(snapshot.Ref, document), nil
	case RenderFormatPresentation:
		if snapshot.Presentation == nil {
			return RenderedArtifact{}, fmt.Errorf("%w: артефакт %s без презентации", ErrArtifactFormatMismatch, snapshot.Format)
		}
		presentation, err := rehydratePresentation(*snapshot.Presentation)
		if err != nil {
			return RenderedArtifact{}, err
		}
		return NewPresentationArtifact(snapshot.Ref, presentation), nil
	default:
		return RenderedArtifact{}, fmt.Errorf("%w: %q", ErrUnknownRenderFormat, snapshot.Format)
	}
}

// rehydratePresentation восстанавливает презентацию из снимка.
func rehydratePresentation(snapshot PresentationSnapshot) (Presentation, error) {
	slides := make([]Slide, 0, len(snapshot.Slides))
	for _, slide := range snapshot.Slides {
		rebuilt, err := NewSlide(slide.Heading, slide.Bullets)
		if err != nil {
			return Presentation{}, err
		}
		slides = append(slides, rebuilt)
	}
	return NewPresentation(snapshot.Title, slides)
}
