package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// AttributionProof — VO attribution-proof: проверяемое основание связи результата
// с исходящим действием (architecture/domain-catalog.yaml,
// value_objects.attribution-proof).
//
// Доказательство состоит из трёх обязательных частей:
//
//	action_ref   — исходящее действие, чей результат атрибутируется;
//	evidence_ref — проверяемое основание связи (ссылка на подтверждение);
//	external_ref — идемпотентная внешняя ссылка записи CRM.
//
// Неполное доказательство не принимается: инвариант агрегата deal требует, чтобы
// подтверждённый результат имел достаточное доказательство и идемпотентную
// внешнюю ссылку.
type AttributionProof struct {
	actionRef   ActionRef
	evidenceRef EvidenceRef
	externalRef ExternalDealRef
	confirmedAt time.Time
}

// AttributionProofSnapshot — сохранённое состояние VO attribution-proof.
type AttributionProofSnapshot struct {
	ActionRef   ActionRef
	EvidenceRef EvidenceRef
	ExternalRef ExternalDealRef
	ConfirmedAt time.Time
}

// NewAttributionProof собирает доказательство. Все ссылки обязаны быть непустыми,
// а время подтверждения — заданным: неполное доказательство отвергается.
func NewAttributionProof(action ActionRef, evidence EvidenceRef, external ExternalDealRef, at time.Time) (AttributionProof, error) {
	var missing []string
	if strings.TrimSpace(string(action)) == "" {
		missing = append(missing, "action_ref")
	}
	if strings.TrimSpace(string(evidence)) == "" {
		missing = append(missing, "evidence_ref")
	}
	if strings.TrimSpace(string(external)) == "" {
		missing = append(missing, "external_ref")
	}
	if at.IsZero() {
		missing = append(missing, "confirmed_at")
	}
	if len(missing) > 0 {
		return AttributionProof{}, fmt.Errorf("%w: %s", ErrInsufficientProof, strings.Join(missing, ", "))
	}
	return AttributionProof{
		actionRef:   action,
		evidenceRef: evidence,
		externalRef: external,
		confirmedAt: at.UTC(),
	}, nil
}

// ActionRef возвращает ссылку на исходящее действие.
func (p AttributionProof) ActionRef() ActionRef { return p.actionRef }

// EvidenceRef возвращает проверяемое основание связи.
func (p AttributionProof) EvidenceRef() EvidenceRef { return p.evidenceRef }

// ExternalRef возвращает идемпотентную внешнюю ссылку.
func (p AttributionProof) ExternalRef() ExternalDealRef { return p.externalRef }

// ConfirmedAt возвращает время подтверждения результата.
func (p AttributionProof) ConfirmedAt() time.Time { return p.confirmedAt }

// IsZero сообщает, что доказательство не задано.
func (p AttributionProof) IsZero() bool {
	return p.actionRef == "" && p.evidenceRef == "" && p.externalRef == "" && p.confirmedAt.IsZero()
}

// IdempotencyKey возвращает ключ идемпотентности подтверждения. Им служит внешняя
// ссылка: повторное подтверждение той же внешней ссылки не создаёт второго
// результата. Кодирование делает разбор ключа однозначным при любых символах в
// ссылке.
func (p AttributionProof) IdempotencyKey() string {
	return url.QueryEscape(string(p.externalRef))
}

// Snapshot возвращает состояние VO для сохранения.
func (p AttributionProof) Snapshot() AttributionProofSnapshot {
	return AttributionProofSnapshot{
		ActionRef:   p.actionRef,
		EvidenceRef: p.evidenceRef,
		ExternalRef: p.externalRef,
		ConfirmedAt: p.confirmedAt,
	}
}

// rehydrateAttributionProof восстанавливает доказательство из сохранённого
// состояния. Отсутствие доказательства кодируется nil и здесь не обрабатывается.
func rehydrateAttributionProof(s AttributionProofSnapshot) (AttributionProof, error) {
	return NewAttributionProof(s.ActionRef, s.EvidenceRef, s.ExternalRef, s.ConfirmedAt)
}
