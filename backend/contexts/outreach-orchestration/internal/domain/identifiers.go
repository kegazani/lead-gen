package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена outreach-orchestration.
//
// Каждый идентификатор — отдельный именованный тип, чтобы черновик нельзя было
// передать вместо кампании, а актора — вместо тенанта. Значения неизменяемы.
type (
	// CampaignID — идентификатор aggregate root campaign.
	CampaignID string

	// DraftID — идентификатор сущности action-draft.
	DraftID string

	// DecisionID — идентификатор сущности operator-decision.
	DecisionID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ActorID — идентификатор пользователя-оператора, принявшего решение.
	ActorID string

	// MaterialID — ссылка на версию материала (владение content-production).
	MaterialID string

	// SegmentID — ссылка на снимок аудитории (владение audience).
	SegmentID string

	// RecipientID — ссылка на адресата действия.
	RecipientID string

	// CommandID — идентификатор входящей команды контракта campaign-command.
	CommandID string
)

func (id CampaignID) String() string  { return string(id) }
func (id DraftID) String() string     { return string(id) }
func (id DecisionID) String() string  { return string(id) }
func (id TenantID) String() string    { return string(id) }
func (id ActorID) String() string     { return string(id) }
func (id MaterialID) String() string  { return string(id) }
func (id SegmentID) String() string   { return string(id) }
func (id RecipientID) String() string { return string(id) }
func (id CommandID) String() string   { return string(id) }

func (id CampaignID) validate() error  { return validateID("campaign_id", string(id)) }
func (id DraftID) validate() error     { return validateID("draft_id", string(id)) }
func (id DecisionID) validate() error  { return validateID("decision_id", string(id)) }
func (id TenantID) validate() error    { return validateID("tenant_id", string(id)) }
func (id ActorID) validate() error     { return validateID("actor_id", string(id)) }
func (id MaterialID) validate() error  { return validateID("material_id", string(id)) }
func (id SegmentID) validate() error   { return validateID("segment_id", string(id)) }
func (id RecipientID) validate() error { return validateID("recipient_id", string(id)) }
func (id CommandID) validate() error   { return validateID("command_id", string(id)) }

func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
