// Package postgres реализует ports.CampaignRepository поверх PostgreSQL.
//
// Адаптер хранит агрегат не указателем, а снимком (domain.CampaignSnapshot):
// Save пишет снимок, FindByID читает его и восстанавливает агрегат через
// domain.RehydrateCampaign. Поэтому сохранённое состояние изолировано от
// вызывающего, а загрузка заново проверяет инварианты.
//
// Граница данных — schema-per-service: адаптер работает только в схеме
// outreach-orchestration (Schema) и не обращается к схемам других сервисов.
//
// # Транзакция
//
// Репозиторий не открывает транзакцию сам, если привязан к ней: NewTx возвращает
// репозиторий в рамках активной транзакции unit-of-work, где запись агрегата и
// запись его событий атомарны. New, напротив, получает пул соединений и для
// каждого Save открывает собственную транзакцию, чтобы составная запись
// (заголовок кампании, черновики, решения) была атомарной и при вызове без
// внешней транзакции.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Schema — схема сервиса в общем кластере PostgreSQL. Имя совпадает с
// идентификатором контекста (backend/platform/migrate.OwnSchema), поэтому
// границы схемы и сервиса совпадают.
const Schema = "outreach-orchestration"

// execer — минимальная поверхность драйвера, нужная репозиторию. Ей
// удовлетворяют и пул соединений, и активная транзакция, поэтому один и тот же
// код работает и вне, и внутри транзакции unit-of-work.
type execer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
}

// Repository — PostgreSQL-реализация ports.CampaignRepository и его
// аддитивного расширения ports.DraftWindowRepository.
type Repository struct {
	db    execer
	begin func(context.Context) (pgx.Tx, error)
}

var (
	_ ports.CampaignRepository    = (*Repository)(nil)
	_ ports.DraftWindowRepository = (*Repository)(nil)
	_ ports.CampaignLister        = (*Repository)(nil)
)

// New создаёт репозиторий поверх пула соединений. Каждый Save выполняется в
// собственной транзакции.
func New(pool *pgxpool.Pool) *Repository {
	if pool == nil {
		panic("postgres: New с nil-пулом")
	}
	return &Repository{db: pool, begin: pool.Begin}
}

// NewTx создаёт репозиторий, привязанный к активной транзакции. Запись идёт в
// рамках этой транзакции: commit и rollback остаются за unit-of-work.
// Транзакций нижнего уровня не открывается.
func NewTx(tx pgx.Tx) *Repository {
	if tx == nil {
		panic("postgres: NewTx с nil-транзакцией")
	}
	return &Repository{db: tx}
}

// Save сохраняет агрегат целиком как снимок. Повторный вызов для той же
// кампании заменяет прежнее состояние (upsert по идентификатору), не создавая
// вторую кампанию. Составная запись атомарна: заголовок, черновики и решения
// пишутся в одной транзакции либо не пишутся вовсе.
func (r *Repository) Save(ctx context.Context, campaign *domain.Campaign) error {
	if campaign == nil {
		return errors.New("postgres: сохранение nil-кампании")
	}
	snapshot := campaign.Snapshot()
	return r.withTransaction(ctx, func(q execer) error {
		return saveSnapshot(ctx, q, snapshot)
	})
}

// FindByID загружает кампанию по идентификатору через domain.RehydrateCampaign.
// Отсутствие кампании даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrCampaignNotFound).
func (r *Repository) FindByID(ctx context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	snapshot, err := loadSnapshot(ctx, r.db, id)
	if err != nil {
		return nil, err
	}
	campaign, err := domain.RehydrateCampaign(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление кампании %s: %w", id, err)
	}
	return campaign, nil
}

// LoadDraftWindow реализует ports.DraftWindowRepository.
//
// Тенант сверяется с кампанией до выборки черновиков: окно чужого тенанта
// неотличимо от отсутствующей кампании (ports.ErrCampaignNotFound), а не
// отвергается с раскрытием существования. Keyset-переход исключающий: окно
// начинается строго после курсора запроса, поэтому соседние страницы не
// пересекаются. За один запрос читается на один черновик больше лимита, чтобы
// отличить «ровно лимит» от «есть продолжение», и лишний черновик не возвращается.
func (r *Repository) LoadDraftWindow(ctx context.Context, query ports.DraftWindowQuery) (ports.DraftWindow, error) {
	if err := query.Validate(); err != nil {
		return ports.DraftWindow{}, err
	}

	tenant, err := campaignTenant(ctx, r.db, query.CampaignID)
	if err != nil {
		return ports.DraftWindow{}, err
	}
	if tenant != query.TenantID {
		// Чужой тенант неотличим от отсутствующей кампании.
		return ports.DraftWindow{}, fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, query.CampaignID)
	}

	rows, err := r.db.Query(
		ctx,
		selectDraftWindow,
		query.CampaignID.String(),
		string(query.After.Material),
		string(query.After.Segment),
		string(query.After.DraftID),
		query.Limit+1,
	)
	if err != nil {
		return ports.DraftWindow{}, fmt.Errorf("postgres: окно черновиков кампании %s: %w", query.CampaignID, err)
	}
	defer rows.Close()

	snapshots, err := scanDraftSnapshots(rows)
	if err != nil {
		return ports.DraftWindow{}, fmt.Errorf("postgres: окно черновиков кампании %s: %w", query.CampaignID, err)
	}

	hasMore := len(snapshots) > query.Limit
	if hasMore {
		snapshots = snapshots[:query.Limit]
	}

	window := ports.DraftWindow{}
	if len(snapshots) > 0 {
		window.Drafts = make([]*domain.ActionDraft, 0, len(snapshots))
		for _, snapshot := range snapshots {
			snapshot.Campaign = query.CampaignID
			draft, err := domain.RehydrateActionDraft(snapshot)
			if err != nil {
				return ports.DraftWindow{}, fmt.Errorf("postgres: восстановление черновика %s: %w", snapshot.ID, err)
			}
			window.Drafts = append(window.Drafts, draft)
		}
		window.HasMore = hasMore
		if hasMore {
			window.Next = ports.CursorOf(window.Drafts[len(window.Drafts)-1])
		}
	}
	return window, nil
}

// ListCampaignIDs реализует ports.CampaignLister: перечисление идентификаторов
// кампаний для роли chains. Схема указана литералом: адаптер работает только в
// схеме сервиса.
func (r *Repository) ListCampaignIDs(ctx context.Context) ([]domain.CampaignID, error) {
	rows, err := r.db.Query(ctx, selectCampaignIDs)
	if err != nil {
		return nil, fmt.Errorf("postgres: перечисление кампаний: %w", err)
	}
	defer rows.Close()

	ids := make([]domain.CampaignID, 0)
	for rows.Next() {
		var id domain.CampaignID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("postgres: разбор идентификатора кампании: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: перечисление кампаний: %w", err)
	}
	return ids, nil
}

// withTransaction выполняет fn в транзакции, если репозиторий привязан к пулу;
// привязанный к транзакции репозиторий выполняет fn в ней же.
func (r *Repository) withTransaction(ctx context.Context, fn func(execer) error) error {
	if r.begin == nil {
		return fn(r.db)
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres: открытие транзакции: %w", err)
	}
	// После успешного commit повторный Rollback возвращает ErrTxClosed, поэтому
	// ошибка отката здесь намеренно игнорируется.
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: коммит транзакции: %w", err)
	}
	return nil
}

// saveSnapshot записывает снимок агрегата в рамках переданного исполнителя.
// Подчинённые таблицы предварительно очищаются: снимок содержит агрегат целиком,
// поэтому полная замена не оставляет строк, удалённых из агрегата.
func saveSnapshot(ctx context.Context, q execer, snapshot domain.CampaignSnapshot) error {
	sequences, err := json.Marshal(snapshot.Sequences)
	if err != nil {
		return fmt.Errorf("postgres: сериализация сценариев кампании %s: %w", snapshot.ID, err)
	}
	executions, err := json.Marshal(snapshot.Executions)
	if err != nil {
		return fmt.Errorf("postgres: сериализация участий кампании %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, upsertCampaign,
		snapshot.ID.String(),
		snapshot.TenantID.String(),
		snapshot.Name,
		snapshot.Window.Start(),
		snapshot.Window.End(),
		string(snapshot.State),
		sequences,
		executions,
	); err != nil {
		return fmt.Errorf("postgres: запись кампании %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, deleteDecisions, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка решений кампании %s: %w", snapshot.ID, err)
	}
	if _, err := q.Exec(ctx, deleteDrafts, snapshot.ID.String()); err != nil {
		return fmt.Errorf("postgres: очистка черновиков кампании %s: %w", snapshot.ID, err)
	}
	for position, draft := range snapshot.Drafts {
		if _, err := q.Exec(ctx, insertDraft,
			snapshot.ID.String(),
			position,
			draft.ID.String(),
			string(draft.Spec.Material),
			string(draft.Spec.Segment),
			draft.Spec.Recipient.String(),
			draft.Spec.Channel.String(),
			string(draft.State),
			draft.Version,
		); err != nil {
			return fmt.Errorf("postgres: запись черновика %s: %w", draft.ID, err)
		}
	}
	for position, decision := range snapshot.Decisions {
		if _, err := q.Exec(ctx, insertDecision,
			snapshot.ID.String(),
			position,
			decision.ID.String(),
			decision.Draft.String(),
			decision.DraftVersion,
			decision.Actor.String(),
			string(decision.Outcome),
			decision.Reason,
			decision.DecidedAt,
			decision.BatchDigest,
		); err != nil {
			return fmt.Errorf("postgres: запись решения %s: %w", decision.ID, err)
		}
	}
	return nil
}

// loadSnapshot читает снимок агрегата. Отсутствие кампании даёт
// ports.ErrCampaignNotFound.
func loadSnapshot(ctx context.Context, q execer, id domain.CampaignID) (domain.CampaignSnapshot, error) {
	snapshot := domain.CampaignSnapshot{}

	var start, end time.Time
	var sequences, executions []byte
	err := q.QueryRow(ctx, selectCampaign, id.String()).Scan(
		&snapshot.ID,
		&snapshot.TenantID,
		&snapshot.Name,
		&start,
		&end,
		&snapshot.State,
		&sequences,
		&executions,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CampaignSnapshot{}, fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, id)
	}
	if err != nil {
		return domain.CampaignSnapshot{}, fmt.Errorf("postgres: чтение кампании %s: %w", id, err)
	}
	window, err := domain.NewCampaignWindow(start, end)
	if err != nil {
		return domain.CampaignSnapshot{}, fmt.Errorf("postgres: окно кампании %s: %w", id, err)
	}
	snapshot.Window = window

	if err := unmarshalSequences(&snapshot, sequences, executions); err != nil {
		return domain.CampaignSnapshot{}, fmt.Errorf("postgres: кампания %s: %w", id, err)
	}

	drafts, err := queryDrafts(ctx, q, id)
	if err != nil {
		return domain.CampaignSnapshot{}, err
	}
	decisions, err := queryDecisions(ctx, q, id)
	if err != nil {
		return domain.CampaignSnapshot{}, err
	}
	snapshot.Drafts = drafts
	snapshot.Decisions = decisions
	return snapshot, nil
}

// unmarshalSequences восстанавливает состояние цепочек из jsonb-колонок.
// Пустое значение трактуется как отсутствие цепочек.
func unmarshalSequences(snapshot *domain.CampaignSnapshot, sequences, executions []byte) error {
	if len(sequences) > 0 {
		if err := json.Unmarshal(sequences, &snapshot.Sequences); err != nil {
			return fmt.Errorf("разбор сценариев: %w", err)
		}
	}
	if len(executions) > 0 {
		if err := json.Unmarshal(executions, &snapshot.Executions); err != nil {
			return fmt.Errorf("разбор участий: %w", err)
		}
	}
	return nil
}

func queryDrafts(ctx context.Context, q execer, id domain.CampaignID) ([]domain.ActionDraftSnapshot, error) {
	rows, err := q.Query(ctx, selectDrafts, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение черновиков кампании %s: %w", id, err)
	}
	defer rows.Close()

	snapshots, err := scanDraftSnapshots(rows)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение черновиков кампании %s: %w", id, err)
	}
	for i := range snapshots {
		snapshots[i].Campaign = id
	}
	return snapshots, nil
}

func queryDecisions(ctx context.Context, q execer, id domain.CampaignID) ([]domain.OperatorDecisionSnapshot, error) {
	rows, err := q.Query(ctx, selectDecisions, id.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение решений кампании %s: %w", id, err)
	}
	defer rows.Close()

	snapshots := make([]domain.OperatorDecisionSnapshot, 0)
	for rows.Next() {
		snapshot := domain.OperatorDecisionSnapshot{Campaign: id}
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.Draft,
			&snapshot.DraftVersion,
			&snapshot.Actor,
			&snapshot.Outcome,
			&snapshot.Reason,
			&snapshot.DecidedAt,
			&snapshot.BatchDigest,
		); err != nil {
			return nil, fmt.Errorf("postgres: разбор решения: %w", err)
		}
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение решений: %w", err)
	}
	return snapshots, nil
}

// scanDraftSnapshots читает черновики в порядке выдачи запроса. Канал
// восстанавливается через domain.ParseActionChannel, поэтому неизвестный канал
// отвергается уже на чтении.
func scanDraftSnapshots(rows pgx.Rows) ([]domain.ActionDraftSnapshot, error) {
	snapshots := make([]domain.ActionDraftSnapshot, 0)
	for rows.Next() {
		snapshot := domain.ActionDraftSnapshot{}
		var channel string
		if err := rows.Scan(
			&snapshot.ID,
			&snapshot.Spec.Material,
			&snapshot.Spec.Segment,
			&snapshot.Spec.Recipient,
			&channel,
			&snapshot.State,
			&snapshot.Version,
		); err != nil {
			return nil, fmt.Errorf("разбор черновика: %w", err)
		}
		parsed, err := domain.ParseActionChannel(channel)
		if err != nil {
			return nil, fmt.Errorf("черновик %s: %w", snapshot.ID, err)
		}
		snapshot.Spec.Channel = parsed
		snapshots = append(snapshots, snapshot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение черновиков: %w", err)
	}
	return snapshots, nil
}

// campaignTenant возвращает тенант кампании или ports.ErrCampaignNotFound.
func campaignTenant(ctx context.Context, q execer, id domain.CampaignID) (domain.TenantID, error) {
	var tenant domain.TenantID
	err := q.QueryRow(ctx, selectCampaignTenant, id.String()).Scan(&tenant)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, id)
	}
	if err != nil {
		return "", fmt.Errorf("postgres: чтение тенанта кампании %s: %w", id, err)
	}
	return tenant, nil
}

// SQL-запросы адаптера. Схема указана литералом: адаптер работает только в
// схеме сервиса (Schema) и не параметризует её извне.
const (
	upsertCampaign = `
INSERT INTO "outreach-orchestration".campaign
    (id, tenant_id, name, window_start, window_end, state, sequence_definitions, sequence_executions)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (id) DO UPDATE SET
    tenant_id            = EXCLUDED.tenant_id,
    name                 = EXCLUDED.name,
    window_start         = EXCLUDED.window_start,
    window_end           = EXCLUDED.window_end,
    state                = EXCLUDED.state,
    sequence_definitions = EXCLUDED.sequence_definitions,
    sequence_executions  = EXCLUDED.sequence_executions`

	deleteDecisions = `DELETE FROM "outreach-orchestration".operator_decision WHERE campaign_id = $1`
	deleteDrafts    = `DELETE FROM "outreach-orchestration".action_draft WHERE campaign_id = $1`

	insertDraft = `
INSERT INTO "outreach-orchestration".action_draft
    (campaign_id, position, id, material_id, segment_id, recipient_id, channel, state, version)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	insertDecision = `
INSERT INTO "outreach-orchestration".operator_decision
    (campaign_id, position, id, draft_id, draft_version, actor_id, outcome, reason, decided_at, batch_digest)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`

	selectCampaign = `
SELECT id, tenant_id, name, window_start, window_end, state, sequence_definitions, sequence_executions
FROM "outreach-orchestration".campaign
WHERE id = $1`

	selectCampaignIDs = `SELECT id FROM "outreach-orchestration".campaign`

	selectCampaignTenant = `SELECT tenant_id FROM "outreach-orchestration".campaign WHERE id = $1`

	selectDrafts = `
SELECT id, material_id, segment_id, recipient_id, channel, state, version
FROM "outreach-orchestration".action_draft
WHERE campaign_id = $1
ORDER BY position`

	selectDraftWindow = `
SELECT id, material_id, segment_id, recipient_id, channel, state, version
FROM "outreach-orchestration".action_draft
WHERE campaign_id = $1
  AND (material_id, segment_id, id) > ($2::text, $3::text, $4::text)
ORDER BY material_id, segment_id, id
LIMIT $5`

	selectDecisions = `
SELECT id, draft_id, draft_version, actor_id, outcome, reason, decided_at, batch_digest
FROM "outreach-orchestration".operator_decision
WHERE campaign_id = $1
ORDER BY position`
)
