// Публичные фабрики контрактов обратной связи и чтения оценки, публикуемые
// composition root'ом: recommendation-apply (применение рекомендации анализа как
// нового запуска поиска) и lead-relevance (чтение вердикта и свидетельств оценки).
//
// Как и у прочих контрактов lead-management, cmd/lead-gen не импортирует
// internal-пакеты напрямую, а получает адаптеры через эти конструкторы. При
// co-location адаптер вызывается в процессе; при разделении сервисов даёт ту же
// форму сетевой клиент (recommendationapply/httpport, leadrelevance/httpport).
package leadmanagement

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/lead-management/internal/app"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/contracts/lead-management/leadrelevance"
	"lead-gen/contracts/lead-management/recommendationapply"
)

// RecommendationApplyDeps — зависимости фабрики in-proc адаптера контракта
// recommendation-apply. Репозиторий заданий обязателен.
type RecommendationApplyDeps struct {
	// Repositories — адаптеры хранилища (журнал заданий поиска).
	Repositories Repositories
	// Clock — часы; nil — системное время.
	Clock ports.Clock
}

// NewRecommendationApply собирает опубликованный контракт recommendation-apply
// поверх сценария применения рекомендации: структурное изменение ставится новым
// заданием поиска (идемпотентно по ключу), возвращается ссылка на задание.
//
// Это публичная фабрика владельца контура поиска: точка сборки не имеет доступа к
// внутренним пакетам контекста, поэтому прямой адаптер контракта объявлен здесь.
func NewRecommendationApply(deps RecommendationApplyDeps) recommendationapply.Port {
	if deps.Repositories.Jobs == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// применение.
		return recommendationapply.InProc(recommendationapply.InProcHandler{})
	}
	useCase := &app.ApplyRecommendation{Jobs: deps.Repositories.Jobs, Clock: deps.Clock}
	return recommendationapply.InProc(recommendationapply.InProcHandler{
		ApplyFunc: func(ctx context.Context, application recommendationapply.Application) (recommendationapply.Receipt, error) {
			if err := application.Validate(); err != nil {
				return recommendationapply.Receipt{}, fmt.Errorf("%w: %v", recommendationapply.ErrRejected, err)
			}
			result, err := useCase.Run(ctx, app.ApplyRecommendationInput{
				TenantID:  application.TenantID,
				ChangeID:  application.ChangeID,
				ScopeKind: application.ScopeKind,
				ScopeKey:  application.ScopeKey,
				Reason:    application.Reason,
				Basis:     application.Basis,
				Criteria:  application.Change.Criteria,
				Targets:   application.Change.Targets,
				Limit:     application.Change.Limit,
			})
			if err != nil {
				return recommendationapply.Receipt{}, recommendationApplyError(err)
			}
			return recommendationapply.Receipt{JobID: result.JobID}, nil
		},
	})
}

// recommendationApplyError переводит ошибку сценария применения в исход
// опубликованного контракта: некорректное изменение — ErrRejected, прочее —
// ErrUnavailable. Ошибка не выдаётся за применённую рекомендацию.
func recommendationApplyError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidRecommendationChange):
		return fmt.Errorf("%w: %v", recommendationapply.ErrRejected, err)
	default:
		return fmt.Errorf("%w: %v", recommendationapply.ErrUnavailable, err)
	}
}

// LeadRelevanceDeps — зависимости фабрики in-proc адаптера контракта
// lead-relevance. Хранилище оценок обязательно.
type LeadRelevanceDeps struct {
	// Repositories — адаптеры хранилища (оценки компаний).
	Repositories Repositories
}

// NewLeadRelevance собирает опубликованный read-контракт lead-relevance поверх
// сценария чтения оценки: по паре (задание, компания) отдаются вердикт, тип
// сигнала и свидетельства. Новых агрегатов не заводит — только чтение.
func NewLeadRelevance(deps LeadRelevanceDeps) leadrelevance.Port {
	if deps.Repositories.Assessments == nil {
		// Владелец не собран: честно сообщаем о недоступности, а не выдумываем
		// оценку.
		return leadrelevance.InProc(leadrelevance.InProcHandler{})
	}
	useCase := &app.ReadLeadRelevance{Assessments: deps.Repositories.Assessments}
	return leadrelevance.InProc(leadrelevance.InProcHandler{
		ReadFunc: func(ctx context.Context, request leadrelevance.Request) (leadrelevance.Assessment, error) {
			result, err := useCase.Run(ctx, app.ReadLeadRelevanceInput{JobID: request.JobID, CompanyID: request.CompanyID})
			if err != nil {
				return leadrelevance.Assessment{}, leadRelevanceError(err)
			}
			evidence := make([]leadrelevance.Evidence, 0, len(result.Evidence))
			for _, e := range result.Evidence {
				evidence = append(evidence, leadrelevance.Evidence{
					Criterion:  e.Criterion,
					Fact:       e.Fact,
					SourceURL:  e.SourceURL,
					Reasoning:  e.Reasoning,
					SignalKind: e.SignalKind,
					Missing:    e.Missing,
					Mismatch:   e.Mismatch,
				})
			}
			return leadrelevance.Assessment{
				Contract:      leadrelevance.ContractName,
				SchemaVersion: leadrelevance.SchemaVersion,
				JobID:         result.JobID,
				CompanyID:     result.CompanyID,
				Verdict:       result.Verdict,
				SignalKind:    result.SignalKind,
				Evidence:      evidence,
				AssessedAt:    result.AssessedAt,
			}, nil
		},
	})
}

// leadRelevanceError переводит ошибку сценария чтения в исход опубликованного
// контракта: некорректный запрос — ErrRejected, отсутствие оценки — ErrNotFound,
// прочее — ErrUnavailable. Оценка не выдаётся за выдуманную.
func leadRelevanceError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, app.ErrInvalidLeadRelevanceRequest):
		return fmt.Errorf("%w: %v", leadrelevance.ErrRejected, err)
	case errors.Is(err, ports.ErrAssessmentNotFound):
		return fmt.Errorf("%w: %v", leadrelevance.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", leadrelevance.ErrUnavailable, err)
	}
}
