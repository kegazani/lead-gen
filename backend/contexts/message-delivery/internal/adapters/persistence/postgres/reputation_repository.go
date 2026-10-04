package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Reputations — PostgreSQL-реализация ports.SenderReputationRepository.
type Reputations struct {
	db execer
}

// Reputations реализует порт репозитория репутаций.
var _ ports.SenderReputationRepository = (*Reputations)(nil)

// NewSenderReputationRepository создаёт репозиторий поверх пула соединений.
func NewSenderReputationRepository(pool *pgxpool.Pool) *Reputations {
	if pool == nil {
		panic("postgres: NewSenderReputationRepository с nil-пулом")
	}
	return &Reputations{db: pool}
}

// NewSenderReputationRepositoryTx создаёт репозиторий, привязанный к активной
// транзакции.
func NewSenderReputationRepositoryTx(tx pgx.Tx) *Reputations {
	if tx == nil {
		panic("postgres: NewSenderReputationRepositoryTx с nil-транзакцией")
	}
	return &Reputations{db: tx}
}

// Save сохраняет репутацию домена как снимок (upsert по домену).
func (r *Reputations) Save(ctx context.Context, reputation *domain.SenderReputation) error {
	if reputation == nil {
		return errors.New("postgres: сохранение nil-репутации")
	}
	snapshot := reputation.Snapshot()
	_, err := r.db.Exec(ctx, upsertReputation,
		snapshot.Domain.String(),
		snapshot.Delivered,
		snapshot.HardBounces,
		snapshot.SoftBounces,
		snapshot.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("postgres: запись репутации домена %s: %w", snapshot.Domain, err)
	}
	return nil
}

// FindByDomain загружает репутацию по отправляющему домену через
// domain.RehydrateSenderReputation. Отсутствие даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrSenderReputationNotFound).
func (r *Reputations) FindByDomain(ctx context.Context, sender domain.SenderDomain) (*domain.SenderReputation, error) {
	var (
		delivered   int
		hardBounces int
		softBounces int
		updatedAt   time.Time
	)
	err := r.db.QueryRow(ctx, selectReputation, sender.String()).
		Scan(&delivered, &hardBounces, &softBounces, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrSenderReputationNotFound, sender)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение репутации домена %s: %w", sender, err)
	}
	reputation, err := domain.RehydrateSenderReputation(domain.SenderReputationSnapshot{
		Domain:      sender,
		Delivered:   delivered,
		HardBounces: hardBounces,
		SoftBounces: softBounces,
		UpdatedAt:   updatedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление репутации %s: %w", sender, err)
	}
	return reputation, nil
}

// SQL-запросы адаптера репутации.
const (
	upsertReputation = `
INSERT INTO "message-delivery".sender_reputation (domain, delivered, hard_bounces, soft_bounces, updated_at)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (domain) DO UPDATE SET
    delivered    = EXCLUDED.delivered,
    hard_bounces = EXCLUDED.hard_bounces,
    soft_bounces = EXCLUDED.soft_bounces,
    updated_at   = EXCLUDED.updated_at`

	selectReputation = `
SELECT delivered, hard_bounces, soft_bounces, updated_at
FROM "message-delivery".sender_reputation
WHERE domain = $1`
)
