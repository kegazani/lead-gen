package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// AssessmentStore — PostgreSQL-реализация ports.LeadAssessmentStore. Ключ записи
// — пара (job_id, company_id): оценка хранится в контексте задания и не является
// постоянным свойством компании, поэтому у одной компании столько независимых
// оценок, сколько заданий её нашли.
type AssessmentStore struct {
	pool *pgxpool.Pool
}

// AssessmentStore реализует порт хранилища оценок.
var _ ports.LeadAssessmentStore = (*AssessmentStore)(nil)

// NewAssessmentStore создаёт хранилище поверх пула соединений.
func NewAssessmentStore(pool *pgxpool.Pool) *AssessmentStore {
	if pool == nil {
		panic("postgres: NewAssessmentStore с nil-пулом")
	}
	return &AssessmentStore{pool: pool}
}

// Save сохраняет оценку как снимок. Повторная запись для той же пары
// (job_id, company_id) заменяет прежний снимок целиком (upsert по паре), как и
// in-memory эталон: перезаписываются и метки времени снимка.
func (s *AssessmentStore) Save(ctx context.Context, assessment *domain.LeadAssessment) error {
	if assessment == nil {
		return errors.New("postgres: сохранение nil-оценки")
	}
	snapshot := assessment.Snapshot()
	evidence, err := json.Marshal(snapshot.Evidence)
	if err != nil {
		return fmt.Errorf("postgres: кодирование свидетельств оценки %s/%s: %w", snapshot.JobID, snapshot.CompanyID, err)
	}
	if _, err := s.pool.Exec(ctx, upsertLeadAssessment,
		snapshot.JobID,
		snapshot.CompanyID,
		string(snapshot.Verdict),
		string(snapshot.SignalKind),
		string(evidence),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись оценки задания %q компании %q: %w", snapshot.JobID, snapshot.CompanyID, err)
	}
	return nil
}

// GetByJobAndCompany загружает оценку компании в задании. Отсутствие даёт
// ошибку, оборачивающую ports.ErrAssessmentNotFound.
func (s *AssessmentStore) GetByJobAndCompany(ctx context.Context, jobID, companyID string) (*domain.LeadAssessment, error) {
	snapshot, err := scanLeadAssessment(s.pool.QueryRow(ctx, selectLeadAssessment, jobID, companyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: задание %q, компания %q", ports.ErrAssessmentNotFound, jobID, companyID)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение оценки задания %q компании %q: %w", jobID, companyID, err)
	}
	return rehydrateLeadAssessment(snapshot)
}

// ListByJob возвращает все оценки задания в порядке возрастания company_id.
// Задание без оценок даёт пустой список.
func (s *AssessmentStore) ListByJob(ctx context.Context, jobID string) ([]*domain.LeadAssessment, error) {
	rows, err := s.pool.Query(ctx, selectLeadAssessmentsByJob, jobID)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение оценок задания %q: %w", jobID, err)
	}
	defer rows.Close()

	assessments := make([]*domain.LeadAssessment, 0)
	for rows.Next() {
		snapshot, err := scanLeadAssessment(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор оценки задания %q: %w", jobID, err)
		}
		assessment, err := rehydrateLeadAssessment(snapshot)
		if err != nil {
			return nil, err
		}
		assessments = append(assessments, assessment)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение оценок задания %q: %w", jobID, err)
	}
	return assessments, nil
}

// LatestByCompany возвращает последнюю по времени оценку каждой из переданных
// компаний по всем заданиям. Для компании выбирается оценка с максимальным
// updated_at, при равенстве — с максимальным created_at, затем с наибольшим
// job_id (порядок DISTINCT ON). Компания без оценок в результат не попадает.
func (s *AssessmentStore) LatestByCompany(ctx context.Context, companyIDs []string) (map[string]*domain.LeadAssessment, error) {
	result := make(map[string]*domain.LeadAssessment, len(companyIDs))
	if len(companyIDs) == 0 {
		return result, nil
	}
	rows, err := s.pool.Query(ctx, selectLatestAssessmentsByCompany, companyIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение последних оценок компаний: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		snapshot, err := scanLeadAssessment(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор последней оценки компании: %w", err)
		}
		assessment, err := rehydrateLeadAssessment(snapshot)
		if err != nil {
			return nil, err
		}
		result[snapshot.CompanyID] = assessment
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение последних оценок компаний: %w", err)
	}
	return result, nil
}

// VerdictDistribution считает распределение вердиктов по оценкам задания.
// Задание без оценок даёт нулевые счётчики. Неизвестный вердикт в базе
// пропускается: распределение знает только согласованный набор.
func (s *AssessmentStore) VerdictDistribution(ctx context.Context, jobID string) (ports.VerdictCounts, error) {
	rows, err := s.pool.Query(ctx, selectAssessmentVerdictDistribution, jobID)
	if err != nil {
		return ports.VerdictCounts{}, fmt.Errorf("postgres: чтение распределения оценок задания %q: %w", jobID, err)
	}
	defer rows.Close()

	var counts ports.VerdictCounts
	for rows.Next() {
		var (
			verdict string
			count   int
		)
		if err := rows.Scan(&verdict, &count); err != nil {
			return ports.VerdictCounts{}, fmt.Errorf("postgres: разбор распределения оценок задания %q: %w", jobID, err)
		}
		switch domain.AssessmentVerdict(verdict) {
		case domain.VerdictFits:
			counts.Fits += count
		case domain.VerdictHypothesis:
			counts.Hypothesis += count
		case domain.VerdictUnsuitable:
			counts.Unsuitable += count
		}
	}
	if err := rows.Err(); err != nil {
		return ports.VerdictCounts{}, fmt.Errorf("postgres: чтение распределения оценок задания %q: %w", jobID, err)
	}
	return counts, nil
}

// scanLeadAssessment разбирает строку оценки в снимок. Запросы заранее
// COALESCE-руют evidence, поэтому значение не-null при сканировании.
func scanLeadAssessment(scanner rowScanner) (domain.LeadAssessmentSnapshot, error) {
	var (
		snapshot    domain.LeadAssessmentSnapshot
		verdict     string
		signalKind  string
		evidenceRaw string
	)
	if err := scanner.Scan(
		&snapshot.JobID,
		&snapshot.CompanyID,
		&verdict,
		&signalKind,
		&evidenceRaw,
		&snapshot.CreatedAt,
		&snapshot.UpdatedAt,
	); err != nil {
		return domain.LeadAssessmentSnapshot{}, err
	}
	snapshot.Verdict = domain.AssessmentVerdict(verdict)
	snapshot.SignalKind = domain.SignalKind(signalKind)
	evidence, err := unmarshalEvidence([]byte(evidenceRaw))
	if err != nil {
		return domain.LeadAssessmentSnapshot{}, err
	}
	snapshot.Evidence = evidence
	return snapshot, nil
}

// unmarshalEvidence разбирает JSON-снимок свидетельств; пустой ввод и null дают
// пустой срез.
func unmarshalEvidence(raw []byte) ([]domain.Evidence, error) {
	if len(raw) == 0 {
		return []domain.Evidence{}, nil
	}
	var evidence []domain.Evidence
	if err := json.Unmarshal(raw, &evidence); err != nil {
		return nil, err
	}
	if evidence == nil {
		evidence = []domain.Evidence{}
	}
	return evidence, nil
}

// rehydrateLeadAssessment восстанавливает оценку из снимка, заново проверяя
// инварианты агрегата.
func rehydrateLeadAssessment(snapshot domain.LeadAssessmentSnapshot) (*domain.LeadAssessment, error) {
	assessment, err := domain.RehydrateLeadAssessment(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление оценки задания %q компании %q: %w", snapshot.JobID, snapshot.CompanyID, err)
	}
	return assessment, nil
}

// SQL-запросы адаптера оценок. evidence в SELECT заранее COALESCE-руется, чтобы
// NULL не ломал сканирование.
const (
	upsertLeadAssessment = `
INSERT INTO "lead-management".lead_assessment
    (job_id, company_id, verdict, signal_kind, evidence, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7)
ON CONFLICT (job_id, company_id) DO UPDATE SET
    verdict = EXCLUDED.verdict,
    signal_kind = EXCLUDED.signal_kind,
    evidence = EXCLUDED.evidence,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectLeadAssessment = `
SELECT job_id, company_id, verdict, signal_kind, COALESCE(evidence::text, '[]'), created_at, updated_at
FROM "lead-management".lead_assessment
WHERE job_id = $1 AND company_id = $2`

	selectLeadAssessmentsByJob = `
SELECT job_id, company_id, verdict, signal_kind, COALESCE(evidence::text, '[]'), created_at, updated_at
FROM "lead-management".lead_assessment
WHERE job_id = $1
ORDER BY company_id`

	selectLatestAssessmentsByCompany = `
SELECT DISTINCT ON (company_id)
    job_id, company_id, verdict, signal_kind, COALESCE(evidence::text, '[]'), created_at, updated_at
FROM "lead-management".lead_assessment
WHERE company_id = ANY($1::text[])
ORDER BY company_id, updated_at DESC, created_at DESC, job_id DESC`

	selectAssessmentVerdictDistribution = `
SELECT verdict, COUNT(*)
FROM "lead-management".lead_assessment
WHERE job_id = $1
GROUP BY verdict`
)
