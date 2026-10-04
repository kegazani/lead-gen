package postgres

import (
	"encoding/json"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Кодирование domain events для таблицы outbox.
//
// Домен хранит value objects с закрытыми полями (например, ActionChannel: имя
// канала не экспортировано), поэтому событие нельзя сериализовать encoding/json
// напрямую — поля value object потерялись бы молча. Здесь событие раскладывается
// в явную проводимую форму (wire struct) со строковыми значениями, а при загрузке
// собирается заново конструкторами домена (ParseActionChannel), то есть проверки
// домена выполняются и на обратном пути. Имя события — дискриминатор формы.
//
// Известный набор событий закрыт типом: незнакомое событие отвергается ошибкой,
// а не записывается усечённым. Так outbox не теряет событие тихо.

// approvedEvent — проводимая форма domain.ActionApproved.
type approvedEvent struct {
	CampaignID   domain.CampaignID `json:"campaign_id"`
	TenantID     domain.TenantID   `json:"tenant_id"`
	DraftID      domain.DraftID    `json:"draft_id"`
	DraftVersion int               `json:"draft_version"`
	DecisionID   domain.DecisionID `json:"decision_id"`
	Actor        domain.ActorID    `json:"actor"`
	Channel      string            `json:"channel"`
	At           time.Time         `json:"at"`
}

// stateChangedEvent — проводимая форма domain.CampaignStateChanged.
type stateChangedEvent struct {
	CampaignID domain.CampaignID    `json:"campaign_id"`
	TenantID   domain.TenantID      `json:"tenant_id"`
	From       domain.CampaignState `json:"from"`
	To         domain.CampaignState `json:"to"`
	At         time.Time            `json:"at"`
}

// outcomeEvent — проводимая форма domain.OutreachOutcomeRecorded.
type outcomeEvent struct {
	CampaignID  domain.CampaignID  `json:"campaign_id"`
	TenantID    domain.TenantID    `json:"tenant_id"`
	ExecutionID domain.ExecutionID `json:"execution_id"`
	SequenceID  domain.SequenceID  `json:"sequence_id"`
	Lead        domain.LeadID      `json:"lead"`
	Step        int                `json:"step"`
	Result      domain.StepOutcome `json:"result"`
	At          time.Time          `json:"at"`
}

// encodeEvent возвращает имя события и его проводимую JSON-форму. Value objects
// переводятся в строки через доменные строковые представления. Незнакомый тип
// события — ошибка: усечённая запись исказила бы outbox.
func encodeEvent(event domain.DomainEvent) (string, []byte, error) {
	if event == nil {
		return "", nil, fmt.Errorf("postgres: кодирование nil-события")
	}

	switch e := event.(type) {
	case domain.ActionApproved:
		channel := e.Channel.String()
		if channel == "" {
			return "", nil, fmt.Errorf("postgres: событие %s без канала действия", e.Name())
		}
		payload, err := json.Marshal(approvedEvent{
			CampaignID:   e.CampaignID,
			TenantID:     e.TenantID,
			DraftID:      e.DraftID,
			DraftVersion: e.DraftVersion,
			DecisionID:   e.DecisionID,
			Actor:        e.Actor,
			Channel:      channel,
			At:           e.At,
		})
		if err != nil {
			return "", nil, fmt.Errorf("postgres: сериализация события %s: %w", e.Name(), err)
		}
		return string(e.Name()), payload, nil

	case domain.CampaignStateChanged:
		payload, err := json.Marshal(stateChangedEvent{
			CampaignID: e.CampaignID,
			TenantID:   e.TenantID,
			From:       e.From,
			To:         e.To,
			At:         e.At,
		})
		if err != nil {
			return "", nil, fmt.Errorf("postgres: сериализация события %s: %w", e.Name(), err)
		}
		return string(e.Name()), payload, nil

	case domain.OutreachOutcomeRecorded:
		payload, err := json.Marshal(outcomeEvent{
			CampaignID:  e.CampaignID,
			TenantID:    e.TenantID,
			ExecutionID: e.ExecutionID,
			SequenceID:  e.SequenceID,
			Lead:        e.Lead,
			Step:        e.Step,
			Result:      e.Result,
			At:          e.At,
		})
		if err != nil {
			return "", nil, fmt.Errorf("postgres: сериализация события %s: %w", e.Name(), err)
		}
		return string(e.Name()), payload, nil

	default:
		return "", nil, fmt.Errorf("postgres: неизвестный тип domain event %T", event)
	}
}

// decodeEvent восстанавливает domain event по имени и проводимой форме. Канал
// действия проверяется доменным конструктором: невалидная запись отвергается, а
// не превращается в событие с пустым каналом.
func decodeEvent(name string, payload []byte) (domain.DomainEvent, error) {
	switch domain.EventName(name) {
	case domain.EventActionApproved:
		var wire approvedEvent
		if err := json.Unmarshal(payload, &wire); err != nil {
			return nil, fmt.Errorf("postgres: разбор события %s: %w", name, err)
		}
		channel, err := domain.ParseActionChannel(wire.Channel)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор канала события %s: %w", name, err)
		}
		return domain.ActionApproved{
			CampaignID:   wire.CampaignID,
			TenantID:     wire.TenantID,
			DraftID:      wire.DraftID,
			DraftVersion: wire.DraftVersion,
			DecisionID:   wire.DecisionID,
			Actor:        wire.Actor,
			Channel:      channel,
			At:           wire.At,
		}, nil

	case domain.EventCampaignStateChanged:
		var wire stateChangedEvent
		if err := json.Unmarshal(payload, &wire); err != nil {
			return nil, fmt.Errorf("postgres: разбор события %s: %w", name, err)
		}
		return domain.CampaignStateChanged{
			CampaignID: wire.CampaignID,
			TenantID:   wire.TenantID,
			From:       wire.From,
			To:         wire.To,
			At:         wire.At,
		}, nil

	case domain.EventOutreachOutcomeRecorded:
		var wire outcomeEvent
		if err := json.Unmarshal(payload, &wire); err != nil {
			return nil, fmt.Errorf("postgres: разбор события %s: %w", name, err)
		}
		return domain.OutreachOutcomeRecorded{
			CampaignID:  wire.CampaignID,
			TenantID:    wire.TenantID,
			ExecutionID: wire.ExecutionID,
			SequenceID:  wire.SequenceID,
			Lead:        wire.Lead,
			Step:        wire.Step,
			Result:      wire.Result,
			At:          wire.At,
		}, nil

	default:
		return nil, fmt.Errorf("postgres: неизвестное имя события outbox %q", name)
	}
}
