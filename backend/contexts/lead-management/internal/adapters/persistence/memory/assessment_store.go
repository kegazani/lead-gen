package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// AssessmentStore — in-memory реализация ports.LeadAssessmentStore. Оценки
// хранятся как снимки и восстанавливаются через domain.RehydrateLeadAssessment
// при чтении, поэтому сохранённый указатель не отдаётся наружу и его изменение не
// влияет на хранимое состояние.
type AssessmentStore struct {
	mu          sync.Mutex
	assessments map[string]domain.LeadAssessmentSnapshot
}

// AssessmentStore реализует порт хранилища оценок.
var _ ports.LeadAssessmentStore = (*AssessmentStore)(nil)

// NewAssessmentStore создаёт пустое хранилище оценок.
func NewAssessmentStore() *AssessmentStore {
	return &AssessmentStore{assessments: make(map[string]domain.LeadAssessmentSnapshot)}
}

// assessmentKey строит ключ пары (jobID, companyID). Разделитель NUL не может
// встретиться в идентификаторах, поэтому пары не склеиваются друг с другом.
func assessmentKey(jobID, companyID string) string {
	return jobID + "\x00" + companyID
}

// Save сохраняет оценку как снимок. Повторный вызов для той же пары
// (jobID, companyID) заменяет прежний снимок (upsert по паре).
func (s *AssessmentStore) Save(_ context.Context, assessment *domain.LeadAssessment) error {
	if assessment == nil {
		return errors.New("memory: сохранение nil-оценки")
	}
	snapshot := assessment.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.assessments[assessmentKey(snapshot.JobID, snapshot.CompanyID)] = snapshot
	return nil
}

// GetByJobAndCompany загружает оценку компании в задании. Отсутствие даёт
// ports.ErrAssessmentNotFound.
func (s *AssessmentStore) GetByJobAndCompany(_ context.Context, jobID, companyID string) (*domain.LeadAssessment, error) {
	s.mu.Lock()
	snapshot, ok := s.assessments[assessmentKey(jobID, companyID)]
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: задание %q, компания %q", ports.ErrAssessmentNotFound, jobID, companyID)
	}
	return rehydrateAssessment(snapshot)
}

// ListByJob возвращает все оценки задания в порядке возрастания companyID.
// Задание без оценок даёт пустой список.
func (s *AssessmentStore) ListByJob(_ context.Context, jobID string) ([]*domain.LeadAssessment, error) {
	s.mu.Lock()
	snapshots := make([]domain.LeadAssessmentSnapshot, 0)
	for _, snapshot := range s.assessments {
		if snapshot.JobID != jobID {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	s.mu.Unlock()

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CompanyID < snapshots[j].CompanyID
	})

	assessments := make([]*domain.LeadAssessment, 0, len(snapshots))
	for _, snapshot := range snapshots {
		assessment, err := rehydrateAssessment(snapshot)
		if err != nil {
			return nil, err
		}
		assessments = append(assessments, assessment)
	}
	return assessments, nil
}

// LatestByCompany возвращает последнюю по времени оценку каждой из переданных
// компаний по всем заданиям. Для компании выбирается оценка с максимальным
// UpdatedAt, при равенстве — с максимальным CreatedAt, затем с наибольшим JobID.
// Компания без оценок в результат не попадает.
func (s *AssessmentStore) LatestByCompany(_ context.Context, companyIDs []string) (map[string]*domain.LeadAssessment, error) {
	wanted := make(map[string]struct{}, len(companyIDs))
	for _, id := range companyIDs {
		wanted[id] = struct{}{}
	}

	s.mu.Lock()
	latest := make(map[string]domain.LeadAssessmentSnapshot, len(companyIDs))
	for _, snapshot := range s.assessments {
		if _, ok := wanted[snapshot.CompanyID]; !ok {
			continue
		}
		current, seen := latest[snapshot.CompanyID]
		if !seen || moreRecentAssessment(snapshot, current) {
			latest[snapshot.CompanyID] = snapshot
		}
	}
	s.mu.Unlock()

	result := make(map[string]*domain.LeadAssessment, len(latest))
	for companyID, snapshot := range latest {
		assessment, err := rehydrateAssessment(snapshot)
		if err != nil {
			return nil, err
		}
		result[companyID] = assessment
	}
	return result, nil
}

// moreRecentAssessment сообщает, должна ли candidate заменить current как более
// свежая оценка компании: сначала по UpdatedAt, затем по CreatedAt, затем по
// JobID — так выбор детерминирован и не зависит от порядка обхода карты.
func moreRecentAssessment(candidate, current domain.LeadAssessmentSnapshot) bool {
	if !candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.UpdatedAt.After(current.UpdatedAt)
	}
	if !candidate.CreatedAt.Equal(current.CreatedAt) {
		return candidate.CreatedAt.After(current.CreatedAt)
	}
	return candidate.JobID > current.JobID
}

// VerdictDistribution считает распределение вердиктов по оценкам задания.
// Задание без оценок даёт нулевые счётчики.
func (s *AssessmentStore) VerdictDistribution(_ context.Context, jobID string) (ports.VerdictCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counts ports.VerdictCounts
	for _, snapshot := range s.assessments {
		if snapshot.JobID != jobID {
			continue
		}
		switch snapshot.Verdict {
		case domain.VerdictFits:
			counts.Fits++
		case domain.VerdictHypothesis:
			counts.Hypothesis++
		case domain.VerdictUnsuitable:
			counts.Unsuitable++
		}
	}
	return counts, nil
}

// rehydrateAssessment восстанавливает оценку из снимка.
func rehydrateAssessment(snapshot domain.LeadAssessmentSnapshot) (*domain.LeadAssessment, error) {
	assessment, err := domain.RehydrateLeadAssessment(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление оценки задания %q компании %q: %w", snapshot.JobID, snapshot.CompanyID, err)
	}
	return assessment, nil
}
