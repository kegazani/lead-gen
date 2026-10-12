package postgres

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// ApprovedQueue — PostgreSQL inbox/queue approved-action, обеспечивающая
// идемпотентный приём по ключу и lease с восстановлением после рестарта.
type ApprovedQueue struct {
	pool  *pgxpool.Pool
	lease time.Duration
	poll  time.Duration
}

var _ ports.DurableCommandQueue = (*ApprovedQueue)(nil)
var _ ports.CampaignCancellationQueue = (*ApprovedQueue)(nil)
var _ ports.CampaignFenceReader = (*ApprovedQueue)(nil)

func NewApprovedQueue(pool *pgxpool.Pool, lease time.Duration) (*ApprovedQueue, error) {
	if pool == nil {
		return nil, errors.New("postgres queue: nil pool")
	}
	if lease <= 0 {
		lease = 30 * time.Second
	}
	return &ApprovedQueue{pool: pool, lease: lease, poll: 100 * time.Millisecond}, nil
}

type commandPayload struct {
	CommandID        string    `json:"command_id"`
	TenantID         string    `json:"tenant_id"`
	ApprovedActionID string    `json:"approved_action_id"`
	CampaignID       string    `json:"campaign_id,omitempty"`
	RecipientRef     string    `json:"recipient_ref"`
	Address          string    `json:"address"`
	SenderDomain     string    `json:"sender_domain"`
	Subject          string    `json:"subject"`
	Body             string    `json:"body"`
	CorrelationID    string    `json:"correlation_id"`
	CausationID      string    `json:"causation_id"`
	RequestedAt      time.Time `json:"requested_at"`
}

func encode(command domain.DeliveryCommand) (string, []byte, string, error) {
	payload := commandPayload{
		CommandID: command.CommandID.String(), TenantID: command.TenantID.String(),
		ApprovedActionID: command.ApprovedActionID.String(), CampaignID: command.CampaignID,
		RecipientRef: command.RecipientRef.String(), Address: command.Address.String(),
		SenderDomain: command.SenderDomain.String(), Subject: command.Subject, Body: command.Body,
		CorrelationID: command.CorrelationID.String(), CausationID: command.CausationID,
		RequestedAt: command.RequestedAt.UTC(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", nil, "", err
	}
	canonical := payload
	canonical.RequestedAt = time.Time{}
	canonicalRaw, err := json.Marshal(canonical)
	if err != nil {
		return "", nil, "", err
	}
	sum := sha256.Sum256(canonicalRaw)
	return command.IdempotencyKey(), raw, hex.EncodeToString(sum[:]), nil
}

// Enqueue коммитит inbox item. Replay того же payload идемпотентен, а другой
// payload под тем же ключом возвращает конфликт.
func (q *ApprovedQueue) Enqueue(ctx context.Context, command domain.DeliveryCommand) (bool, error) {
	key, raw, hash, err := encode(command)
	if err != nil {
		return false, err
	}
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("postgres queue: begin enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = lockCampaign(ctx, tx, command.TenantID.String(), command.CampaignID); err != nil {
		return false, err
	}
	var cancelled bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "message-delivery".campaign_cancellation WHERE tenant_id=$1 AND campaign_id=$2)`, command.TenantID.String(), command.CampaignID).Scan(&cancelled); err != nil {
		return false, err
	}
	if cancelled {
		return false, ports.ErrCampaignCancelled
	}
	var inserted string
	err = tx.QueryRow(ctx, `INSERT INTO "message-delivery".approved_action_inbox
		(idempotency_key, payload_hash, payload, status) VALUES ($1,$2,$3::jsonb,'pending')
		ON CONFLICT (idempotency_key) DO NOTHING RETURNING idempotency_key`, key, hash, string(raw)).Scan(&inserted)
	if err == nil {
		if err = tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("postgres queue: enqueue: %w", err)
	}
	var existingHash string
	if err := tx.QueryRow(ctx, `SELECT payload_hash FROM "message-delivery".approved_action_inbox WHERE idempotency_key=$1`, key).Scan(&existingHash); err != nil {
		return false, fmt.Errorf("postgres queue: read replay: %w", err)
	}
	if existingHash != hash {
		return false, fmt.Errorf("postgres queue: payload conflict for idempotency key %q", key)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (q *ApprovedQueue) CancelCampaign(ctx context.Context, tenantID, campaignID string) error {
	tx, err := q.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("postgres queue: begin cancellation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockCampaign(ctx, tx, tenantID, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO "message-delivery".campaign_cancellation (tenant_id,campaign_id)
		VALUES ($1,$2) ON CONFLICT (tenant_id,campaign_id) DO NOTHING`, tenantID, campaignID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE "message-delivery".approved_action_inbox
		SET status='cancelled', lease_token=NULL, lease_until=NULL
		WHERE status IN ('pending','leased') AND payload->>'tenant_id'=$1 AND payload->>'campaign_id'=$2`, tenantID, campaignID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (q *ApprovedQueue) CampaignCancelled(ctx context.Context, tenantID, campaignID string) (bool, error) {
	var cancelled bool
	err := q.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM "message-delivery".campaign_cancellation WHERE tenant_id=$1 AND campaign_id=$2)`, tenantID, campaignID).Scan(&cancelled)
	if err != nil {
		return false, fmt.Errorf("postgres queue: read cancellation fence: %w", err)
	}
	return cancelled, nil
}

func lockCampaign(ctx context.Context, tx pgx.Tx, tenantID, campaignID string) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(jsonb_build_array($1::text,$2::text)::text,0))`, tenantID, campaignID)
	if err != nil {
		return fmt.Errorf("postgres queue: lock campaign: %w", err)
	}
	return nil
}

// Claim сериализует конкурирующие sender через FOR UPDATE SKIP LOCKED и
// обновляет lease в одной SQL-операции.
func (q *ApprovedQueue) Claim(ctx context.Context) (domain.DeliveryCommand, string, bool, error) {
	for {
		var raw []byte
		var token string
		var tokenBytes [16]byte
		if _, err := rand.Read(tokenBytes[:]); err != nil {
			return domain.DeliveryCommand{}, "", false, fmt.Errorf("postgres queue: lease token: %w", err)
		}
		token = hex.EncodeToString(tokenBytes[:])
		err := q.pool.QueryRow(ctx, `WITH candidate AS (
			SELECT idempotency_key FROM "message-delivery".approved_action_inbox
			WHERE (status='pending' OR (status='leased' AND lease_until < now()))
			AND NOT EXISTS (SELECT 1 FROM "message-delivery".campaign_cancellation c
				WHERE c.tenant_id=approved_action_inbox.payload->>'tenant_id'
				AND c.campaign_id=approved_action_inbox.payload->>'campaign_id')
			ORDER BY created_at, idempotency_key LIMIT 1 FOR UPDATE SKIP LOCKED
		)
		UPDATE "message-delivery".approved_action_inbox q SET status='leased',
			lease_token=$2, lease_until=now()+$1::interval
		FROM candidate c WHERE q.idempotency_key=c.idempotency_key
		RETURNING q.payload, q.lease_token`, fmt.Sprintf("%f seconds", q.lease.Seconds()), token).Scan(&raw, &token)
		if err == nil {
			command, decodeErr := decode(raw)
			return command, token, decodeErr == nil, decodeErr
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return domain.DeliveryCommand{}, "", false, fmt.Errorf("postgres queue: claim: %w", err)
		}
		timer := time.NewTimer(q.poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return domain.DeliveryCommand{}, "", false, ctx.Err()
		case <-timer.C:
		}
	}
}

func (q *ApprovedQueue) Ack(ctx context.Context, token string) error {
	commandTag, err := q.pool.Exec(ctx, `UPDATE "message-delivery".approved_action_inbox
		SET status='acked', lease_token=NULL, lease_until=NULL, acked_at=now()
		WHERE lease_token=$1 AND status='leased'`, token)
	if err != nil {
		return fmt.Errorf("postgres queue: ack: %w", err)
	}
	if commandTag.RowsAffected() != 1 {
		return errors.New("postgres queue: lease lost before ACK")
	}
	return nil
}

func (q *ApprovedQueue) Release(ctx context.Context, token string) error {
	_, err := q.pool.Exec(ctx, `UPDATE "message-delivery".approved_action_inbox
		SET status='pending', lease_token=NULL, lease_until=NULL
		WHERE lease_token=$1 AND status='leased'`, token)
	if err != nil {
		return fmt.Errorf("postgres queue: release: %w", err)
	}
	return nil
}

func decode(raw []byte) (domain.DeliveryCommand, error) {
	var p commandPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return domain.DeliveryCommand{}, err
	}
	address, err := domain.ParseDeliveryAddress(p.Address)
	if err != nil {
		return domain.DeliveryCommand{}, err
	}
	sender, err := domain.ParseSenderDomain(p.SenderDomain)
	if err != nil {
		return domain.DeliveryCommand{}, err
	}
	return domain.DeliveryCommand{CommandID: domain.CommandID(p.CommandID), TenantID: domain.TenantID(p.TenantID),
		ApprovedActionID: domain.ApprovedActionID(p.ApprovedActionID), CampaignID: p.CampaignID,
		RecipientRef: domain.RecipientRef(p.RecipientRef), Address: address, SenderDomain: sender,
		Subject: p.Subject, Body: p.Body, CorrelationID: domain.CorrelationID(p.CorrelationID),
		CausationID: p.CausationID, RequestedAt: p.RequestedAt}, nil
}
