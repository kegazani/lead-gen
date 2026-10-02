package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы и ссылки домена sales-workflow.
//
// Каждый идентификатор — отдельный именованный тип, чтобы сделку нельзя было
// передать вместо синхронизации, а актора — вместо тенанта. Сделка принадлежит
// внешней CRM (зона red, storage external), поэтому собственные идентификаторы
// контекста — только crm-sync и событие; ссылки на чужие агрегаты хранятся как
// непрозрачные идентификаторы (инвариант «ссылки на чужие агрегаты по
// идентификаторам»).
type (
	// DealID — идентификатор aggregate root deal. Внешняя CRM хранит ту же
	// сделку под ExternalDealRef.
	DealID string

	// CRMSyncID — идентификатор aggregate root crm-sync.
	CRMSyncID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя-оператора, совершившего действие.
	ActorID string

	// CompanyRef — ссылка на карточку компании (владение lead-management,
	// контракт company-profile). Не персональные данные.
	CompanyRef string

	// ExternalDealRef — идентификатор записи сделки во внешней CRM. Служит
	// идемпотентной внешней ссылкой.
	ExternalDealRef string

	// ActionRef — ссылка на исходящее действие (владение outreach-orchestration),
	// результат которого атрибутируется.
	ActionRef string

	// EvidenceRef — проверяемая ссылка-основание подтверждённого результата.
	EvidenceRef string

	// CorrelationID — идентификатор сквозного сценария, к которому относится
	// событие.
	CorrelationID string

	// CausationID — идентификатор команды или события, вызвавшего это событие.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id DealID) String() string          { return string(id) }
func (id CRMSyncID) String() string       { return string(id) }
func (id TenantID) String() string        { return string(id) }
func (id ActorID) String() string         { return string(id) }
func (id ExternalDealRef) String() string { return string(id) }
func (id ActionRef) String() string       { return string(id) }
func (id EvidenceRef) String() string     { return string(id) }
func (id CorrelationID) String() string   { return string(id) }
func (id CausationID) String() string     { return string(id) }
func (id EventID) String() string         { return string(id) }

func (id DealID) validate() error     { return validateID("deal_id", string(id)) }
func (id CRMSyncID) validate() error  { return validateID("crm_sync_id", string(id)) }
func (id TenantID) validate() error   { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error    { return validateID("actor_id", string(id)) }
func (id EventID) validate() error    { return validateID("event_id", string(id)) }
func (id CompanyRef) validate() error { return validateID("company_ref", string(id)) }

// validateID отвергает пустой или состоящий из пробелов идентификатор. Ссылки на
// чужие агрегаты (компания, исходящее действие) проверяются в момент, когда
// соответствующее правило требует их наличия.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
