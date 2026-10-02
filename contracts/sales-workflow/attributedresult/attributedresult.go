// Package attributedresult объявляет опубликованный контракт attributed-result
// контекста sales-workflow: подтверждённая атрибуция результата сделки.
//
// Контракт публикуется транспортом async/amqp в биллинг (sales-to-billing,
// architecture/domain-catalog.yaml) после commit изменения сделки через
// transactional outbox. Зависимости — стандартная библиотека и общий пакет
// envelope.
//
// Ключ идемпотентности контракта — (tenant_id, external_ref): внешняя ссылка
// сделки идемпотентна, поэтому повторная публикация того же результата не создаёт
// второго эффекта.
package attributedresult

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lead-gen/contracts/sales-workflow/envelope"
)

// ContractName — имя публикуемого контракта.
const ContractName = "attributed-result"

// SchemaVersion — текущая версия схемы. Изменяется по правилам semver из пакета
// envelope.
const SchemaVersion = "1.0.0"

// AttributedResult — подтверждённый результат сделки, атрибутированный исходящему
// действию.
type AttributedResult struct {
	envelope.Envelope

	// DealID — идентификатор сделки.
	DealID string `json:"deal_id"`
	// ActionRef — исходящее действие, чей результат подтверждён.
	ActionRef string `json:"action_ref"`
	// EvidenceRef — проверяемое основание связи результата с действием.
	EvidenceRef string `json:"evidence_ref"`
	// ExternalRef — идемпотентная внешняя ссылка записи CRM.
	ExternalRef string `json:"external_ref"`
	// ConfirmedAt — момент подтверждения результата.
	ConfirmedAt time.Time `json:"confirmed_at"`
}

// NewAttributedResult возвращает контракт с проставленными именем и версией
// схемы. Остальные поля заполняет вызывающий.
func NewAttributedResult(env envelope.Envelope) AttributedResult {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return AttributedResult{Envelope: env}
}

// Validate проверяет конверт и обязательные поля контракта.
func (a AttributedResult) Validate() error {
	if err := a.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if strings.TrimSpace(a.DealID) == "" {
		invalid = append(invalid, "deal_id")
	}
	if strings.TrimSpace(a.ActionRef) == "" {
		invalid = append(invalid, "action_ref")
	}
	if strings.TrimSpace(a.EvidenceRef) == "" {
		invalid = append(invalid, "evidence_ref")
	}
	if strings.TrimSpace(a.ExternalRef) == "" {
		invalid = append(invalid, "external_ref")
	}
	if a.ConfirmedAt.IsZero() {
		invalid = append(invalid, "confirmed_at")
	}
	if a.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("attributedresult: attributed-result: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Ключ внутренний и не
// является частью wire-формата.
func (a AttributedResult) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s", envelope.EncodeKeyPart(a.TenantID), envelope.EncodeKeyPart(a.ExternalRef))
}

// Publisher — синхронная точка публикации контракта для co-location и тестов.
// При разделении сервисов публикацию выполняет транспорт async/amqp; форма вызова
// сохраняется.
type Publisher interface {
	// Publish принимает подтверждённый результат. Невалидный контракт не
	// доставляется: ошибка видна вызывающему.
	Publish(ctx context.Context, result AttributedResult) error
}
