// Package postgres реализует ports.ProjectionStore поверх PostgreSQL.
//
// Адаптер хранит эксплуатационную проекцию дашборда снимком состояния компании:
// SaveCompany пишет снимок, LoadCompanies читает все снимки для тёплого старта.
// Повторная запись по тому же ключу (тенант, компания) заменяет прежнее состояние
// (upsert), поэтому применение события идемпотентно и переживает повтор доставки.
//
// Граница данных — schema-per-service: адаптер работает только в схеме analytics
// (Schema) и не обращается к схемам других сервисов. Текст ответов компаний
// (reply_text) не хранится ни в одном столбце: маскирование обеспечивает сам
// носитель состояния.
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому границы
// схемы и сервиса совпадают.
const Schema = "analytics"

// execer — минимальная поверхность драйвера, нужная хранилищу. Ей удовлетворяет
// пул соединений; отдельный интерфейс оставляет место для привязки к транзакции
// без смены кода запросов.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
}

// ProjectionStore — PostgreSQL-реализация ports.ProjectionStore.
type ProjectionStore struct {
	db execer
}

// Проверка соответствия порту на этапе компиляции.
var _ ports.ProjectionStore = (*ProjectionStore)(nil)

// NewProjectionStore создаёт хранилище проекции поверх пула соединений.
func NewProjectionStore(pool *pgxpool.Pool) *ProjectionStore {
	if pool == nil {
		panic("postgres: NewProjectionStore с nil-пулом")
	}
	return &ProjectionStore{db: pool}
}

// SaveCompany сохраняет снимок состояния компании. Повторный вызов для того же
// ключа заменяет прежнюю строку (upsert), не создавая вторую.
func (s *ProjectionStore) SaveCompany(ctx context.Context, record readmodel.CompanyRecord) error {
	if _, err := s.db.Exec(
		ctx,
		upsertCompany,
		record.TenantID,
		record.CompanyID,
		string(record.Stage),
		nullTime(record.FoundAt),
		nullTime(record.SentAt),
		nullTime(record.RepliedAt),
		record.HasSent,
		record.HasReply,
		record.Source,
		record.Industry,
		record.Segment,
		record.CampaignID,
		record.ManagerID,
		nullTime(record.DeliveredAt),
		nullTime(record.TransitionedAt),
		nullTime(record.UnsubscribedAt),
		nullTime(record.BouncedAt),
		nullTime(record.WonAt),
		record.HasDelivered,
		record.HasTransition,
		record.HasUnsubscribed,
		record.HasBounce,
		record.HasWon,
		record.Validity,
		record.Duplicate,
		record.PendingDrafts,
	); err != nil {
		return fmt.Errorf("postgres: сохранение проекции компании %s/%s: %w", record.TenantID, record.CompanyID, err)
	}
	return nil
}

// LoadCompanies читает все сохранённые снимки проекции в порядке (тенант,
// компания). Пустое хранилище даёт пустой список без ошибки.
func (s *ProjectionStore) LoadCompanies(ctx context.Context) ([]readmodel.CompanyRecord, error) {
	rows, err := s.db.Query(ctx, selectCompanies)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение проекции компаний: %w", err)
	}
	defer rows.Close()

	records := make([]readmodel.CompanyRecord, 0)
	for rows.Next() {
		var (
			record                                                        readmodel.CompanyRecord
			stage                                                         string
			foundAt, sentAt, repliedAt                                    *time.Time
			deliveredAt, transitionedAt, unsubscribedAt, bouncedAt, wonAt *time.Time
		)
		if err := rows.Scan(
			&record.TenantID,
			&record.CompanyID,
			&stage,
			&foundAt,
			&sentAt,
			&repliedAt,
			&record.HasSent,
			&record.HasReply,
			&record.Source,
			&record.Industry,
			&record.Segment,
			&record.CampaignID,
			&record.ManagerID,
			&deliveredAt,
			&transitionedAt,
			&unsubscribedAt,
			&bouncedAt,
			&wonAt,
			&record.HasDelivered,
			&record.HasTransition,
			&record.HasUnsubscribed,
			&record.HasBounce,
			&record.HasWon,
			&record.Validity,
			&record.Duplicate,
			&record.PendingDrafts,
		); err != nil {
			return nil, fmt.Errorf("postgres: разбор проекции компании: %w", err)
		}
		record.Stage = readmodel.Stage(stage)
		record.FoundAt = timeValue(foundAt)
		record.SentAt = timeValue(sentAt)
		record.RepliedAt = timeValue(repliedAt)
		record.DeliveredAt = timeValue(deliveredAt)
		record.TransitionedAt = timeValue(transitionedAt)
		record.UnsubscribedAt = timeValue(unsubscribedAt)
		record.BouncedAt = timeValue(bouncedAt)
		record.WonAt = timeValue(wonAt)
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение проекции компаний: %w", err)
	}
	return records, nil
}

// nullTime переводит нулевое время в NULL: отметка «ещё не наступило» хранится
// как отсутствие значения, а не как эпоха.
func nullTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

// timeValue переводит NULL в нулевое время, а непустое — в момент.
func timeValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в схеме
// сервиса (Schema) и не параметризует её извне.
const (
	upsertCompany = `
INSERT INTO "analytics".company_projection
    (tenant_id, company_id, stage, found_at, sent_at, replied_at, has_sent, has_reply,
     source, industry, segment, campaign_id, manager_id,
     delivered_at, transitioned_at, unsubscribed_at, bounced_at, won_at,
     has_delivered, has_transition, has_unsubscribed, has_bounce, has_won,
     validity, duplicate, pending_drafts)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8,
        $9, $10, $11, $12, $13,
        $14, $15, $16, $17, $18,
        $19, $20, $21, $22, $23,
        $24, $25, $26)
ON CONFLICT (tenant_id, company_id) DO UPDATE SET
    stage           = EXCLUDED.stage,
    found_at        = EXCLUDED.found_at,
    sent_at         = EXCLUDED.sent_at,
    replied_at      = EXCLUDED.replied_at,
    has_sent        = EXCLUDED.has_sent,
    has_reply       = EXCLUDED.has_reply,
    source          = EXCLUDED.source,
    industry        = EXCLUDED.industry,
    segment         = EXCLUDED.segment,
    campaign_id     = EXCLUDED.campaign_id,
    manager_id      = EXCLUDED.manager_id,
    delivered_at    = EXCLUDED.delivered_at,
    transitioned_at = EXCLUDED.transitioned_at,
    unsubscribed_at = EXCLUDED.unsubscribed_at,
    bounced_at      = EXCLUDED.bounced_at,
    won_at          = EXCLUDED.won_at,
    has_delivered   = EXCLUDED.has_delivered,
    has_transition  = EXCLUDED.has_transition,
    has_unsubscribed = EXCLUDED.has_unsubscribed,
    has_bounce      = EXCLUDED.has_bounce,
    has_won         = EXCLUDED.has_won,
    validity        = EXCLUDED.validity,
    duplicate       = EXCLUDED.duplicate,
    pending_drafts  = EXCLUDED.pending_drafts`

	selectCompanies = `
SELECT tenant_id, company_id, stage, found_at, sent_at, replied_at, has_sent, has_reply,
       source, industry, segment, campaign_id, manager_id,
       delivered_at, transitioned_at, unsubscribed_at, bounced_at, won_at,
       has_delivered, has_transition, has_unsubscribed, has_bounce, has_won,
       validity, duplicate, pending_drafts
FROM "analytics".company_projection
ORDER BY tenant_id, company_id`
)
