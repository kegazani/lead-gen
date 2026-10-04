package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// Repository — PostgreSQL-реализация ports.EngagementRepository: хранилище
// агрегатов engagement-event.
type Repository struct {
	db execer
}

// Repository реализует порт хранилища событий взаимодействия.
var _ ports.EngagementRepository = (*Repository)(nil)

// New создаёт репозиторий поверх пула соединений.
func New(pool *pgxpool.Pool) *Repository {
	if pool == nil {
		panic("postgres: New с nil-пулом")
	}
	return &Repository{db: pool}
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
// expectedVersion — версия, прочитанная при загрузке; при создании равна нулю.
// Создание вставляет строку, и занятый идентификатор отвергается ошибкой
// ports.ErrVersionConflict (ON CONFLICT DO NOTHING не трогает чужую версию).
// Изменение выполняется UPDATE ... WHERE version = $expected: ноль затронутых
// строк означает расхождение сохранённой и ожидаемой версии, поэтому и создание
// с ненулевой ожидаемой версией, и устаревшее изменение дают один и тот же
// конфликт, а состояние не меняется.
func (r *Repository) Save(ctx context.Context, event *domain.EngagementEvent, expectedVersion int) error {
	if event == nil {
		return errors.New("postgres: сохранение nil-события")
	}
	snapshot := event.Snapshot()

	var (
		tag pgconn.CommandTag
		err error
	)
	if expectedVersion == 0 {
		tag, err = r.db.Exec(ctx, insertEvent,
			snapshot.ID.String(),
			snapshot.SignalID.String(),
			snapshot.TenantID.String(),
			snapshot.CompanyID.String(),
			string(snapshot.Kind),
			string(snapshot.Confidence),
			string(snapshot.Outcome),
			string(snapshot.LinkRef),
			snapshot.Target,
			snapshot.OccurredAt,
			string(snapshot.CorrelationID),
			string(snapshot.CausationID),
			snapshot.Version,
		)
	} else {
		tag, err = r.db.Exec(ctx, updateEvent,
			snapshot.ID.String(),
			snapshot.SignalID.String(),
			snapshot.TenantID.String(),
			snapshot.CompanyID.String(),
			string(snapshot.Kind),
			string(snapshot.Confidence),
			string(snapshot.Outcome),
			string(snapshot.LinkRef),
			snapshot.Target,
			snapshot.OccurredAt,
			string(snapshot.CorrelationID),
			string(snapshot.CausationID),
			snapshot.Version,
			expectedVersion,
		)
	}
	if err != nil {
		return fmt.Errorf("postgres: запись события %s: %w", snapshot.ID, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: событие %s: ожидалась версия %d", ports.ErrVersionConflict, snapshot.ID, expectedVersion)
	}
	return nil
}

// FindByID загружает агрегат по идентификатору через domain.Rehydrate.
// Отсутствие даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrEventNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.EngagementEventID) (*domain.EngagementEvent, error) {
	snapshot, err := scanEvent(r.db.QueryRow(ctx, selectEvent, id.String()))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrEventNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение события %s: %w", id, err)
	}
	event, err := domain.Rehydrate(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление события %s: %w", id, err)
	}
	return event, nil
}

// ListByTenant возвращает события тенанта, упорядоченные по идентификатору.
func (r *Repository) ListByTenant(ctx context.Context, tenant domain.TenantID) ([]*domain.EngagementEvent, error) {
	rows, err := r.db.Query(ctx, selectEventsByTenant, tenant.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение событий тенанта %s: %w", tenant, err)
	}
	defer rows.Close()

	events := make([]*domain.EngagementEvent, 0)
	for rows.Next() {
		snapshot, err := scanEvent(rows)
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор события тенанта %s: %w", tenant, err)
		}
		event, err := domain.Rehydrate(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление события %s: %w", snapshot.ID, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение событий тенанта %s: %w", tenant, err)
	}
	return events, nil
}

// scanEvent читает снимок события из строки запроса. Значения читаются во
// встроенные типы и затем переводятся в именованные типы домена, чтобы разбор не
// зависел от поддержки драйвером именованных строковых типов.
func scanEvent(row rowScanner) (domain.EngagementSnapshot, error) {
	var (
		id            string
		signalID      string
		tenantID      string
		companyID     string
		kind          string
		confidence    string
		outcome       string
		linkRef       string
		target        string
		occurredAt    time.Time
		correlationID string
		causationID   string
		version       int
	)
	if err := row.Scan(
		&id,
		&signalID,
		&tenantID,
		&companyID,
		&kind,
		&confidence,
		&outcome,
		&linkRef,
		&target,
		&occurredAt,
		&correlationID,
		&causationID,
		&version,
	); err != nil {
		return domain.EngagementSnapshot{}, err
	}
	return domain.EngagementSnapshot{
		ID:            domain.EngagementEventID(id),
		SignalID:      domain.SignalID(signalID),
		TenantID:      domain.TenantID(tenantID),
		CompanyID:     domain.CompanyID(companyID),
		Kind:          domain.EngagementKind(kind),
		Confidence:    domain.SignalConfidence(confidence),
		Outcome:       domain.Outcome(outcome),
		LinkRef:       domain.LinkRef(linkRef),
		Target:        target,
		OccurredAt:    occurredAt,
		CorrelationID: domain.CorrelationID(correlationID),
		CausationID:   domain.CausationID(causationID),
		Version:       version,
	}, nil
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в схеме
// сервиса (Schema) и не параметризует её извне.
const (
	insertEvent = `
INSERT INTO "engagement-observation".engagement_event
    (id, signal_id, tenant_id, company_id, kind, confidence, outcome,
     link_ref, target, occurred_at, correlation_id, causation_id, version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (id) DO NOTHING`

	updateEvent = `
UPDATE "engagement-observation".engagement_event
SET signal_id      = $2,
    tenant_id      = $3,
    company_id     = $4,
    kind           = $5,
    confidence     = $6,
    outcome        = $7,
    link_ref       = $8,
    target         = $9,
    occurred_at    = $10,
    correlation_id = $11,
    causation_id   = $12,
    version        = $13
WHERE id = $1 AND version = $14`

	selectEvent = `
SELECT id, signal_id, tenant_id, company_id, kind, confidence, outcome,
       link_ref, target, occurred_at, correlation_id, causation_id, version
FROM "engagement-observation".engagement_event
WHERE id = $1`

	selectEventsByTenant = `
SELECT id, signal_id, tenant_id, company_id, kind, confidence, outcome,
       link_ref, target, occurred_at, correlation_id, causation_id, version
FROM "engagement-observation".engagement_event
WHERE tenant_id = $1
ORDER BY id`
)
