package domain

import (
	"fmt"
	"strings"
)

// Идентификаторы домена billing.
//
// Каждый идентификатор — отдельный именованный тип, чтобы счёт нельзя было
// передать вместо подписки, а сделку — вместо тенанта. Значения неизменяемы.
type (
	// SubscriptionID — идентификатор aggregate root subscription-invoice.
	SubscriptionID string

	// SuccessFeeID — идентификатор aggregate root success-fee.
	SuccessFeeID string

	// InvoiceID — идентификатор entity invoice (счёт за период).
	InvoiceID string

	// UsageID — идентификатор записи потребления.
	UsageID string

	// TenantID — идентификатор тенанта-владельца данных.
	TenantID string

	// DealID — ссылка на сделку (владение sales-workflow).
	DealID string

	// ResultRef — непрозрачная ссылка на подтверждённый результат
	// (владение sales-workflow, контракт attributed-result).
	ResultRef string

	// CorrelationID — идентификатор сквозного сценария.
	CorrelationID string

	// CausationID — идентификатор команды или события-причины.
	CausationID string

	// EventID — стабильный идентификатор события домена. Служит ключом
	// идемпотентности публикации и дедупликации у потребителя.
	EventID string
)

func (id SubscriptionID) String() string { return string(id) }
func (id SuccessFeeID) String() string   { return string(id) }
func (id InvoiceID) String() string      { return string(id) }
func (id UsageID) String() string        { return string(id) }
func (id TenantID) String() string       { return string(id) }
func (id DealID) String() string         { return string(id) }
func (id ResultRef) String() string      { return string(id) }
func (id CorrelationID) String() string  { return string(id) }
func (id CausationID) String() string    { return string(id) }
func (id EventID) String() string        { return string(id) }

func (id SubscriptionID) validate() error { return validateID("subscription_id", string(id)) }
func (id SuccessFeeID) validate() error   { return validateID("success_fee_id", string(id)) }
func (id InvoiceID) validate() error      { return validateID("invoice_id", string(id)) }
func (id UsageID) validate() error        { return validateID("usage_id", string(id)) }
func (id TenantID) validate() error       { return validateID("tenant_id", string(id)) }
func (id EventID) validate() error        { return validateID("event_id", string(id)) }

// validateResultRef отвергает пустую ссылку на подтверждённый результат:
// вознаграждение без источника не создаётся.
func (id ResultRef) validate() error {
	if strings.TrimSpace(string(id)) == "" {
		return fmt.Errorf("%w: result_ref", ErrResultRefRequired)
	}
	return nil
}

// validateID отвергает пустой или состоящий из пробелов идентификатор. Ссылки на
// чужие агрегаты (сделка) заполняются исполнением и проверяются на месте.
func validateID(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%w: %s", ErrEmptyID, field)
	}
	return nil
}
