package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Repository — PostgreSQL-реализация ports.RequestRepository.
//
// Репозиторий не знает о транзакциях выше себя: Save выполняется одним
// оператором, а атомарность записи агрегата и его событий обеспечивает
// unit-of-work, привязывающий репозиторий к транзакции через NewTx.
type Repository struct {
	db   execer
	ping func(context.Context) error
}

// Repository реализует порт репозитория заявок.
var _ ports.RequestRepository = (*Repository)(nil)

// NewRepository создаёт репозиторий заявок поверх пула соединений.
func NewRepository(pool *pgxpool.Pool) *Repository {
	if pool == nil {
		panic("postgres: NewRepository с nil-пулом")
	}
	return &Repository{db: pool, ping: pool.Ping}
}

// Ping проверяет реальную доступность пула без чтения или изменения доменных данных.
func (r *Repository) Ping(ctx context.Context) error {
	if r == nil || r.ping == nil {
		return errors.New("request-fulfillment: PostgreSQL pool is not configured")
	}
	return r.ping(ctx)
}

// NewTx создаёт репозиторий, привязанный к активной транзакции. Запись идёт в
// рамках этой транзакции: commit и rollback остаются за unit-of-work.
func NewTx(tx pgx.Tx) *Repository {
	if tx == nil {
		panic("postgres: NewTx с nil-транзакцией")
	}
	return &Repository{db: tx}
}

// Save сохраняет агрегат целиком как снимок с оптимистичной блокировкой.
//
// expectedVersion — версия, прочитанная при загрузке; при создании заявки она
// равна нулю. Создание вставляет строку, и занятый идентификатор отвергается
// ошибкой ports.ErrVersionConflict (ON CONFLICT DO NOTHING не трогает чужую
// версию). Изменение выполняется UPDATE ... WHERE version = $expected: ноль
// затронутых строк означает, что сохранённая версия не совпала с ожидаемой,
// поэтому и создание с ненулевой ожидаемой версией, и устаревшее изменение дают
// один и тот же конфликт, а состояние не меняется.
func (r *Repository) Save(ctx context.Context, request *domain.Request, expectedVersion int) error {
	if request == nil {
		return errors.New("postgres: сохранение nil-заявки")
	}
	snapshot := request.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация заявки %s: %w", snapshot.ID, err)
	}

	var tag pgconn.CommandTag
	if expectedVersion == 0 {
		tag, err = r.db.Exec(ctx, insertRequestNew,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			string(snapshot.Status),
			snapshot.Version,
			string(raw),
		)
	} else {
		tag, err = r.db.Exec(ctx, updateRequest,
			snapshot.ID.String(),
			snapshot.TenantID.String(),
			string(snapshot.Status),
			snapshot.Version,
			string(raw),
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись заявки %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: заявка %s: ожидалась версия %d", ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	return nil
}

// FindByID загружает заявку по идентификатору через domain.RehydrateRequest.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrRequestNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.RequestID) (*domain.Request, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	request, err := domain.RehydrateRequest(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление заявки %s: %w", id, err)
	}
	return request, nil
}

// FindByCampaign загружает заявку по идентификатору кампании в границах
// тенанта. Кампания хранится в снимке (JSONB); запрос обслуживается выраженным
// индексом (миграция 0003). Отсутствие даёт ports.ErrRequestNotFound.
func (r *Repository) FindByCampaign(ctx context.Context, tenantID domain.TenantID, campaignID domain.CampaignID) (*domain.Request, error) {
	if strings.TrimSpace(string(campaignID)) == "" {
		return nil, fmt.Errorf("%w: пустая кампания", ports.ErrRequestNotFound)
	}
	rows, err := r.db.Query(ctx, selectRequestsByCampaign, tenantID.String(), campaignID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение заявки по кампании %s: %w", campaignID, err)
	}
	defer rows.Close()

	var snapshots []domain.RequestSnapshot
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор заявки по кампании %s: %w", campaignID, err)
		}
		var snapshot domain.RequestSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка заявки: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение заявки по кампании %s: %w", campaignID, err)
	}
	if len(snapshots) == 0 {
		return nil, fmt.Errorf("%w: кампания %s", ports.ErrRequestNotFound, campaignID)
	}
	if len(snapshots) > 1 {
		return nil, fmt.Errorf("postgres: кампания %s неоднозначна: найдено %d заявок", campaignID, len(snapshots))
	}
	request, err := domain.RehydrateRequest(snapshots[0])
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление заявки %s: %w", snapshots[0].ID, err)
	}
	return request, nil
}

// ListByStatus возвращает заявки в заданном статусе, упорядоченные по
// идентификатору. Обслуживает чтение очереди валидации (submitted).
func (r *Repository) ListByStatus(ctx context.Context, status domain.RequestStatus) ([]*domain.Request, error) {
	rows, err := r.db.Query(ctx, selectRequestsByStatus, string(status))
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение заявок в статусе %q: %w", status, err)
	}
	defer rows.Close()

	requests := make([]*domain.Request, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор заявки в статусе %q: %w", status, err)
		}
		var snapshot domain.RequestSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка заявки: %w", err)
		}
		request, err := domain.RehydrateRequest(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление заявки %s: %w", snapshot.ID, err)
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение заявок в статусе %q: %w", status, err)
	}
	return requests, nil
}

// ListByTenant возвращает все заявки тенанта, упорядоченные по идентификатору.
// Обслуживает историю заявок клиента и список заявок компании в админке.
func (r *Repository) ListByTenant(ctx context.Context, tenantID domain.TenantID) ([]*domain.Request, error) {
	rows, err := r.db.Query(ctx, selectRequestsByTenant, tenantID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение заявок тенанта %s: %w", tenantID, err)
	}
	defer rows.Close()

	requests := make([]*domain.Request, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор заявки тенанта %s: %w", tenantID, err)
		}
		var snapshot domain.RequestSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка заявки: %w", err)
		}
		request, err := domain.RehydrateRequest(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление заявки %s: %w", snapshot.ID, err)
		}
		requests = append(requests, request)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение заявок тенанта %s: %w", tenantID, err)
	}
	return requests, nil
}

// CountByTenant возвращает число заявок тенанта в нетерминальных статусах.
func (r *Repository) CountByTenant(ctx context.Context, tenantID domain.TenantID) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx, countRequestsNonTerminal, tenantID.String()).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: подсчёт заявок тенанта %s: %w", tenantID, err)
	}
	return count, nil
}

// CountInWork возвращает число заявок тенанта в статусе processing.
func (r *Repository) CountInWork(ctx context.Context, tenantID domain.TenantID) (int, error) {
	var count int
	if err := r.db.QueryRow(ctx, countRequestsProcessing, tenantID.String()).Scan(&count); err != nil {
		return 0, fmt.Errorf("postgres: подсчёт заявок в работе тенанта %s: %w", tenantID, err)
	}
	return count, nil
}

// loadSnapshot читает снимок заявки. Отсутствие даёт ports.ErrRequestNotFound.
func (r *Repository) loadSnapshot(ctx context.Context, id domain.RequestID) (domain.RequestSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectRequest, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.RequestSnapshot{}, fmt.Errorf("%w: %s", ports.ErrRequestNotFound, id)
	}
	if err != nil {
		return domain.RequestSnapshot{}, fmt.Errorf("postgres: чтение заявки %s: %w", id, err)
	}
	var snapshot domain.RequestSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		return domain.RequestSnapshot{}, fmt.Errorf("postgres: разбор снимка заявки %s: %w", id, err)
	}
	return snapshot, nil
}

// SQL-запросы адаптера заявок.
const (
	insertRequestNew = `
INSERT INTO "request-fulfillment".request (id, tenant_id, status, version, snapshot, updated_at)
VALUES ($1, $2, $3, $4, $5::jsonb, now())
ON CONFLICT (id) DO NOTHING`

	updateRequest = `
UPDATE "request-fulfillment".request
SET tenant_id  = $2,
    status     = $3,
    version    = $4,
    snapshot   = $5::jsonb,
    updated_at = now()
WHERE id = $1 AND version = $6`

	selectRequest = `
SELECT snapshot::text
FROM "request-fulfillment".request
WHERE id = $1`

	selectRequestsByStatus = `
SELECT snapshot::text
FROM "request-fulfillment".request
WHERE status = $1
ORDER BY id`

	selectRequestsByTenant = `
SELECT snapshot::text
FROM "request-fulfillment".request
WHERE tenant_id = $1
ORDER BY id`

	selectRequestsByCampaign = `
SELECT snapshot::text
FROM "request-fulfillment".request
WHERE tenant_id = $1 AND snapshot->>'CampaignID' = $2
ORDER BY id`

	countRequestsNonTerminal = `
SELECT count(*)
FROM "request-fulfillment".request
WHERE tenant_id = $1 AND status NOT IN ('fulfilled', 'rejected', 'cancelled', 'failed')`

	countRequestsProcessing = `
SELECT count(*)
FROM "request-fulfillment".request
WHERE tenant_id = $1 AND status = 'processing'`
)
