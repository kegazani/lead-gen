package domain

import (
	"fmt"
	"strings"
	"time"
)

// ContentVersion — сущность content-version: неизменяемая версия контента.
//
// Политика immutable-content-version: опубликованная версия не редактируется.
// Новый текст создаёт новую версию, а не изменяет существующую, поэтому у
// версии нет методов-мутаторов, а её поля закрыты.
type ContentVersion struct {
	id        ContentVersionID
	revision  int
	subject   string
	body      string
	locale    ContentLocale
	createdAt time.Time
	createdBy ActorID
}

// ID возвращает идентификатор версии.
func (v ContentVersion) ID() ContentVersionID { return v.id }

// Revision возвращает номер версии в пакете (с единицы, возрастает на единицу).
func (v ContentVersion) Revision() int { return v.revision }

// Body возвращает текст версии.
func (v ContentVersion) Body() string { return v.body }

// Subject возвращает точную тему версии; пустая тема допустима.
func (v ContentVersion) Subject() string { return v.subject }

// Locale возвращает локаль версии.
func (v ContentVersion) Locale() ContentLocale { return v.locale }

// CreatedAt возвращает время создания версии.
func (v ContentVersion) CreatedAt() time.Time { return v.createdAt }

// CreatedBy возвращает актора, создавшего версию.
func (v ContentVersion) CreatedBy() ActorID { return v.createdBy }

// Snapshot возвращает сохранённое состояние версии.
func (v ContentVersion) Snapshot() ContentVersionSnapshot {
	return ContentVersionSnapshot{
		ID:        v.id,
		Revision:  v.revision,
		Subject:   v.subject,
		Body:      v.body,
		Locale:    v.locale,
		CreatedAt: v.createdAt,
		CreatedBy: v.createdBy,
	}
}

// ContentPackage — aggregate root content-package (корень content-asset).
//
// Агрегат владеет сущностями:
//
//	content-version — неизменяемые версии текста (политика
//	                  immutable-content-version);
//	landing-content — наполнение лендинга, порождённое рендером версии;
//	document        — документ, порождённый рендером версии.
//
// Инварианты:
//
//	I1 — версия контента неизменяема: новый текст создаёт новую версию;
//	I2 — нумерация версий начинается с единицы и возрастает на единицу;
//	I3 — наполнение лендинга и документ ссылаются на существующую версию пакета;
//	I4 — пакет привязан ровно к одному тенанту.
//
// version — токен оптимистичной блокировки: каждое изменение повышает его на
// единицу, а репозиторий сверяет сохранённую версию с ожидаемой.
type ContentPackage struct {
	id       ContentAssetID
	tenantID TenantID
	locale   ContentLocale
	versions []ContentVersion
	landing  *LandingContent
	document *Document
	version  int
	events   []DomainEvent
}

// NewContentPackageParams — данные создания контент-пакета с первой версией.
type NewContentPackageParams struct {
	ID        ContentAssetID
	TenantID  TenantID
	Locale    ContentLocale
	VersionID ContentVersionID
	Body      string
	Subject   string
	ExactText bool
	Actor     ActorID
	At        time.Time
	EventID   EventID

	CorrelationID CorrelationID
	CausationID   CausationID
}

// NewContentPackage создаёт пакет с первой неизменяемой версией. Пустой текст не
// принимается (I1). Создание фиксируется событием content-version-created.
func NewContentPackage(params NewContentPackageParams) (*ContentPackage, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.VersionID.validate(); err != nil {
		return nil, err
	}
	if params.Locale.IsZero() {
		return nil, fmt.Errorf("%w: локаль пакета не задана", ErrInvalidLocale)
	}
	body, err := normalizeVersionBody(params.Body, params.ExactText)
	if err != nil {
		return nil, err
	}
	header, err := newEventHeader(params.TenantID, params.At, params.EventID, params.CorrelationID, params.CausationID)
	if err != nil {
		return nil, err
	}
	if err := params.Actor.validate(); err != nil {
		return nil, err
	}

	pkg := &ContentPackage{
		id:       params.ID,
		tenantID: params.TenantID,
		locale:   params.Locale,
		version:  1,
	}
	pkg.versions = append(pkg.versions, ContentVersion{
		id:        params.VersionID,
		revision:  1,
		subject:   params.Subject,
		body:      body,
		locale:    params.Locale,
		createdAt: header.OccurredAt,
		createdBy: params.Actor,
	})
	pkg.events = append(pkg.events, ContentVersionCreated{
		EventHeader: header,
		AssetID:     pkg.id,
		VersionID:   params.VersionID,
		Revision:    1,
		Locale:      params.Locale.String(),
	})
	return pkg, nil
}

// PublishVersionInput — данные публикации новой версии.
type PublishVersionInput struct {
	VersionID ContentVersionID
	Body      string
	Subject   string
	ExactText bool
	Actor     ActorID
	At        time.Time
	EventID   EventID

	CorrelationID CorrelationID
	CausationID   CausationID
}

// PublishVersion публикует новый текст как новую неизменяемую версию (I1, I2),
// не трогая прежние. Порождает событие content-version-created.
func (p *ContentPackage) PublishVersion(input PublishVersionInput) (ContentVersion, error) {
	if err := input.VersionID.validate(); err != nil {
		return ContentVersion{}, err
	}
	if err := input.Actor.validate(); err != nil {
		return ContentVersion{}, err
	}
	body, err := normalizeVersionBody(input.Body, input.ExactText)
	if err != nil {
		return ContentVersion{}, err
	}
	header, err := newEventHeader(p.tenantID, input.At, input.EventID, input.CorrelationID, input.CausationID)
	if err != nil {
		return ContentVersion{}, err
	}

	revision := len(p.versions) + 1
	version := ContentVersion{
		id:        input.VersionID,
		revision:  revision,
		subject:   input.Subject,
		body:      body,
		locale:    p.locale,
		createdAt: header.OccurredAt,
		createdBy: input.Actor,
	}
	p.versions = append(p.versions, version)
	p.events = append(p.events, ContentVersionCreated{
		EventHeader: header,
		AssetID:     p.id,
		VersionID:   input.VersionID,
		Revision:    revision,
		Locale:      p.locale.String(),
	})
	p.version++
	return version, nil
}

// AttachLandingContent закрепляет наполнение лендинга за пакетом (I3). Наполнение
// неизменяемо: повторное закрепление заменяет прежнее (например, после повторного
// рендера). Порождает изменение версии, но не доменное событие: факт рендера
// несёт агрегат render-job.
func (p *ContentPackage) AttachLandingContent(landing LandingContent) error {
	if !p.hasVersion(landing.VersionID()) {
		return fmt.Errorf("%w: %s", ErrVersionNotFound, landing.VersionID())
	}
	p.landing = &landing
	p.version++
	return nil
}

// AttachDocument закрепляет документ за пакетом (I3).
func (p *ContentPackage) AttachDocument(document Document) error {
	if !p.hasVersion(document.VersionID()) {
		return fmt.Errorf("%w: %s", ErrVersionNotFound, document.VersionID())
	}
	p.document = &document
	p.version++
	return nil
}

// ID возвращает идентификатор пакета.
func (p *ContentPackage) ID() ContentAssetID { return p.id }

// TenantID возвращает тенанта-владельца пакета.
func (p *ContentPackage) TenantID() TenantID { return p.tenantID }

// Locale возвращает локаль пакета.
func (p *ContentPackage) Locale() ContentLocale { return p.locale }

// Versions возвращает версии в порядке публикации.
func (p *ContentPackage) Versions() []ContentVersion {
	return append([]ContentVersion(nil), p.versions...)
}

// LatestVersion возвращает последнюю версию и признак её наличия. У пакета всегда
// есть хотя бы одна версия.
func (p *ContentPackage) LatestVersion() (ContentVersion, bool) {
	if len(p.versions) == 0 {
		return ContentVersion{}, false
	}
	return p.versions[len(p.versions)-1], true
}

// FindVersion находит версию по идентификатору.
func (p *ContentPackage) FindVersion(id ContentVersionID) (ContentVersion, bool) {
	for _, version := range p.versions {
		if version.id == id {
			return version, true
		}
	}
	return ContentVersion{}, false
}

// LandingContent возвращает наполнение лендинга и признак его наличия.
func (p *ContentPackage) LandingContent() (LandingContent, bool) {
	if p.landing == nil {
		return LandingContent{}, false
	}
	return *p.landing, true
}

// Document возвращает документ и признак его наличия.
func (p *ContentPackage) Document() (Document, bool) {
	if p.document == nil {
		return Document{}, false
	}
	return *p.document, true
}

// Version возвращает версию агрегата — токен оптимистичной блокировки.
func (p *ContentPackage) Version() int { return p.version }

// DomainEvents возвращает копию неподтверждённых событий.
func (p *ContentPackage) DomainEvents() []DomainEvent {
	return append([]DomainEvent(nil), p.events...)
}

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (p *ContentPackage) PullDomainEvents() []DomainEvent {
	events := p.events
	p.events = nil
	return events
}

// hasVersion сообщает, что версия с таким идентификатором есть в пакете.
func (p *ContentPackage) hasVersion(id ContentVersionID) bool {
	_, ok := p.FindVersion(id)
	return ok
}

// normalizeBody обрезает пробелы и отвергает пустой текст (I1).
func normalizeBody(body string) (string, error) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "", ErrEmptyContent
	}
	return trimmed, nil
}

// normalizeVersionBody сохраняет legacy-нормализацию и поддерживает точный текст
// version-only: обязательность проверяется без изменения исходных байтов.
func normalizeVersionBody(body string, exact bool) (string, error) {
	if !exact {
		return normalizeBody(body)
	}
	if strings.TrimSpace(body) == "" {
		return "", ErrEmptyContent
	}
	return body, nil
}
