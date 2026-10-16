// Package researchacquisition реализует клиентский durable ledger получения
// данных. Денежные строки принадлежат исключительно Firecrawl; строки попыток
// одновременно являются единственным frontier ограниченных Search/Fetch.
package researchacquisition

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

type Store struct {
	pool          *pgxpool.Pool
	maxConcurrent int
	minInterval   time.Duration
}

func NewStore(pool *pgxpool.Pool, concurrency int, interval time.Duration) *Store {
	return &Store{pool: pool, maxConcurrent: concurrency, minInterval: interval}
}

var _ contract.Ledger = (*Store)(nil)

func (s *Store) EnsureAccount(ctx context.Context, p contract.AccountPolicy) error {
	if p.AccountID == "" || p.MaxCredits < 0 || s.maxConcurrent < 1 || s.minInterval < 0 {
		return contract.ErrInvalid
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO "integration-platform".research_fc_account(account_id,max_credits,max_concurrent,min_interval_ms)
 VALUES($1,$2,$3,$4) ON CONFLICT(account_id) DO UPDATE SET account_id=EXCLUDED.account_id
 WHERE "integration-platform".research_fc_account.max_credits=EXCLUDED.max_credits
 AND "integration-platform".research_fc_account.max_concurrent=EXCLUDED.max_concurrent
 AND "integration-platform".research_fc_account.min_interval_ms=EXCLUDED.min_interval_ms`, p.AccountID, p.MaxCredits, s.maxConcurrent, s.minInterval.Milliseconds())
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return contract.ErrConflict
	}
	return nil
}
func (s *Store) EnsureGrant(ctx context.Context, g contract.Grant) error {
	if err := g.Validate(); err != nil {
		return writeError(err)
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO "integration-platform".research_fc_grant(grant_id,account_id,tenant_id,purpose_id,intent_id,intent_kind,max_credits)
 VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(grant_id) DO UPDATE SET grant_id=EXCLUDED.grant_id
 WHERE "integration-platform".research_fc_grant.account_id=EXCLUDED.account_id
 AND "integration-platform".research_fc_grant.tenant_id=EXCLUDED.tenant_id
 AND "integration-platform".research_fc_grant.purpose_id=EXCLUDED.purpose_id
 AND "integration-platform".research_fc_grant.intent_id=EXCLUDED.intent_id
 AND "integration-platform".research_fc_grant.intent_kind=EXCLUDED.intent_kind
 AND "integration-platform".research_fc_grant.max_credits=EXCLUDED.max_credits`, g.ID, g.AccountID, g.Scope.TenantID, g.Scope.PurposeID, g.IntentID, g.IntentKind, g.MaxCredits)
	if err != nil {
		return writeError(err)
	}
	if tag.RowsAffected() != 1 {
		return contract.ErrConflict
	}
	return nil
}
func (s *Store) Snapshot(ctx context.Context, scope contract.Scope, id string) (contract.GrantSnapshot, error) {
	var out contract.GrantSnapshot
	if scope.Validate() != nil || id == "" {
		return out, contract.ErrInvalid
	}
	err := s.pool.QueryRow(ctx, `SELECT g.grant_id,g.account_id,g.intent_id,g.intent_kind,g.max_credits,g.spent_credits,g.reserved_credits,g.unknown_credits,
 a.max_credits,a.spent_credits,a.reserved_credits,a.unknown_credits FROM "integration-platform".research_fc_grant g
 JOIN "integration-platform".research_fc_account a USING(account_id) WHERE g.grant_id=$1 AND g.tenant_id=$2 AND g.purpose_id=$3`, id, scope.TenantID, scope.PurposeID).Scan(&out.Grant.ID, &out.Grant.AccountID, &out.Grant.IntentID, &out.Grant.IntentKind, &out.Allocation.MaxCredits, &out.Allocation.SpentCredits, &out.Allocation.ReservedCredits, &out.Allocation.UnknownCredits, &out.Account.MaxCredits, &out.Account.SpentCredits, &out.Account.ReservedCredits, &out.Account.UnknownCredits)
	out.Grant.Scope = scope
	out.Grant.MaxCredits = out.Allocation.MaxCredits
	return out, notFound(err)
}

type reservation struct {
	id      string
	epoch   int64
	credits int
	replay  *contract.Receipt
}

// begin атомарно резервирует одновременно account и grant. Сеть запускается
// только после commit. Повтор не может изменить request, scope или grant.
func (s *Store) begin(ctx context.Context, a contract.Action, kind, hash string, credits int) (reservation, error) {
	var out reservation
	if a.GrantID == "" {
		return out, contract.ErrInvalid
	}
	if err := a.Validate(); err != nil {
		return out, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	var account string
	err = tx.QueryRow(ctx, `SELECT account_id FROM "integration-platform".research_fc_grant WHERE grant_id=$1 AND tenant_id=$2 AND purpose_id=$3`, a.GrantID, a.Scope.TenantID, a.Scope.PurposeID).Scan(&account)
	if err != nil {
		return out, notFound(err)
	}
	var ac, asp, ar, gc, gsp, gr, maxConcurrent int
	var stopped bool
	var next time.Time
	var minInterval int64
	err = tx.QueryRow(ctx, `SELECT max_credits,spent_credits,reserved_credits,stopped,max_concurrent,min_interval_ms,next_allowed_at FROM "integration-platform".research_fc_account WHERE account_id=$1 FOR UPDATE`, account).Scan(&ac, &asp, &ar, &stopped, &maxConcurrent, &minInterval, &next)
	if err != nil {
		return out, err
	}
	// Время берётся из PostgreSQL: процесс с неверными часами не расширяет lease.
	var now time.Time
	if err = tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return out, err
	}
	var state, oldHash, oldGrant string
	var receipt []byte
	var lease time.Time
	err = tx.QueryRow(ctx, `SELECT operation_id,lease_epoch,reserved_credits,command_hash,grant_id,state,lease_until,receipt
 FROM "integration-platform".research_acquisition_attempt WHERE tenant_id=$1 AND purpose_id=$2 AND action_id=$3 AND attempt_no=$4 FOR UPDATE`, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID, a.Attempt).Scan(&out.id, &out.epoch, &out.credits, &oldHash, &oldGrant, &state, &lease, &receipt)
	if err == nil {
		if oldHash != hash || oldGrant != a.GrantID {
			return out, contract.ErrConflict
		}
		if len(receipt) > 0 {
			var r contract.Receipt
			if err = json.Unmarshal(receipt, &r); err != nil {
				return out, err
			}
			r.Persistence = "persisted"
			out.replay = &r
			return out, tx.Commit(ctx)
		}
		if state == "running" && now.Before(lease) {
			return out, contract.ErrInFlight
		}
		// Истёкший lease не доказывает, что HTTP не выполнялся. Резерв удерживается,
		// новый worker не отправляет дублирующий POST.
		if state == "running" {
			if err = s.markExpired(ctx, tx, account, a.GrantID, out.id, out.credits); err != nil {
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
	var uncertain bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "integration-platform".research_acquisition_attempt WHERE tenant_id=$1 AND purpose_id=$2 AND action_id=$3 AND (state='unknown' OR state='running'))`, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID).Scan(&uncertain); err != nil {
		return out, err
	}
	if uncertain {
		return out, contract.ErrUnknown
	}
	err = tx.QueryRow(ctx, `SELECT max_credits,spent_credits,reserved_credits FROM "integration-platform".research_fc_grant WHERE grant_id=$1 FOR UPDATE`, a.GrantID).Scan(&gc, &gsp, &gr)
	if err != nil {
		return out, err
	}
	if stopped || (ac > 0 && credits > ac-asp) || (gc > 0 && credits > gc-gsp) {
		return out, contract.ErrBudgetExhausted
	}
	if (ac > 0 && credits > ac-asp-ar) || (gc > 0 && credits > gc-gsp-gr) {
		// Резерв конкурирующей операции ещё может освободиться: это ожидание,
		// а не доказательство окончательного исчерпания account или grant.
		return out, contract.ErrThrottled
	}
	var active int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM "integration-platform".research_acquisition_attempt WHERE account_id=$1 AND state='running' AND lease_until>clock_timestamp()`, account).Scan(&active); err != nil {
		return out, err
	}
	if active >= maxConcurrent || now.Before(next) {
		return out, contract.ErrThrottled
	}
	out.id = commandHash(struct {
		Scope   contract.Scope
		Action  string
		Attempt int
	}{a.Scope, a.ActionID, a.Attempt})
	out.epoch = 1
	out.credits = credits
	_, err = tx.Exec(ctx, `INSERT INTO "integration-platform".research_acquisition_attempt(operation_id,account_id,grant_id,tenant_id,purpose_id,action_id,attempt_no,command_hash,operation_kind,state,reserved_credits,lease_until)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'running',$10,clock_timestamp()+($11 * interval '1 second'))`, out.id, account, a.GrantID, a.Scope.TenantID, a.Scope.PurposeID, a.ActionID, a.Attempt, hash, kind, credits, a.Limits.TimeoutSeconds+30)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_account SET reserved_credits=reserved_credits+$2,next_allowed_at=clock_timestamp()+($3 * interval '1 millisecond') WHERE account_id=$1`, account, credits, minInterval)
	if err != nil {
		return out, err
	}
	_, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_grant SET reserved_credits=reserved_credits+$2 WHERE grant_id=$1`, a.GrantID, credits)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// admitProvider сериализует решение по уже прочитанному балансу без сети в TX.
// Одновременные pending probes не вычитают друг друга: первый допущенный
// резерв становится виден следующему. Unknown всегда удерживает свою долю.
func (s *Store) admitProvider(ctx context.Context, a contract.Action, res reservation, remaining, spentBeforeProbe int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var account string
	var spent int
	var stopped bool
	err = tx.QueryRow(ctx, `SELECT a.account_id,a.spent_credits,a.stopped FROM "integration-platform".research_fc_account a
 JOIN "integration-platform".research_fc_grant g USING(account_id)
 WHERE g.grant_id=$1 AND g.tenant_id=$2 AND g.purpose_id=$3 FOR UPDATE OF a`, a.GrantID, a.Scope.TenantID, a.Scope.PurposeID).Scan(&account, &spent, &stopped)
	if err != nil {
		return notFound(err)
	}
	var epoch int64
	var state string
	var admitted, expired bool
	err = tx.QueryRow(ctx, `SELECT lease_epoch,state,provider_admitted,lease_until<=clock_timestamp()
 FROM "integration-platform".research_acquisition_attempt WHERE operation_id=$1 AND grant_id=$2 AND tenant_id=$3 AND purpose_id=$4 FOR UPDATE`, res.id, a.GrantID, a.Scope.TenantID, a.Scope.PurposeID).Scan(&epoch, &state, &admitted, &expired)
	if err != nil {
		return notFound(err)
	}
	if epoch != res.epoch || admitted || state != "running" || expired {
		return contract.ErrConflict
	}
	if stopped || remaining < res.credits {
		return contract.ErrBudgetExhausted
	}
	var held int
	err = tx.QueryRow(ctx, `SELECT COALESCE(sum(reserved_credits),0) FROM "integration-platform".research_acquisition_attempt
 WHERE account_id=$1 AND operation_id<>$2 AND (state='unknown' OR (state='running' AND provider_admitted))`, account, res.id).Scan(&held)
	if err != nil {
		return err
	}
	if remaining-held-(spent-spentBeforeProbe) < res.credits {
		return contract.ErrThrottled
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_acquisition_attempt SET provider_admitted=true WHERE operation_id=$1`, res.id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) markExpired(ctx context.Context, tx pgx.Tx, account, grant, id string, credits int) error {
	_, err := tx.Exec(ctx, `UPDATE "integration-platform".research_acquisition_attempt SET state='unknown' WHERE operation_id=$1`, id)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_account SET unknown_credits=unknown_credits+$2 WHERE account_id=$1`, account, credits); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_grant SET unknown_credits=unknown_credits+$2 WHERE grant_id=$1`, grant, credits)
	return err
}

// finish сохраняет итог и финансовый переход в одной транзакции. Неизвестный
// outcome держит полный reserve; actual может быть выше оценки и тогда расход
// всё равно фиксируется, следующие admissions остановятся.
func (s *Store) finish(ctx context.Context, a contract.Action, res reservation, r contract.Receipt, unknown, stopAccount bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var account string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM "integration-platform".research_fc_grant WHERE grant_id=$1 AND tenant_id=$2 AND purpose_id=$3`, a.GrantID, a.Scope.TenantID, a.Scope.PurposeID).Scan(&account); err != nil {
		return notFound(err)
	}
	if _, err = tx.Exec(ctx, `SELECT account_id FROM "integration-platform".research_fc_account WHERE account_id=$1 FOR UPDATE`, account); err != nil {
		return err
	}
	var state string
	var epoch int64
	var oldHash *string
	if err = tx.QueryRow(ctx, `SELECT state,lease_epoch,receipt_hash FROM "integration-platform".research_acquisition_attempt WHERE operation_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND grant_id=$4 FOR UPDATE`, res.id, a.Scope.TenantID, a.Scope.PurposeID, a.GrantID).Scan(&state, &epoch, &oldHash); err != nil {
		return notFound(err)
	}
	if oldHash != nil {
		if *oldHash != r.SHA256 {
			return contract.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if epoch != res.epoch {
		return contract.ErrConflict
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	nextState := "completed"
	reservedDelta := -res.credits
	unknownDelta := 0
	if state == "unknown" {
		unknownDelta = -res.credits
	}
	if unknown {
		nextState = "unknown"
		reservedDelta = 0
		unknownDelta += res.credits
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_account SET spent_credits=spent_credits+$2,reserved_credits=reserved_credits+$3,unknown_credits=unknown_credits+$4,stopped=stopped OR $5,next_allowed_at=GREATEST(next_allowed_at,clock_timestamp()+($6 * interval '1 second')) WHERE account_id=$1`, account, r.CreditsCharged, reservedDelta, unknownDelta, stopAccount, r.RetryAfterSeconds); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_grant SET spent_credits=spent_credits+$2,reserved_credits=reserved_credits+$3,unknown_credits=unknown_credits+$4 WHERE grant_id=$1`, a.GrantID, r.CreditsCharged, reservedDelta, unknownDelta); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_acquisition_attempt SET state=$2,charged_credits=$3,receipt=$4,receipt_hash=$5,completed_at=clock_timestamp() WHERE operation_id=$1`, res.id, nextState, r.CreditsCharged, raw, r.SHA256); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Reconcile принимает точное evidence конкретной операции или аудированное
// решение оператора. Разница общего account balance не является таким evidence.
func (s *Store) Reconcile(ctx context.Context, r contract.Reconciliation) error {
	if r.Scope.Validate() != nil || r.GrantID == "" || r.OperationID == "" || r.ActualCredits < 0 || strings.TrimSpace(r.EvidenceRef) == "" || strings.TrimSpace(r.Actor) == "" {
		return contract.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	var account string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM "integration-platform".research_fc_grant WHERE grant_id=$1 AND tenant_id=$2 AND purpose_id=$3`, r.GrantID, r.Scope.TenantID, r.Scope.PurposeID).Scan(&account); err != nil {
		return notFound(err)
	}
	if _, err = tx.Exec(ctx, `SELECT account_id FROM "integration-platform".research_fc_account WHERE account_id=$1 FOR UPDATE`, account); err != nil {
		return err
	}
	var owned bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM "integration-platform".research_acquisition_attempt WHERE operation_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND grant_id=$4)`, r.OperationID, r.Scope.TenantID, r.Scope.PurposeID, r.GrantID).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return contract.ErrNotFound
	}
	var actual int
	var evidence, actor string
	err = tx.QueryRow(ctx, `SELECT actual_credits,evidence_ref,actor FROM "integration-platform".research_fc_reconciliation WHERE operation_id=$1`, r.OperationID).Scan(&actual, &evidence, &actor)
	if err == nil {
		if actual != r.ActualCredits || evidence != r.EvidenceRef || actor != r.Actor {
			return contract.ErrConflict
		}
		return tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var state string
	var reserve int
	if err = tx.QueryRow(ctx, `SELECT state,reserved_credits FROM "integration-platform".research_acquisition_attempt WHERE operation_id=$1 AND tenant_id=$2 AND purpose_id=$3 AND grant_id=$4 FOR UPDATE`, r.OperationID, r.Scope.TenantID, r.Scope.PurposeID, r.GrantID).Scan(&state, &reserve); err != nil {
		return notFound(err)
	}
	if state != "unknown" {
		return fmt.Errorf("%w: reconciliation разрешён только unknown", contract.ErrConflict)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO "integration-platform".research_fc_reconciliation(operation_id,actual_credits,evidence_ref,actor) VALUES($1,$2,$3,$4)`, r.OperationID, r.ActualCredits, r.EvidenceRef, r.Actor); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_account SET spent_credits=spent_credits+$2,reserved_credits=reserved_credits-$3,unknown_credits=unknown_credits-$3 WHERE account_id=$1`, account, r.ActualCredits, reserve); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_fc_grant SET spent_credits=spent_credits+$2,reserved_credits=reserved_credits-$3,unknown_credits=unknown_credits-$3 WHERE grant_id=$1`, r.GrantID, r.ActualCredits, reserve); err != nil {
		return err
	}
	// Исходный receipt неизменяем: reconciliation — отдельный финансовый факт,
	// отсутствие результата страницы не превращается в успех получения.
	if _, err = tx.Exec(ctx, `UPDATE "integration-platform".research_acquisition_attempt SET state='completed',charged_credits=$2,lease_epoch=lease_epoch+1 WHERE operation_id=$1`, r.OperationID, r.ActualCredits); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.ErrNotFound
	}
	return err
}
func commandHash(v any) string {
	raw, _ := json.Marshal(v)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// Lookup читает immutable receipt; при истёкшем lease только помечает
// неопределённость. Он никогда не вызывает Firecrawl.
func (s *Store) Lookup(ctx context.Context, scope contract.Scope, actionID string, attempt int) (contract.Receipt, error) {
	var out contract.Receipt
	if scope.Validate() != nil || actionID == "" || attempt < 1 {
		return out, contract.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(context.Background())
	var account string
	if err = tx.QueryRow(ctx, `SELECT account_id FROM "integration-platform".research_acquisition_attempt WHERE tenant_id=$1 AND purpose_id=$2 AND action_id=$3 AND attempt_no=$4`, scope.TenantID, scope.PurposeID, actionID, attempt).Scan(&account); err != nil {
		return out, notFound(err)
	}
	if _, err = tx.Exec(ctx, `SELECT account_id FROM "integration-platform".research_fc_account WHERE account_id=$1 FOR UPDATE`, account); err != nil {
		return out, err
	}
	var raw []byte
	var state, grant string
	var expired bool
	var credits int
	if err = tx.QueryRow(ctx, `SELECT operation_id,grant_id,state,receipt,reserved_credits,lease_until<=clock_timestamp() FROM "integration-platform".research_acquisition_attempt WHERE tenant_id=$1 AND purpose_id=$2 AND action_id=$3 AND attempt_no=$4 FOR UPDATE`, scope.TenantID, scope.PurposeID, actionID, attempt).Scan(&out.OperationID, &grant, &state, &raw, &credits, &expired); err != nil {
		return out, notFound(err)
	}
	if len(raw) > 0 {
		if err = json.Unmarshal(raw, &out); err != nil {
			return out, err
		}
		out.Persistence = "persisted"
		if err = tx.Commit(ctx); err != nil {
			return out, err
		}
		return out, receiptError(out)
	}
	out.Scope = scope
	out.ActionID = actionID
	out.Attempt = attempt
	out.Provider = "firecrawl"
	out.CreditsReserved = credits
	if state == "running" && !expired {
		return out, contract.ErrInFlight
	}
	if state == "running" {
		if err = s.markExpired(ctx, tx, account, grant, out.OperationID, credits); err != nil {
			return out, err
		}
	}
	out.Status = "unknown"
	out.ErrorCode = "provider_outcome_unknown"
	out.CostState = "unknown"
	if err = tx.Commit(ctx); err != nil {
		return out, err
	}
	// Это durable inferred outcome без payload receipt (SHA256 пустой).
	// Резерв уже переведён в unknown; повторного платного вызова не было.
	out.Persistence = "persisted"
	return out, contract.ErrUnknown
}

var _ contract.ReceiptReader = (*Store)(nil)

func writeError(err error) error {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "23505" {
			return contract.ErrConflict
		}
		if pg.Code == "23503" {
			return contract.ErrNotFound
		}
	}
	return err
}
