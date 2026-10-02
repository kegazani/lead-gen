package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена content-production.
//
// Каждый идентификатор — отдельный именованный тип, чтобы контент-пакет нельзя
// было передать вместо версии, а шаблон — вместо тенанта. Значения неизменяемы.
//
// Ссылки на чужие агрегаты и объекты (роль, форма заявки и т.п.) домен хранит по
// непрозрачным ссылкам ArtifactRef: их смысл владелец не выводит.
type (
	// ContentAssetID — идентификатор aggregate root content-package
	// (сущность content-asset).
	ContentAssetID string

	// ContentVersionID — идентификатор сущности content-version
	// (неизменяемой версии контента).
	ContentVersionID string

	// LandingContentID — идентификатор сущности landing-content
	// (наполнения сгенерированного лендинга).
	LandingContentID string

	// DocumentID — идентификатор сущности document (сгенерированного документа).
	DocumentID string

	// RenderJobID — идентификатор aggregate root render-job.
	RenderJobID string

	// TemplateID — идентификатор шаблона, по которому выполняется рендер.
	TemplateID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя или процесса, инициировавшего действие.
	ActorID string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор доменного события. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string

	// ArtifactRef — непрозрачная ссылка на воспроизведённый артефакт (лендинг или
	// презентацию). Домен не выводит из неё смысл.
	ArtifactRef string
)

func (id ContentAssetID) String() string   { return string(id) }
func (id ContentVersionID) String() string { return string(id) }
func (id LandingContentID) String() string { return string(id) }
func (id DocumentID) String() string       { return string(id) }
func (id RenderJobID) String() string      { return string(id) }
func (id TemplateID) String() string       { return string(id) }
func (id TenantID) String() string         { return string(id) }
func (id ActorID) String() string          { return string(id) }
func (id CorrelationID) String() string    { return string(id) }
func (id CausationID) String() string      { return string(id) }
func (id EventID) String() string          { return string(id) }
func (ref ArtifactRef) String() string     { return string(ref) }

func (id ContentAssetID) validate() error   { return validateID("content_asset_id", string(id)) }
func (id ContentVersionID) validate() error { return validateID("content_version_id", string(id)) }
func (id LandingContentID) validate() error { return validateID("landing_content_id", string(id)) }
func (id DocumentID) validate() error       { return validateID("document_id", string(id)) }
func (id RenderJobID) validate() error      { return validateID("render_job_id", string(id)) }
func (id TenantID) validate() error         { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error          { return validateID("actor_id", string(id)) }
func (id EventID) validate() error          { return validateID("event_id", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
