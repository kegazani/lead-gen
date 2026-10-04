package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// IntegrationInvocationRepository — PostgreSQL-реализация
// ports.IntegrationInvocationRepository.
//
// Снимок вызова хранится одной строкой. Ключ идемпотентности денормализован
// колонкой, потому что порт ищет вызов по нему, а колонка seq — монотонная
// последовательность вставки: она разрешает порядок вызовов с совпадающим
// моментом начала, поэтому FindLatestByCorrelation детерминирован.
type IntegrationInvocationRepository struct {
	db execer
}

// IntegrationInvocationRepository реализует порт репозитория вызовов.
var _ ports.IntegrationInvocationRepository = (*IntegrationInvocationRepository)(nil)

// NewIntegrationInvocationRepository создаёт репозиторий поверх пула соединений.
func NewIntegrationInvocationRepository(pool *pgxpool.Pool) *IntegrationInvocationRepository {
	if pool == nil {
		panic("postgres: NewIntegrationInvocationRepository с nil-пулом")
	}
	return &IntegrationInvocationRepository{db: pool}
}

// Save сохраняет вызов целиком (upsert по идентификатору).
func (r *IntegrationInvocationRepository) Save(ctx context.Context, invocation *domain.IntegrationInvocation) error {
	if invocation == nil {
		return errors.New("postgres: сохранение nil-вызова")
	}
	return saveInvocation(ctx, r.db, invocation.Snapshot())
}

// PutInvocationSnapshot кладёт снимок напрямую, минуя домен. Метод существует
// для contract suite и диагностики: он позволяет поместить в хранилище намеренно
// противоречивый снимок и проверить, что загрузка отличается от «нет сущности».
func (r *IntegrationInvocationRepository) PutInvocationSnapshot(snapshot domain.IntegrationInvocationSnapshot) error {
	return saveInvocation(context.Background(), r.db, snapshot)
}

// FindByID загружает вызов по идентификатору. Отсутствие даёт ошибку, для которой
// истинно errors.Is(err, ports.ErrInvocationNotFound).
func (r *IntegrationInvocationRepository) FindByID(ctx context.Context, id domain.InvocationID) (*domain.IntegrationInvocation, error) {
	return r.find(ctx, selectInvocation, id, id)
}

// FindByIdempotencyKey загружает вызов по ключу идемпотентности. Если ключ
// повторяется, возвращается последний по порядку вставки вызов.
func (r *IntegrationInvocationRepository) FindByIdempotencyKey(ctx context.Context, key string) (*domain.IntegrationInvocation, error) {
	return r.find(ctx, selectInvocationByKey, key, key)
}

// FindLatestByCorrelation загружает последний по времени вызов в correlation-ref.
// При совпадающих моментах начала порядок разрешает последовательность вставки.
func (r *IntegrationInvocationRepository) FindLatestByCorrelation(ctx context.Context, ref domain.CorrelationRef) (*domain.IntegrationInvocation, error) {
	return r.find(ctx, selectInvocationByCorrelation, ref.String(), ref)
}

// find выполняет запрос, различает «нет сущности» и восстанавливает агрегат.
func (r *IntegrationInvocationRepository) find(ctx context.Context, query string, arg, subject any) (*domain.IntegrationInvocation, error) {
	snapshot, err := scanInvocation(r.db.QueryRow(ctx, query, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %v", ports.ErrInvocationNotFound, subject)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение вызова %v: %w", subject, err)
	}
	invocation, err := domain.RehydrateIntegrationInvocation(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление вызова %s: %w", snapshot.ID, err)
	}
	return invocation, nil
}

// saveInvocation записывает снимок вызова (upsert по идентификатору).
func saveInvocation(ctx context.Context, q execer, snapshot domain.IntegrationInvocationSnapshot) error {
	var externalType, externalAdapter, externalID any
	if snapshot.ExternalRef != nil {
		externalType = snapshot.ExternalRef.IntegrationType.String()
		externalAdapter = snapshot.ExternalRef.AdapterName.String()
		externalID = snapshot.ExternalRef.ExternalID.String()
	}
	if _, err := q.Exec(ctx, upsertInvocation,
		snapshot.ID.String(),
		snapshot.TenantID.String(),
		snapshot.CorrelationRef.String(),
		snapshot.IntegrationType.String(),
		snapshot.AdapterName.String(),
		snapshot.Operation.String(),
		snapshot.CommandKey.String(),
		invocationKeyOf(snapshot),
		string(snapshot.State),
		string(snapshot.Category),
		externalType,
		externalAdapter,
		externalID,
		snapshot.StartedAt,
		nullableTime(snapshot.FinishedAt),
	); err != nil {
		return fmt.Errorf("postgres: запись вызова %s: %w", snapshot.ID, err)
	}
	return nil
}

// scanInvocation читает снимок вызова. Необязательные привязка внешнего
// идентификатора и момент завершения читаются как NULL: их отсутствие неотличимо
// от пустого значения, потому что домен сам проверяет согласованность исхода.
func scanInvocation(row pgx.Row) (domain.IntegrationInvocationSnapshot, error) {
	snapshot := domain.IntegrationInvocationSnapshot{}
	var (
		externalType    *string
		externalAdapter *string
		externalID      *string
		finishedAt      *time.Time
	)
	err := row.Scan(
		&snapshot.ID,
		&snapshot.TenantID,
		&snapshot.CorrelationRef,
		&snapshot.IntegrationType,
		&snapshot.AdapterName,
		&snapshot.Operation,
		&snapshot.CommandKey,
		&snapshot.State,
		&snapshot.Category,
		&externalType,
		&externalAdapter,
		&externalID,
		&snapshot.StartedAt,
		&finishedAt,
	)
	if err != nil {
		return domain.IntegrationInvocationSnapshot{}, err
	}
	if finishedAt != nil {
		snapshot.FinishedAt = *finishedAt
	}
	if externalType != nil && externalAdapter != nil && externalID != nil {
		snapshot.ExternalRef = &domain.ExternalRef{
			IntegrationType: domain.IntegrationType(*externalType),
			AdapterName:     domain.AdapterName(*externalAdapter),
			ExternalID:      domain.ExternalID(*externalID),
		}
	}
	return snapshot, nil
}

// invocationKeyOf вычисляет ключ идемпотентности снимка.
func invocationKeyOf(snapshot domain.IntegrationInvocationSnapshot) string {
	return domain.InvocationKey(snapshot.TenantID, snapshot.IntegrationType, snapshot.Operation, snapshot.CommandKey)
}

// nullableTime возвращает NULL для нулевого момента (незавершённый вызов).
func nullableTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t
}

// SQL-запросы адаптера вызовов. Схема указана литералом: адаптер работает только
// в схеме сервиса (Schema) и не параметризует её извне.
const (
	upsertInvocation = `
INSERT INTO "integration-platform".integration_invocation (
    id, tenant_id, correlation_ref, integration_type, adapter_name, operation, command_key,
    idempotency_key, state, category,
    external_integration_type, external_adapter_name, external_id,
    started_at, finished_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
ON CONFLICT (id) DO UPDATE SET
    tenant_id                 = EXCLUDED.tenant_id,
    correlation_ref           = EXCLUDED.correlation_ref,
    integration_type          = EXCLUDED.integration_type,
    adapter_name              = EXCLUDED.adapter_name,
    operation                 = EXCLUDED.operation,
    command_key               = EXCLUDED.command_key,
    idempotency_key           = EXCLUDED.idempotency_key,
    state                     = EXCLUDED.state,
    category                  = EXCLUDED.category,
    external_integration_type = EXCLUDED.external_integration_type,
    external_adapter_name     = EXCLUDED.external_adapter_name,
    external_id               = EXCLUDED.external_id,
    started_at                = EXCLUDED.started_at,
    finished_at               = EXCLUDED.finished_at`

	selectInvocationColumns = `
SELECT id, tenant_id, correlation_ref, integration_type, adapter_name, operation, command_key,
       state, category, external_integration_type, external_adapter_name, external_id,
       started_at, finished_at
FROM "integration-platform".integration_invocation`

	selectInvocation = selectInvocationColumns + `
WHERE id = $1`

	selectInvocationByKey = selectInvocationColumns + `
WHERE idempotency_key = $1
ORDER BY seq DESC
LIMIT 1`

	selectInvocationByCorrelation = selectInvocationColumns + `
WHERE correlation_ref = $1
ORDER BY started_at DESC, seq DESC
LIMIT 1`
)
