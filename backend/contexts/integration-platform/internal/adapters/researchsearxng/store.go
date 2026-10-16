// Package researchsearxng реализует настоящий HTTP Search API нашего SearXNG.
// Технический ledger отделён от Firecrawl account/grant: отсутствие usage/price
// proxy не может остановить разрешённый поиск.
package researchsearxng

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

type Store struct {
	pool   *pgxpool.Pool
	source string
}
type lease struct {
	id     string
	epoch  int64
	replay *contract.Receipt
}

func NewStore(ctx context.Context, pool *pgxpool.Pool, source string, concurrent, attempts int, interval time.Duration) (*Store, error) {
	if pool == nil || source == "" || concurrent < 1 || concurrent > 128 || attempts < 1 || attempts > 10 || interval < 0 {
		return nil, contract.ErrInvalid
	}
	tag, err := pool.Exec(ctx, `INSERT INTO "integration-platform".research_source_policy(source_id,max_concurrent,max_attempts,min_interval_ms) VALUES($1,$2,$3,$4)
 ON CONFLICT(source_id) DO UPDATE SET source_id=EXCLUDED.source_id WHERE "integration-platform".research_source_policy.max_concurrent=EXCLUDED.max_concurrent AND "integration-platform".research_source_policy.max_attempts=EXCLUDED.max_attempts AND "integration-platform".research_source_policy.min_interval_ms=EXCLUDED.min_interval_ms`, source, concurrent, attempts, interval.Milliseconds())
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, contract.ErrConflict
	}
	return &Store{pool: pool, source: source}, nil
}
func (s *Store) begin(ctx context.Context, a contract.Action, hash string) (lease, error) {
	var out lease
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	var concurrent, attempts int
	var interval int64
	var ready bool
	err = tx.QueryRow(ctx, `SELECT max_concurrent,max_attempts,min_interval_ms,next_allowed_at<=clock_timestamp() FROM "integration-platform".research_source_policy WHERE source_id=$1 FOR UPDATE`, s.source).Scan(&concurrent, &attempts, &interval, &ready)
	if err != nil {
		return out, err
	}
	var state, oldHash string
	var raw []byte
	var expired bool
	err = tx.QueryRow(ctx, `SELECT operation_id,lease_epoch,command_hash,state,receipt,lease_until<=clock_timestamp() FROM "integration-platform".research_source_attempt WHERE source_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND action_id=$4 AND attempt_no=$5 FOR UPDATE`, s.source, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID, a.Attempt).Scan(&out.id, &out.epoch, &oldHash, &state, &raw, &expired)
	if err == nil {
		if oldHash != hash {
			return out, contract.ErrConflict
		}
		if len(raw) > 0 {
			var r contract.Receipt
			if err = json.Unmarshal(raw, &r); err != nil {
				return out, err
			}
			r.Persistence = "persisted"
			out.replay = &r
			return out, tx.Commit(ctx)
		}
		if state == "running" && !expired {
			return out, contract.ErrInFlight
		}
		if state == "running" {
			if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_attempt SET state='unknown',lease_epoch=lease_epoch+1 WHERE operation_id=$1`, out.id); err != nil {
				return out, err
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		return out, contract.ErrUnknown
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	if a.Attempt > attempts {
		return out, contract.ErrAttemptLimit
	}
	// Новый read-only retry не получает независимый техлимит: число попыток
	// включает все сохранённые исходы того же scoped action.
	var count, active int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM "integration-platform".research_source_attempt WHERE source_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND action_id=$4`, s.source, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID).Scan(&count); err != nil {
		return out, err
	}
	if count >= attempts {
		return out, contract.ErrAttemptLimit
	}
	var actionActive bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "integration-platform".research_source_attempt WHERE source_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND action_id=$4 AND state='running' AND lease_until>clock_timestamp())`, s.source, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID).Scan(&actionActive); err != nil {
		return out, err
	}
	if actionActive {
		return out, contract.ErrInFlight
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM "integration-platform".research_source_attempt WHERE source_id=$1 AND state='running' AND lease_until>clock_timestamp()`, s.source).Scan(&active); err != nil {
		return out, err
	}
	if active >= concurrent || !ready {
		return out, contract.ErrThrottled
	}
	// Fencing истёкших read-only workers до следующего attempt. Их поздний
	// ответ можно сохранить отдельно, но он не становится текущим receipt.
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_attempt SET state='unknown',lease_epoch=lease_epoch+1 WHERE source_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND action_id=$4 AND state='running' AND lease_until<=clock_timestamp()`, s.source, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID); err != nil {
		return out, err
	}
	out.id = hashValue(struct {
		Source  string
		Scope   contract.Scope
		Action  string
		Attempt int
	}{s.source, a.Scope, a.ActionID, a.Attempt})
	out.epoch = 1
	if _, err = tx.Exec(ctx, `INSERT INTO "integration-platform".research_source_attempt(operation_id,source_id,tenant_id,purpose_id,action_id,attempt_no,command_hash,state,lease_until) VALUES($1,$2,$3,$4,$5,$6,$7,'running',clock_timestamp()+($8 * interval '1 second'))`, out.id, s.source, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID, a.Attempt, hash, a.Limits.TimeoutSeconds+30); err != nil {
		return out, err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_policy SET next_allowed_at=clock_timestamp()+($2 * interval '1 millisecond') WHERE source_id=$1`, s.source, interval); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s *Store) finish(ctx context.Context, a contract.Action, l lease, r contract.Receipt) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, `SELECT source_id FROM "integration-platform".research_source_policy WHERE source_id=$1 FOR UPDATE`, s.source); err != nil {
		return err
	}
	var epoch int64
	var oldHash *string
	if err = tx.QueryRow(ctx, `SELECT lease_epoch,receipt_hash FROM "integration-platform".research_source_attempt WHERE operation_id=$1 AND source_id=$2 AND tenant_id=$3 AND purpose_id=$4 FOR UPDATE`, l.id, s.source, a.Scope.TenantID, a.Scope.PurposeID).Scan(&epoch, &oldHash); err != nil {
		return notFound(err)
	}
	if oldHash != nil {
		if *oldHash != r.SHA256 {
			return contract.ErrConflict
		}
		return tx.Commit(ctx)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if epoch != l.epoch {
		if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_attempt SET late_receipt=COALESCE(late_receipt,$2::jsonb) WHERE operation_id=$1`, l.id, raw); err != nil {
			return err
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		return contract.ErrConflict
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_attempt SET state='completed',receipt=$2,receipt_hash=$3,completed_at=clock_timestamp() WHERE operation_id=$1`, l.id, raw, r.SHA256); err != nil {
		return err
	}
	if r.RetryAfterSeconds > 0 {
		if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_source_policy SET next_allowed_at=GREATEST(next_allowed_at,clock_timestamp()+($2 * interval '1 second')) WHERE source_id=$1`, s.source, r.RetryAfterSeconds); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
func (s *Store) Lookup(ctx context.Context, scope contract.Scope, actionID string, attempt int) (contract.Receipt, error) {
	var out contract.Receipt
	if scope.Validate() != nil || actionID == "" || attempt < 1 {
		return out, contract.ErrInvalid
	}
	var raw []byte
	var state string
	var expired bool
	err := s.pool.QueryRow(ctx, `SELECT operation_id,state,receipt,lease_until<=clock_timestamp() FROM "integration-platform".research_source_attempt WHERE source_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND action_id=$4 AND attempt_no=$5`, s.source, scope.TenantID, scope.PurposeID, actionID, attempt).Scan(&out.OperationID, &state, &raw, &expired)
	if err != nil {
		return out, notFound(err)
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
		out.Persistence = "persisted"
		return out, receiptError(out)
	}
	out.Scope = scope
	out.ActionID = actionID
	out.Attempt = attempt
	out.Provider = "searxng"
	out.CostState = "not_reported"
	if state == "running" && !expired {
		return out, contract.ErrInFlight
	}
	out.Status = "unknown"
	out.ErrorCode = "read_outcome_unknown"
	// Исход выведен из durable lease, payload receipt отсутствует (SHA256 пустой).
	// Для read-only источника это не финансовый unknown и не новый HTTP retry.
	out.Persistence = "persisted"
	return out, contract.ErrUnknown
}
func hashValue(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.ErrNotFound
	}
	return err
}
