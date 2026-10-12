package approvedaction

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Publisher — синхронный контракт approved-action: операция, которой владелец
// контракта (outreach-orchestration) отдаёт утверждённое действие на исполнение.
//
// Контракт выражен интерфейсом, а не конкретным пакетом владельца. Владелец
// реализует Publisher своим прикладным сервисом, а потребитель (например,
// message-delivery) держит только этот интерфейс: он не импортирует внутренние
// пакеты владельца. Чем именно связаны стороны — прямым вызовом в одном
// рантайме (in-process) или сетевым клиентом — потребителя не касается; способ
// связи выбирается топологией (design.md, Решение 2).
//
// Реализация обязана соблюдать семантику контракта: принимать context.Context,
// уважать дедлайн и отмену и соблюдать ключ идемпотентности
// (ApprovedAction.IdempotencyKey) так, чтобы повторная доставка того же действия
// не создавала второго эффекта. Это позволяет заменить in-process адаптер
// сетевым без изменения наблюдаемого поведения.
type Publisher interface {
	// Publish принимает утверждённое действие. Невалидный контракт не
	// доставляется: ошибка видна вызывающему, а не прячется на приёмной стороне.
	Publish(ctx context.Context, action ApprovedAction) error
}

// CampaignCancellation fences deliveries belonging to an outreach campaign.
// The request owner derives this campaign from its durable request-to-campaign
// mapping; it is never inferred by message-delivery from an ID string.
type CampaignCancellation struct {
	TenantID   string
	CampaignID string
}

var ErrInvalidCampaignCancellation = errors.New("approved-action: некорректная отмена кампании")

func (c CampaignCancellation) Validate() error {
	var missing []string
	if strings.TrimSpace(c.TenantID) == "" {
		missing = append(missing, "tenant_id")
	}
	if strings.TrimSpace(c.CampaignID) == "" {
		missing = append(missing, "campaign_id")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidCampaignCancellation, strings.Join(missing, ", "))
	}
	return nil
}

// Canceller is the reverse control operation for the approved-action delivery
// path. Acknowledgement means the delivery owner persisted its fence; it cannot
// retract an SMTP operation that has already started.
type Canceller interface {
	CancelCampaign(ctx context.Context, cancellation CampaignCancellation) error
}
