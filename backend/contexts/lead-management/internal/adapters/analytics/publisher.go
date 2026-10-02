// Package analytics публикует нормализованные доменные события дашборда клиента
// (контракт analytics-event) в брокер. Адаптер не содержит доменных правил: он
// собирает тело контракта из переданного события и делегирует публикацию
// platform/transport/amqp.Publisher.
//
// Событие уходит в общий topic exchange платформы с ключом маршрутизации
// <contract>.<major>; очередь потребителя analytics.analytics-event
// (<downstream>.<contract>) получает его по привязке <contract>.*.
package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/contracts"
	amqptransport "lead-gen/backend/platform/transport/amqp"
)

// ContractName — имя контракта-события дашборда. Последний сегмент очереди
// потребителя (analytics.analytics-event) и основа ключа маршрутизации.
const ContractName = "analytics-event"

// SchemaVersion — версия схемы контракта (semver). Маршрутизация идёт по MAJOR.
const SchemaVersion = "1.0.0"

// exchangeOwner — контекст-владелец события, передаваемый издателю. Физически
// общий exchange платформы не разводит обмен по владельцу (см.
// amqp.PlatformExchange), но владелец сохраняется в вызове для совместимости
// интерфейса Publisher.
const exchangeOwner = "lead-management"

// Publisher публикует нормализованное событие дашборда поверх брокера.
type Publisher struct {
	publisher amqptransport.Publisher
	tenant    string
}

// NewPublisher собирает издателя. publisher — порт публикации брокера; tenant —
// тенант по умолчанию, подставляемый, если событие его не несёт.
func NewPublisher(publisher amqptransport.Publisher, tenant string) *Publisher {
	return &Publisher{publisher: publisher, tenant: strings.TrimSpace(tenant)}
}

// eventDTO — тело контракта analytics-event. Форма полей совпадает с приёмом
// транспортного адаптера analytics и dev-контуром dashboard (POST
// /dashboard/events), поэтому рабочий и dev-пути питают проекцию одинаково.
type eventDTO struct {
	EventID    string `json:"event_id"`
	TenantID   string `json:"tenant_id"`
	Kind       string `json:"kind"`
	CompanyID  string `json:"company_id"`
	Status     string `json:"status"`
	Outcome    string `json:"outcome"`
	OccurredAt string `json:"occurred_at"`
	ReplyText  string `json:"reply_text"`
}

// Publish собирает тело контракта и публикует событие. Возвращает ошибку, если
// издатель не собран или у события нет идентификатора: дедуплицировать нечем.
// Тенант события приоритетнее тенанта издателя.
func (p *Publisher) Publish(ctx context.Context, event ports.AnalyticsEvent) error {
	if p == nil || p.publisher == nil {
		return errors.New("analytics: издатель не собран")
	}
	eventID := strings.TrimSpace(event.EventID)
	if eventID == "" {
		return errors.New("analytics: событие без event_id")
	}
	tenant := strings.TrimSpace(event.TenantID)
	if tenant == "" {
		tenant = p.tenant
	}
	routingKey, err := contracts.RoutingKey(ContractName, SchemaVersion)
	if err != nil {
		return fmt.Errorf("analytics: ключ маршрутизации: %w", err)
	}
	occurred := event.OccurredAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	body, err := json.Marshal(eventDTO{
		EventID:    eventID,
		TenantID:   tenant,
		Kind:       event.Kind,
		CompanyID:  event.CompanyID,
		Status:     event.Status,
		Outcome:    event.Outcome,
		OccurredAt: occurred.UTC().Format(time.RFC3339),
		ReplyText:  event.ReplyText,
	})
	if err != nil {
		return fmt.Errorf("analytics: сборка тела события %s: %w", eventID, err)
	}
	return p.publisher.Publish(ctx, exchangeOwner, routingKey, body, map[string]string{
		"event_id":  eventID,
		"tenant_id": tenant,
	})
}
