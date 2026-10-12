package deliveryevent

import (
	"fmt"
	"strings"

	"lead-gen/contracts/message-delivery/envelope"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml).
const ContractName = "delivery-event"

// SchemaVersion — текущая версия схемы. Изменять по правилам semver из envelope.go.
// Версия 1.1.0 добавлена по правилу MINOR: `campaign_id` — необязательное поле,
// старые издатели и потребители продолжают работать без изменений.
const SchemaVersion = "1.1.0"

// Envelope — общий конверт контрактов контекста. Реэкспорт пакета envelope,
// чтобы потребитель видел форму конверта рядом с самим контрактом.
type Envelope = envelope.Envelope

// Outcome — нормализованный исход доставки. Набор закрыт: наблюдаемое значение
// сводится к одному из объявленных, поэтому потребитель не разбирает сырой ответ
// провайдера.
type Outcome string

const (
	// OutcomeDelivered — сообщение принято провайдером.
	OutcomeDelivered Outcome = "delivered"
	// OutcomeFailed — доставка не состоялась.
	OutcomeFailed Outcome = "failed"
)

var allowedOutcomes = map[Outcome]struct{}{
	OutcomeDelivered: {},
	OutcomeFailed:    {},
}

// NormalizeOutcome приводит исход к объявленному набору. Неизвестное значение
// отвергается, а не проходит как есть.
func NormalizeOutcome(outcome Outcome) (Outcome, error) {
	normalized := Outcome(strings.ToLower(strings.TrimSpace(string(outcome))))
	if _, ok := allowedOutcomes[normalized]; !ok {
		return "", fmt.Errorf("deliveryevent: неизвестный исход %q", outcome)
	}
	return normalized, nil
}

// DeliveryEvent — публикуемый контракт исхода доставки сообщения.
//
// Ключ идемпотентности — (tenant_id, message_id, attempt_number): повторная
// доставка события (at-least-once) не создаёт второго эффекта у потребителя;
// event_id конверта служит ключом дедупликации.
type DeliveryEvent struct {
	envelope.Envelope

	// MessageID — идентификатор сообщения-владельца исхода.
	MessageID string `json:"message_id"`
	// ApprovedActionID — утверждённое действие, из которого создано сообщение.
	ApprovedActionID string `json:"approved_action_id"`
	// CampaignID — непрозрачная ссылка на кампанию-владельца, унаследованная от
	// утверждённого действия (его correlation_id равен идентификатору кампании).
	// Необязательное поле версии 1.1.0: позволяет потребителю сопоставить исход
	// доставки со своей кампанией. Персональных данных не несёт.
	CampaignID string `json:"campaign_id,omitempty"`
	// RecipientRef — непрозрачная ссылка на получателя. Не адрес и не
	// персональные данные.
	RecipientRef string `json:"recipient_ref"`
	// SenderDomain — отправляющий домен, к репутации которого относится исход.
	SenderDomain string `json:"sender_domain"`
	// Outcome — нормализованный исход доставки.
	Outcome Outcome `json:"outcome"`
	// AttemptNumber — номер попытки доставки, к которой относится исход.
	AttemptNumber int64 `json:"attempt_number"`
	// ProviderCode — нормализованный код ответа провайдера (необязателен).
	ProviderCode string `json:"provider_code,omitempty"`
}

// NewDeliveryEvent возвращает контракт с проставленными именем и версией схемы.
// Остальные поля заполняет вызывающий.
func NewDeliveryEvent(env Envelope) DeliveryEvent {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return DeliveryEvent{Envelope: env}
}

// Validate проверяет envelope и обязательные поля контракта. Наблюдаемый исход
// сверяется с закрытым набором, а не с произвольной непустой строкой. Ошибка
// называет все отсутствующие или некорректные поля.
func (e DeliveryEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if e.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(e.MessageID) == "" {
		invalid = append(invalid, "message_id")
	}
	if strings.TrimSpace(e.ApprovedActionID) == "" {
		invalid = append(invalid, "approved_action_id")
	}
	if strings.TrimSpace(e.RecipientRef) == "" {
		invalid = append(invalid, "recipient_ref")
	}
	if strings.TrimSpace(e.SenderDomain) == "" {
		invalid = append(invalid, "sender_domain")
	}
	if _, err := NormalizeOutcome(e.Outcome); err != nil {
		invalid = append(invalid, "outcome")
	}
	if e.AttemptNumber <= 0 {
		invalid = append(invalid, "attempt_number")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("deliveryevent: delivery-event: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Ключ внутренний и не
// является частью wire-формата; компоненты кодируются, чтобы разделитель не
// допускал коллизий.
func (e DeliveryEvent) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s|%d",
		envelope.EncodeKeyPart(e.TenantID),
		envelope.EncodeKeyPart(e.MessageID),
		e.AttemptNumber,
	)
}

// OccurredAtUnixNano возвращает момент события в наносекундах — вспомогательное
// представление для потребителей, которым удобен числовой штамп времени.
func (e DeliveryEvent) OccurredAtUnixNano() int64 {
	if e.Envelope.OccurredAt.IsZero() {
		return 0
	}
	return e.Envelope.OccurredAt.UnixNano()
}
