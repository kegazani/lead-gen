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

// OutboundMessageRepository — PostgreSQL-реализация ports.OutboundMessageRepository.
type OutboundMessageRepository struct {
	db execer
}

// OutboundMessageRepository реализует порт репозитория исходящих писем.
var _ ports.OutboundMessageRepository = (*OutboundMessageRepository)(nil)

// NewOutboundMessageRepository создаёт репозиторий поверх пула соединений.
func NewOutboundMessageRepository(pool *pgxpool.Pool) *OutboundMessageRepository {
	if pool == nil {
		panic("postgres: NewOutboundMessageRepository с nil-пулом")
	}
	return &OutboundMessageRepository{db: pool}
}

// Save сохраняет сообщение целиком как снимок в JSONB. Повторный вызов для того
// же сообщения заменяет прежний снимок (upsert по идентификатору).
func (r *OutboundMessageRepository) Save(ctx context.Context, message *domain.OutboundMessage) error {
	if message == nil {
		return errors.New("postgres: сохранение nil-сообщения")
	}
	snapshot := message.Snapshot()
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("postgres: сериализация сообщения %s: %w", snapshot.ID, err)
	}
	if _, err := r.db.Exec(ctx, upsertOutboundMessage,
		snapshot.ID.String(),
		snapshot.LeadID.String(),
		string(snapshot.Status),
		string(raw),
		snapshot.CreatedAt,
		snapshot.UpdatedAt,
	); err != nil {
		return fmt.Errorf("postgres: запись сообщения %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает сообщение по идентификатору через
// domain.RehydrateOutboundMessage. Отсутствие даёт ports.ErrMessageNotFound.
func (r *OutboundMessageRepository) FindByID(ctx context.Context, id domain.OutboundMessageID) (*domain.OutboundMessage, error) {
	snapshot, err := r.loadSnapshot(ctx, id)
	if err != nil {
		return nil, err
	}
	message, err := domain.RehydrateOutboundMessage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление сообщения %s: %w", id, err)
	}
	return message, nil
}

// ListByLead возвращает историю сообщений лида, старые первыми.
func (r *OutboundMessageRepository) ListByLead(ctx context.Context, leadID domain.CompanyID) ([]*domain.OutboundMessage, error) {
	rows, err := r.db.Query(ctx, selectOutboundMessagesByLead, leadID.String())
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение истории лида %s: %w", leadID, err)
	}
	defer rows.Close()

	messages := make([]*domain.OutboundMessage, 0)
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор сообщения лида %s: %w", leadID, err)
		}
		snapshot, err := unmarshalOutboundMessageSnapshot([]byte(raw))
		if err != nil {
			return nil, fmt.Errorf("postgres: разбор снимка сообщения: %w", err)
		}
		message, err := domain.RehydrateOutboundMessage(snapshot)
		if err != nil {
			return nil, fmt.Errorf("postgres: восстановление сообщения %s: %w", snapshot.ID, err)
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение истории лида %s: %w", leadID, err)
	}
	return messages, nil
}

// SuppressedLeads возвращает идентификаторы лидов с хотя бы одним сообщением с
// подавляющим исходом. Список подавляющих исходов задаётся доменом и передаётся
// параметром, поэтому SQL не дублирует доменное правило.
func (r *OutboundMessageRepository) SuppressedLeads(ctx context.Context) ([]domain.CompanyID, error) {
	suppressing := []string{
		string(domain.MessageStatusBounced),
		string(domain.MessageStatusUnsubscribed),
		string(domain.MessageStatusRejected),
	}
	rows, err := r.db.Query(ctx, selectSuppressedLeads, suppressing)
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение подавленных лидов: %w", err)
	}
	defer rows.Close()

	leads := make([]domain.CompanyID, 0)
	for rows.Next() {
		var leadID string
		if err := rows.Scan(&leadID); err != nil {
			return nil, fmt.Errorf("postgres: разбор подавленного лида: %w", err)
		}
		leads = append(leads, domain.CompanyID(leadID))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: чтение подавленных лидов: %w", err)
	}
	return leads, nil
}

// loadSnapshot читает снимок сообщения. Отсутствие даёт ports.ErrMessageNotFound.
func (r *OutboundMessageRepository) loadSnapshot(ctx context.Context, id domain.OutboundMessageID) (domain.OutboundMessageSnapshot, error) {
	var raw string
	err := r.db.QueryRow(ctx, selectOutboundMessage, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.OutboundMessageSnapshot{}, fmt.Errorf("%w: %s", ports.ErrMessageNotFound, id)
	}
	if err != nil {
		return domain.OutboundMessageSnapshot{}, fmt.Errorf("postgres: чтение сообщения %s: %w", id, err)
	}
	snapshot, err := unmarshalOutboundMessageSnapshot([]byte(raw))
	if err != nil {
		return domain.OutboundMessageSnapshot{}, fmt.Errorf("postgres: разбор снимка сообщения %s: %w", id, err)
	}
	return snapshot, nil
}

// unmarshalOutboundMessageSnapshot разбирает JSONB-снимок сообщения.
func unmarshalOutboundMessageSnapshot(raw []byte) (domain.OutboundMessageSnapshot, error) {
	var snapshot domain.OutboundMessageSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return domain.OutboundMessageSnapshot{}, err
	}
	return snapshot, nil
}

// SQL-запросы адаптера исходящих сообщений.
const (
	upsertOutboundMessage = `
INSERT INTO "lead-management".outbound_message (id, lead_id, status, snapshot, created_at, updated_at)
VALUES ($1, $2, $3, $4::jsonb, $5, $6)
ON CONFLICT (id) DO UPDATE SET
    lead_id    = EXCLUDED.lead_id,
    status     = EXCLUDED.status,
    snapshot   = EXCLUDED.snapshot,
    created_at = EXCLUDED.created_at,
    updated_at = EXCLUDED.updated_at`

	selectOutboundMessage = `
SELECT snapshot::text
FROM "lead-management".outbound_message
WHERE id = $1`

	selectOutboundMessagesByLead = `
SELECT snapshot::text
FROM "lead-management".outbound_message
WHERE lead_id = $1
ORDER BY created_at ASC, id ASC`

	selectSuppressedLeads = `
SELECT DISTINCT lead_id
FROM "lead-management".outbound_message
WHERE status = ANY($1)
ORDER BY lead_id`
)
