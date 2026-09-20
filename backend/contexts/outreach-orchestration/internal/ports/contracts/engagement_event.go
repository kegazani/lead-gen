package contracts

import (
	"fmt"
	"strings"
)

// ContractEngagementEvent — имя потребляемого контракта-события: реакция
// получателя (открытие, переход, ответ и т. п.), владельцем которой является
// контекст engagement-observation (каталог связей, связь observation-to-outreach,
// интеграция event, транспорт async/amqp).
const ContractEngagementEvent = "engagement-event"

// EngagementEventSchemaVersion — текущая версия схемы. Изменять по правилам
// semver из envelope.go.
const EngagementEventSchemaVersion = "1.0.0"

// Типы реакции контракта engagement-event. Набор закрыт: неизвестный тип
// отвергается контрактом, чтобы потребитель не трактовал молча то, чего не
// знает.
const (
	// EngagementEventOpened — получатель открыл сообщение.
	EngagementEventOpened = "opened"
	// EngagementEventClicked — получатель перешёл по ссылке.
	EngagementEventClicked = "clicked"
	// EngagementEventReplied — получатель ответил.
	EngagementEventReplied = "replied"
	// EngagementEventBounced — сообщение не доставлено.
	EngagementEventBounced = "bounced"
	// EngagementEventUnsubscribed — получатель отписался.
	EngagementEventUnsubscribed = "unsubscribed"
)

// EngagementEvent — антикоррупционный DTO ПОТРЕБИТЕЛЯ для контракта
// engagement-event.
//
// Владелец контракта — контекст engagement-observation; его Go-типы сюда не
// импортируются. Потребителю нужна только ссылка на факт и координаты действия,
// к которому он относится: идентификаторы кампании, черновика и получателя плюс
// вид реакции. Полезная нагрузка провайдера (адрес, заголовки письма, тело
// ответа) через границу не проходит.
type EngagementEvent struct {
	Envelope

	// EventType — вид реакции (см. EngagementEvent*).
	EventType string `json:"event_type"`
	// CampaignID — ссылка на кампанию, к которой относится реакция.
	CampaignID string `json:"campaign_id"`
	// DraftID — ссылка на черновик, породивший сообщение.
	DraftID string `json:"draft_id"`
	// RecipientRef — непрозрачная ссылка на получателя; не адрес и не
	// персональные данные.
	RecipientRef string `json:"recipient_ref"`
}

// Validate проверяет envelope и обязательные поля контракта, собирая все
// отсутствующие или некорректные поля, а не первое из них.
func (e EngagementEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if e.Name != ContractEngagementEvent {
		invalid = append(invalid, "name")
	}
	if !isKnownEngagementEventType(e.EventType) {
		invalid = append(invalid, "event_type")
	}
	if strings.TrimSpace(e.CampaignID) == "" {
		invalid = append(invalid, "campaign_id")
	}
	if strings.TrimSpace(e.DraftID) == "" {
		invalid = append(invalid, "draft_id")
	}
	if strings.TrimSpace(e.RecipientRef) == "" {
		invalid = append(invalid, "recipient_ref")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("contracts: engagement-event: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// isKnownEngagementEventType сообщает, принадлежит ли тип закрытому набору
// реакции контракта.
func isKnownEngagementEventType(eventType string) bool {
	switch eventType {
	case EngagementEventOpened, EngagementEventClicked, EngagementEventReplied,
		EngagementEventBounced, EngagementEventUnsubscribed:
		return true
	default:
		return false
	}
}
