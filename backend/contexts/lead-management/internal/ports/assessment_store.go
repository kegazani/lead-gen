package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// ErrAssessmentNotFound — оценка компании в задании не найдена.
var ErrAssessmentNotFound = errors.New("ports: оценка не найдена")

// VerdictCounts — распределение оценок задания по вердиктам.
type VerdictCounts struct {
	// Fits — число оценок «Подходит».
	Fits int
	// Hypothesis — число оценок «Гипотеза».
	Hypothesis int
	// Unsuitable — число оценок «Не подходит».
	Unsuitable int
}

// LeadAssessmentStore — хранилище оценок компании относительно конкретного
// задания поиска. Ключ записи — пара (jobID, companyID): одна компания может
// иметь независимые оценки в разных заданиях.
type LeadAssessmentStore interface {
	// Save сохраняет оценку. Повторный вызов для той же пары (jobID, companyID)
	// перезаписывает прежнюю оценку (upsert по паре).
	Save(ctx context.Context, assessment *domain.LeadAssessment) error
	// GetByJobAndCompany возвращает оценку компании в задании. Отсутствие даёт
	// ports.ErrAssessmentNotFound.
	GetByJobAndCompany(ctx context.Context, jobID, companyID string) (*domain.LeadAssessment, error)
	// ListByJob возвращает все оценки задания в стабильном порядке (по companyID).
	// Задание без оценок даёт пустой список.
	ListByJob(ctx context.Context, jobID string) ([]*domain.LeadAssessment, error)
	// LatestByCompany возвращает последнюю по времени оценку каждой из переданных
	// компаний по всем заданиям: для компании выбирается оценка с максимальным
	// UpdatedAt, при равенстве — с максимальным CreatedAt, затем с наибольшим
	// JobID (детерминированный выбор). Это проекция чтения для витрины, а не
	// перенос оценки между заданиями: сама оценка остаётся привязанной к паре
	// (job, company). Компания без оценок в результат не попадает.
	LatestByCompany(ctx context.Context, companyIDs []string) (map[string]*domain.LeadAssessment, error)
	// VerdictDistribution считает распределение вердиктов по оценкам задания.
	// Задание без оценок даёт нулевые счётчики.
	VerdictDistribution(ctx context.Context, jobID string) (VerdictCounts, error)
}
