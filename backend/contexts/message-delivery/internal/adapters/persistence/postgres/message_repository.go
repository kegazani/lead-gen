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

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Messages — PostgreSQL-реализация ports.MessageRepository и
// ports.RetryableMessages.
type Messages struct {
	db execer
}

var (
	_ ports.MessageRepository = (*Messages)(nil)
	_ ports.RetryableMessages = (*Messages)(nil)
)

// NewMessageRepository создаёт репозиторий поверх пула соединений.
func NewMessageRepository(pool *pgxpool.Pool) *Messages {
	if pool == nil {
		panic("postgres: NewMessageRepository с nil-пулом")
	}
	return &Messages{db: pool}
}

// NewMessageRepositoryTx создаёт репозиторий, привязанный к активной транзакции:
// commit и rollback остаются за unit-of-work.
func NewMessageRepositoryTx(tx pgx.Tx) *Messages {
	if tx == nil {
		panic("postgres: NewMessageRepositoryTx с nil-транзакцией")
	}
	return &Messages{db: tx}
}

// Save сохраняет сообщение целиком как снимок (upsert по идентификатору).
// Нарушение уникальности ключа идемпотентности (другое сообщение с тем же
// ключом) отвергается, а не затирает чужую строку.
func (r *Messages) Save(ctx context.Context, message *domain.Message) error {
	if message == nil {
		return errors.New("postgres: сохранение nil-сообщения")
	}
	snapshot := message.Snapshot()
	raw, err := marshalMessage(snapshot)
	if err != nil {
		return err
	}

	lastOutcome, _ := message.LastOutcome()
	_, err = r.db.Exec(ctx, upsertMessage,
		snapshot.ID.String(),
		snapshot.TenantID.String(),
		snapshot.ApprovedActionID.String(),
		snapshot.IdempotencyKey,
		snapshot.SenderDomain.String(),
		string(snapshot.Status),
		len(snapshot.Attempts),
		string(lastOutcome),
		snapshot.CreatedAt,
		string(raw),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: ключ идемпотентности %q занят", errIdempotencyKeyTaken, snapshot.IdempotencyKey)
		}
		return fmt.Errorf("postgres: запись сообщения %s: %w", snapshot.ID, err)
	}
	return nil
}

// FindByID загружает сообщение по идентификатору через domain.RehydrateMessage.
// Отсутствие даёт ошибку, для которой истинно errors.Is(err,
// ports.ErrMessageNotFound).
func (r *Messages) FindByID(ctx context.Context, id domain.MessageID) (*domain.Message, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, selectMessageByID, id.String()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: %s", ports.ErrMessageNotFound, id)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение сообщения %s: %w", id, err)
	}
	return rehydrateMessage(raw)
}

// FindByIdempotencyKey загружает сообщение по ключу идемпотентности. Отсутствие
// даёт портовую ошибку.
func (r *Messages) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Message, error) {
	var raw []byte
	err := r.db.QueryRow(ctx, selectMessageByKey, key).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("%w: ключ %q", ports.ErrMessageNotFound, key)
	}
	if err != nil {
		return nil, fmt.Errorf("postgres: чтение сообщения по ключу: %w", err)
	}
	return rehydrateMessage(raw)
}

// ListRetryable реализует ports.RetryableMessages: сообщения с временным исходом
// последней попытки, у которых число попыток меньше maxAttempts, в порядке
// создания. limit > 0 ограничивает порцию.
func (r *Messages) ListRetryable(ctx context.Context, maxAttempts, limit int) ([]*domain.Message, error) {
	var lim any
	if limit > 0 {
		lim = limit
	}
	var max any
	if maxAttempts > 0 {
		max = maxAttempts
	}
	rows, err := r.db.Query(ctx, selectRetryableMessages, max, lim)
	if err != nil {
		return nil, fmt.Errorf("postgres: выбор сообщений для повтора: %w", err)
	}
	defer rows.Close()

	messages := make([]*domain.Message, 0)
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("postgres: разбор сообщения для повтора: %w", err)
		}
		message, err := rehydrateMessage(raw)
		if err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: выбор сообщений для повтора: %w", err)
	}
	return messages, nil
}

// rehydrateMessage разбирает сохранённый снимок и восстанавливает агрегат.
func rehydrateMessage(raw []byte) (*domain.Message, error) {
	snapshot, err := unmarshalMessage(raw)
	if err != nil {
		return nil, err
	}
	message, err := domain.RehydrateMessage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("postgres: восстановление сообщения %s: %w", snapshot.ID, err)
	}
	return message, nil
}

// errIdempotencyKeyTaken — нарушение уникальности ключа идемпотентности.
var errIdempotencyKeyTaken = errors.New("postgres: ключ идемпотентности занят")

// SQL-запросы адаптера сообщений.
const (
	upsertMessage = `
INSERT INTO "message-delivery".message (
    id, tenant_id, approved_action_id, idempotency_key, sender_domain,
    status, attempts_count, last_outcome, created_at, updated_at, snapshot
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now(), $10::jsonb)
ON CONFLICT (id) DO UPDATE SET
    tenant_id          = EXCLUDED.tenant_id,
    approved_action_id = EXCLUDED.approved_action_id,
    idempotency_key    = EXCLUDED.idempotency_key,
    sender_domain      = EXCLUDED.sender_domain,
    status             = EXCLUDED.status,
    attempts_count     = EXCLUDED.attempts_count,
    last_outcome       = EXCLUDED.last_outcome,
    created_at         = EXCLUDED.created_at,
    updated_at         = now(),
    snapshot           = EXCLUDED.snapshot`

	selectMessageByID = `
SELECT snapshot
FROM "message-delivery".message
WHERE id = $1`

	selectMessageByKey = `
SELECT snapshot
FROM "message-delivery".message
WHERE idempotency_key = $1`

	selectRetryableMessages = `
SELECT snapshot
FROM "message-delivery".message
WHERE status = 'failed'
  AND last_outcome = 'temporary'
  AND ($1::int IS NULL OR attempts_count < $1)
ORDER BY created_at ASC, id ASC
LIMIT $2`
)

// messageRecord — адаптерная форма снимка сообщения: value objects раскрыты в
// строки, потому что у DeliveryAddress и SenderDomain нет экспортируемых полей.
type messageRecord struct {
	ID               string          `json:"id"`
	TenantID         string          `json:"tenant_id"`
	ApprovedActionID string          `json:"approved_action_id"`
	RecipientRef     string          `json:"recipient_ref"`
	Address          string          `json:"address"`
	SenderDomain     string          `json:"sender_domain"`
	Subject          string          `json:"subject"`
	Body             string          `json:"body"`
	IdempotencyKey   string          `json:"idempotency_key"`
	Status           string          `json:"status"`
	Attempts         []attemptRecord `json:"attempts"`
	CreatedAt        time.Time       `json:"created_at"`
}

type attemptRecord struct {
	Number       int       `json:"number"`
	Outcome      string    `json:"outcome"`
	ProviderCode string    `json:"provider_code"`
	At           time.Time `json:"at"`
}

// marshalMessage кодирует снимок в адаптерную форму.
func marshalMessage(snapshot domain.MessageSnapshot) ([]byte, error) {
	record := messageRecord{
		ID:               snapshot.ID.String(),
		TenantID:         snapshot.TenantID.String(),
		ApprovedActionID: snapshot.ApprovedActionID.String(),
		RecipientRef:     snapshot.RecipientRef.String(),
		Address:          snapshot.Address.String(),
		SenderDomain:     snapshot.SenderDomain.String(),
		Subject:          snapshot.Subject,
		Body:             snapshot.Body,
		IdempotencyKey:   snapshot.IdempotencyKey,
		Status:           string(snapshot.Status),
		Attempts:         make([]attemptRecord, 0, len(snapshot.Attempts)),
		CreatedAt:        snapshot.CreatedAt,
	}
	for _, attempt := range snapshot.Attempts {
		record.Attempts = append(record.Attempts, attemptRecord{
			Number:       attempt.Number,
			Outcome:      string(attempt.Outcome),
			ProviderCode: attempt.ProviderCode,
			At:           attempt.At,
		})
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("postgres: сериализация сообщения %s: %w", snapshot.ID, err)
	}
	return raw, nil
}

// unmarshalMessage разбирает адаптерную форму в доменный снимок, разбирая
// value objects конструкторами.
func unmarshalMessage(raw []byte) (domain.MessageSnapshot, error) {
	var record messageRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return domain.MessageSnapshot{}, fmt.Errorf("postgres: разбор снимка сообщения: %w", err)
	}
	address, err := domain.ParseDeliveryAddress(record.Address)
	if err != nil {
		return domain.MessageSnapshot{}, fmt.Errorf("postgres: разбор адреса сообщения %s: %w", record.ID, err)
	}
	sender, err := domain.ParseSenderDomain(record.SenderDomain)
	if err != nil {
		return domain.MessageSnapshot{}, fmt.Errorf("postgres: разбор домена сообщения %s: %w", record.ID, err)
	}
	attempts := make([]domain.DeliveryAttempt, 0, len(record.Attempts))
	for _, attempt := range record.Attempts {
		attempts = append(attempts, domain.DeliveryAttempt{
			Number:       attempt.Number,
			Outcome:      domain.AttemptOutcome(attempt.Outcome),
			ProviderCode: attempt.ProviderCode,
			At:           attempt.At,
		})
	}
	return domain.MessageSnapshot{
		ID:               domain.MessageID(record.ID),
		TenantID:         domain.TenantID(record.TenantID),
		ApprovedActionID: domain.ApprovedActionID(record.ApprovedActionID),
		RecipientRef:     domain.RecipientRef(record.RecipientRef),
		Address:          address,
		SenderDomain:     sender,
		Subject:          record.Subject,
		Body:             record.Body,
		IdempotencyKey:   record.IdempotencyKey,
		Status:           domain.MessageStatus(record.Status),
		Attempts:         attempts,
		CreatedAt:        record.CreatedAt,
	}, nil
}
