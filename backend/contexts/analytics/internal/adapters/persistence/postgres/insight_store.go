package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// InsightStore — PostgreSQL-реализация ports.InsightStore: снимки качества
// поиска и применения рекомендаций. Снимок хранит исходы, watermark и версию
// формул, поэтому срез воспроизводим; персональных данных в таблицах нет.
//
// Граница данных — schema-per-service: адаптер работает только в схеме analytics.
type InsightStore struct {
	db execer
}

// InsightStore реализует порт на этапе компиляции.
var _ ports.InsightStore = (*InsightStore)(nil)

// NewInsightStore создаёт хранилище инсайтов поверх пула соединений.
func NewInsightStore(pool *pgxpool.Pool) *InsightStore {
	if pool == nil {
		panic("postgres: NewInsightStore с nil-пулом")
	}
	return &InsightStore{db: pool}
}

// SaveSnapshot сохраняет снимок тенанта, заменяя прежний (upsert по тенанту).
func (s *InsightStore) SaveSnapshot(ctx context.Context, snapshot readmodel.InsightSnapshot) error {
	outcomes, err := json.Marshal(snapshot.Outcomes)
	if err != nil {
		return fmt.Errorf("postgres: сериализация снимка инсайтов: %w", err)
	}
	if _, err := s.db.Exec(ctx, upsertInsightSnapshot,
		snapshot.TenantID,
		nullTime(snapshot.Watermark),
		snapshot.FormulaVersion,
		outcomes,
	); err != nil {
		return fmt.Errorf("postgres: сохранение снимка инсайтов %s: %w", snapshot.TenantID, err)
	}
	return nil
}

// LoadSnapshots читает снимки всех тенантов в порядке (тенант).
func (s *InsightStore) LoadSnapshots(ctx context.Context) ([]readmodel.InsightSnapshot, error) {
	rows, err := s.db.Query(ctx, selectInsightSnapshots)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение снимков инсайтов: %w", err)
	}
	defer rows.Close()

	snapshots := make([]readmodel.InsightSnapshot, 0)
	for rows.Next() {
		var (
			snapshot  readmodel.InsightSnapshot
			watermark *time.Time
			outcomes  []byte
		)
		if err := rows.Scan(&snapshot.TenantID, &watermark, &snapshot.FormulaVersion, &outcomes); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка инсайтов: %w", err)
		}
		snapshot.Watermark = timeValue(watermark)
		if len(outcomes) > 0 {
			if err := json.Unmarshal(outcomes, &snapshot.Outcomes); err != nil {
				return nil, fmt.Errorf("postgres: разбор исходов снимка: %w", err)
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение снимков инсайтов: %w", err)
	}
	return snapshots, nil
}

// SaveApplication фиксирует применение рекомендации (upsert по ключу).
func (s *InsightStore) SaveApplication(ctx context.Context, application readmodel.RecommendationApplication) error {
	reasons, err := json.Marshal(application.Before.Reasons)
	if err != nil {
		return fmt.Errorf("postgres: сериализация причин применения: %w", err)
	}
	if _, err := s.db.Exec(ctx, upsertInsightApplication,
		application.TenantID,
		application.ScopeKind,
		application.ScopeKey,
		application.AppliedAt,
		application.Reference,
		application.Action,
		string(application.Reason),
		application.Basis,
		nullTime(application.BeforeWatermark),
		application.Before.Total,
		application.Before.Verdicts.Fits,
		application.Before.Verdicts.Hypothesis,
		application.Before.Verdicts.Unsuitable,
		application.Before.Verdicts.ExtractionErrors,
		reasons,
	); err != nil {
		return fmt.Errorf("postgres: фиксация применения рекомендации %s/%s: %w", application.TenantID, application.ScopeKey, err)
	}
	return nil
}

// LoadApplications читает применения рекомендаций в порядке (тенант, срез, время).
func (s *InsightStore) LoadApplications(ctx context.Context) ([]readmodel.RecommendationApplication, error) {
	rows, err := s.db.Query(ctx, selectInsightApplications)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение применений рекомендаций: %w", err)
	}
	defer rows.Close()

	applications := make([]readmodel.RecommendationApplication, 0)
	for rows.Next() {
		var (
			application readmodel.RecommendationApplication
			reason      string
			beforeAt    *time.Time
			reasons     []byte
		)
		if err := rows.Scan(
			&application.TenantID,
			&application.ScopeKind,
			&application.ScopeKey,
			&application.AppliedAt,
			&application.Reference,
			&application.Action,
			&reason,
			&application.Basis,
			&beforeAt,
			&application.Before.Total,
			&application.Before.Verdicts.Fits,
			&application.Before.Verdicts.Hypothesis,
			&application.Before.Verdicts.Unsuitable,
			&application.Before.Verdicts.ExtractionErrors,
			&reasons,
		); err != nil {
			return nil, fmt.Errorf("postgres: разбор применения рекомендации: %w", err)
		}
		application.Reason = readmodel.ReasonCategory(reason)
		application.BeforeWatermark = timeValue(beforeAt)
		if len(reasons) > 0 {
			if err := json.Unmarshal(reasons, &application.Before.Reasons); err != nil {
				return nil, fmt.Errorf("postgres: разбор причин применения: %w", err)
			}
		}
		applications = append(applications, application)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение применений рекомендаций: %w", err)
	}
	return applications, nil
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в схеме
// сервиса (Schema).
const (
	upsertInsightSnapshot = `
INSERT INTO "analytics".quality_insight_snapshot
    (tenant_id, watermark, formula_version, outcomes, updated_at)
VALUES ($1, $2, $3, $4, now())
ON CONFLICT (tenant_id) DO UPDATE SET
    watermark       = EXCLUDED.watermark,
    formula_version = EXCLUDED.formula_version,
    outcomes        = EXCLUDED.outcomes,
    updated_at      = now()`

	selectInsightSnapshots = `
SELECT tenant_id, watermark, formula_version, outcomes
FROM "analytics".quality_insight_snapshot
ORDER BY tenant_id`

	upsertInsightApplication = `
INSERT INTO "analytics".quality_insight_application
    (tenant_id, scope_kind, scope_key, applied_at, reference, action, reason, basis,
     before_watermark, before_total, before_fits, before_hypothesis, before_unsuitable,
     before_extraction_errors, before_reasons)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (tenant_id, scope_kind, scope_key, applied_at) DO UPDATE SET
    reference                = EXCLUDED.reference,
    action                   = EXCLUDED.action,
    reason                   = EXCLUDED.reason,
    basis                    = EXCLUDED.basis,
    before_watermark         = EXCLUDED.before_watermark,
    before_total             = EXCLUDED.before_total,
    before_fits              = EXCLUDED.before_fits,
    before_hypothesis        = EXCLUDED.before_hypothesis,
    before_unsuitable        = EXCLUDED.before_unsuitable,
    before_extraction_errors = EXCLUDED.before_extraction_errors,
    before_reasons           = EXCLUDED.before_reasons`

	selectInsightApplications = `
SELECT tenant_id, scope_kind, scope_key, applied_at, reference, action, reason, basis,
       before_watermark, before_total, before_fits, before_hypothesis, before_unsuitable,
       before_extraction_errors, before_reasons
FROM "analytics".quality_insight_application
ORDER BY tenant_id, scope_kind, scope_key, applied_at`
)
