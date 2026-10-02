package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы и ссылки домена message-delivery.
//
// Каждый идентификатор — отдельный именованный тип, чтобы сообщение нельзя было
// передать вместо тенанта, а получателя — вместо утверждённого действия.
// Значения неизменяемы.
type (
	// MessageID — идентификатор aggregate root message.
	MessageID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// ApprovedActionID — ссылка на утверждённое действие (владение
	// outreach-orchestration), санкционировавшее отправку.
	ApprovedActionID string

	// RecipientRef — непрозрачная ссылка на получателя. Не адрес и не
	// персональные данные.
	RecipientRef string

	// CommandID — идентификатор входящей команды контракта delivery-command.
	CommandID string

	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string
)

func (id MessageID) String() string        { return string(id) }
func (id TenantID) String() string         { return string(id) }
func (id ApprovedActionID) String() string { return string(id) }
func (id RecipientRef) String() string     { return string(id) }
func (id CommandID) String() string        { return string(id) }
func (id CorrelationID) String() string    { return string(id) }

func (id MessageID) validate() error        { return validateID("message_id", string(id)) }
func (id TenantID) validate() error         { return validateID("tenant_id", string(id)) }
func (id ApprovedActionID) validate() error { return validateID("approved_action_id", string(id)) }
func (id RecipientRef) validate() error     { return validateID("recipient_ref", string(id)) }
func (id CommandID) validate() error        { return validateID("command_id", string(id)) }

func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
