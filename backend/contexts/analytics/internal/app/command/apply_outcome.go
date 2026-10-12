package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// ErrInsightUnavailable — проекция качества поиска не собрана: честная
// недоступность, а не пустой исход.
var ErrInsightUnavailable = errors.New("command: проекция качества поиска недоступна")

// ErrFeedbackUnavailable — контур применения рекомендации не подключён: факт
// применения не выдумывается.
var ErrFeedbackUnavailable = errors.New("command: контур применения рекомендации недоступен")

// ApplyOutcome — команда применения исхода классификации к проекции качества
// поиска. Поля вердикта и причины необязательны: причина выводится из вердикта,
// если не задана явно.
type ApplyOutcome struct {
	TenantID  string
	JobID     string
	CompanyID string
	Source    string
	Industry  string
	Verdict   string
	Reason    string

	OccurredAt time.Time
}

// ApplyOutcomeHandler — use case применения исхода к проекции качества поиска.
type ApplyOutcomeHandler struct {
	model *readmodel.InsightModel
	store ports.InsightStore
}

// NewApplyOutcomeHandler собирает use case поверх проекции. store необязателен:
// без хранилища проекция остаётся в памяти процесса; с хранилищем снимок тенанта
// записывается на носитель и переживает рестарт.
func NewApplyOutcomeHandler(model *readmodel.InsightModel, store ports.InsightStore) *ApplyOutcomeHandler {
	return &ApplyOutcomeHandler{model: model, store: store}
}

// Handle применяет исход и, если задано хранилище, сохраняет снимок тенанта.
// Исход без тенанта или компании проекция игнорирует сама: прикладной слой не
// выдумывает принадлежность и ничего не сохраняет по такому исходу.
func (h *ApplyOutcomeHandler) Handle(ctx context.Context, cmd ApplyOutcome) error {
	if h == nil || h.model == nil {
		return ErrInsightUnavailable
	}
	outcome := readmodel.InsightOutcome{
		TenantID:   cmd.TenantID,
		JobID:      cmd.JobID,
		CompanyID:  cmd.CompanyID,
		Source:     cmd.Source,
		Industry:   cmd.Industry,
		Verdict:    readmodel.Verdict(cmd.Verdict),
		Reason:     readmodel.ReasonCategory(cmd.Reason),
		OccurredAt: cmd.OccurredAt,
	}
	h.model.Apply(outcome)

	if h.store == nil {
		return nil
	}
	if tenant := outcome.TenantID; tenant != "" && outcome.CompanyID != "" {
		if err := h.store.SaveSnapshot(ctx, h.model.Snapshot(tenant)); err != nil {
			return fmt.Errorf("command: сохранение снимка инсайтов: %w", err)
		}
	}
	return nil
}

// ApplyRecommendationHandler — use case применения рекомендации: создаёт
// изменение в контуре поиска, фиксирует факт применения и снимок «до», по
// которому позже считается сравнение «до/после».
type ApplyRecommendationHandler struct {
	model    *readmodel.InsightModel
	feedback ports.QualityFeedback
	store    ports.InsightStore
	clock    ports.Clock
}

// NewApplyRecommendationHandler собирает use case применения рекомендации.
// feedback и clock обязательны на продуктовом пути; отсутствие feedback — честная
// недоступность применения.
func NewApplyRecommendationHandler(model *readmodel.InsightModel, feedback ports.QualityFeedback, store ports.InsightStore, clock ports.Clock) *ApplyRecommendationHandler {
	return &ApplyRecommendationHandler{model: model, feedback: feedback, store: store, clock: clock}
}

// ApplyRecommendation — команда применения рекомендации оператором.
type ApplyRecommendation struct {
	TenantID  string
	ScopeKind string
	ScopeKey  string
	Reason    string
	Basis     string
	// Change — структурное изменение контура поиска (не проза).
	Change ports.SearchChange
}

// Handle применяет рекомендацию: вызывает порт контура поиска, фиксирует факт
// применения со снимком «до». Ошибка означает, что изменение не создано и факт
// применения не зафиксирован.
func (h *ApplyRecommendationHandler) Handle(ctx context.Context, cmd ApplyRecommendation) (readmodel.RecommendationApplication, error) {
	if h == nil || h.model == nil {
		return readmodel.RecommendationApplication{}, ErrInsightUnavailable
	}
	if h.feedback == nil {
		return readmodel.RecommendationApplication{}, ErrFeedbackUnavailable
	}

	tenant := cmd.TenantID
	before := h.model.Insight(tenant, filterForScope(cmd.ScopeKind, cmd.ScopeKey))
	receipt, err := h.feedback.ApplyRecommendation(ctx, ports.RecommendationChange{
		TenantID:  tenant,
		ScopeKind: cmd.ScopeKind,
		ScopeKey:  cmd.ScopeKey,
		Reason:    cmd.Reason,
		Basis:     cmd.Basis,
		Change:    cmd.Change,
	})
	if err != nil {
		return readmodel.RecommendationApplication{}, fmt.Errorf("command: применение рекомендации в контуре поиска: %w", err)
	}

	application := readmodel.RecommendationApplication{
		TenantID:        tenant,
		ScopeKind:       cmd.ScopeKind,
		ScopeKey:        cmd.ScopeKey,
		Action:          changeLabel(cmd.Change),
		Reason:          readmodel.ReasonCategory(cmd.Reason),
		Basis:           cmd.Basis,
		AppliedAt:       h.now(),
		Reference:       receipt.Reference,
		BeforeWatermark: before.Watermark,
		Before:          readmodel.Summarize(before),
	}
	if h.store != nil {
		if err := h.store.SaveApplication(ctx, application); err != nil {
			return readmodel.RecommendationApplication{}, fmt.Errorf("command: фиксация применения рекомендации: %w", err)
		}
	}
	return application, nil
}

func (h *ApplyRecommendationHandler) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}

// changeLabel даёт подпись структурного изменения для зафиксированного факта
// применения: запись читается человеком, но применяется структурное изменение.
func changeLabel(change ports.SearchChange) string {
	switch change.Kind {
	case "new-search":
		if criteria := strings.TrimSpace(change.Criteria); criteria != "" {
			return "новый запуск поиска: " + criteria
		}
		return "новый запуск поиска по целям обхода"
	default:
		return "изменение контура поиска"
	}
}

// filterForScope переводит измерение рекомендации в фильтр среза: снимок «до»
// считается по тому же срезу, к которому относится рекомендация.
func filterForScope(scopeKind, scopeKey string) readmodel.InsightFilter {
	switch scopeKind {
	case "job":
		return readmodel.InsightFilter{JobID: scopeKey}
	case "source":
		return readmodel.InsightFilter{Source: scopeKey}
	case "industry":
		return readmodel.InsightFilter{Industry: scopeKey}
	default:
		return readmodel.InsightFilter{}
	}
}
