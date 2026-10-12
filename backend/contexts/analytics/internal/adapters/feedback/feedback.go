// Адаптер потребителя контракта recommendation-apply: реализует порт analytics
// ports.QualityFeedback поверх владельца контура поиска (lead-management).
//
// Применение рекомендации как нового запуска поиска — единственное, что делает
// адаптер: переводит структурное изменение внутреннего порта в контракт и обратно
// (идентификатор задания — ссылка на применение), выводит ключ идемпотентности из
// личности рекомендации, чтобы повторное применение не создавало второе задание.
// Недоступность владельца не подменяется успехом: ошибка контракта возвращается
// вызывающему.
package feedback

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/contracts/lead-management/recommendationapply"
)

// Feedback — адаптер ports.QualityFeedback поверх контракта recommendation-apply.
type Feedback struct {
	port recommendationapply.Port
}

// Проверка соответствия порту на этапе компиляции.
var _ ports.QualityFeedback = (*Feedback)(nil)

// New собирает адаптер поверх контракта владельца.
func New(port recommendationapply.Port) *Feedback { return &Feedback{port: port} }

// ApplyRecommendation применяет рекомендацию как изменение в контуре поиска и
// возвращает ссылку на созданное (или ранее созданное) задание.
func (f *Feedback) ApplyRecommendation(ctx context.Context, change ports.RecommendationChange) (ports.RecommendationReceipt, error) {
	if f == nil || f.port == nil {
		return ports.RecommendationReceipt{}, recommendationapply.ErrUnavailable
	}
	receipt, err := f.port.ApplyRecommendation(ctx, recommendationapply.Application{
		TenantID:  change.TenantID,
		ChangeID:  changeID(change),
		ScopeKind: change.ScopeKind,
		ScopeKey:  change.ScopeKey,
		Reason:    change.Reason,
		Basis:     change.Basis,
		Change: recommendationapply.Change{
			Kind:     change.Change.Kind,
			Criteria: change.Change.Criteria,
			Targets:  change.Change.Targets,
			Limit:    change.Change.Limit,
		},
	})
	if err != nil {
		return ports.RecommendationReceipt{}, err
	}
	return ports.RecommendationReceipt{Reference: receipt.JobID}, nil
}

// changeID выводит детерминированный ключ идемпотентности из личности
// рекомендации и структурного изменения: повторное применение той же рекомендации
// (тенант, срез, причина, изменение) даёт тот же ключ, поэтому владелец не создаёт
// второе задание.
func changeID(change ports.RecommendationChange) string {
	parts := []string{
		change.TenantID,
		change.ScopeKind,
		change.ScopeKey,
		change.Reason,
		change.Change.Kind,
		change.Change.Criteria,
		strings.Join(change.Change.Targets, "\x1f"),
		strconv.Itoa(change.Change.Limit),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x1e")))
	return hex.EncodeToString(sum[:])
}
