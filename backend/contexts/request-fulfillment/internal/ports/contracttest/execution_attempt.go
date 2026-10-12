package contracttest

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

type ExecutionAttemptHarness struct {
	UnitOfWork   ports.UnitOfWork
	Repository   ports.RequestRepository
	Attempts     ports.ExecutionAttemptRepository
	Journal      platformoutbox.Journal
	ExpireLease  func()
	AdvanceClock func(time.Duration)
}

type ExecutionAttemptFactory func(t *testing.T) ExecutionAttemptHarness

// RunExecutionAttemptContract — общий контракт owner intent, атомарности UoW,
// идемпотентности CommandID и fenced lease. Драйвер/БД suite не предполагает.
func RunExecutionAttemptContract(t *testing.T, factory ExecutionAttemptFactory) {
	t.Helper()
	ctx := context.Background()
	makeIntent := func(id domain.RequestID, command domain.CommandID, generation int64, target domain.LeadTarget) domain.ExecutionIntent {
		t.Helper()
		intent, err := domain.NewExecutionIntent(command, id, "tenant-1", generation, target, []byte(`{"offer_revision":1}`))
		if err != nil {
			t.Fatal(err)
		}
		return intent
	}
	commit := func(t *testing.T, h ExecutionAttemptHarness, intent domain.ExecutionIntent) {
		t.Helper()
		req := mustRequest(t, intent.RequestID())
		err := h.UnitOfWork.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			if err := tx.Requests().Save(txCtx, req, 0); err != nil {
				return err
			}
			if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, 0); err != nil {
				return err
			}
			return tx.Outbox().Append(txCtx, req.DomainEvents()...)
		})
		if err != nil {
			t.Fatalf("commit intent: %v", err)
		}
	}
	t.Run("идемпотентность и неизменяемость", func(t *testing.T) {
		h := factory(t)
		intent := makeIntent("req-idempotent", "cmd-same", 1, domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 0})
		commit(t, h, intent)
		got, err := h.Attempts.FindByCommand(ctx, "tenant-1", "cmd-same")
		if err != nil {
			t.Fatal(err)
		}
		if !got.Intent.SameCommand(intent) {
			t.Fatal("сохранённая intent отличается")
		}
		if got.Intent.Parameters().LeadTarget.State != domain.LeadTargetExplicit {
			t.Fatal("explicit zero потерял семантику")
		}
		err = h.UnitOfWork.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, 0); err != nil {
				return err
			}
			conflict, err := domain.NewExecutionIntent(intent.CommandID(), intent.RequestID(), intent.TenantID(), intent.Generation(), domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 9}, json.RawMessage(`{"offer_revision":1}`))
			if err != nil {
				return err
			}
			return tx.ExecutionAttempts().SaveIntent(txCtx, conflict, 0)
		})
		if !errors.Is(err, ports.ErrExecutionCommandConflict) {
			t.Fatalf("изменённый повтор command: %v", err)
		}
		unchanged, err := h.Attempts.FindByCommand(ctx, "tenant-1", "cmd-same")
		if err != nil {
			t.Fatal(err)
		}
		if !unchanged.Intent.SameCommand(intent) {
			t.Fatal("конфликтный повтор изменил сохранённую intent")
		}
	})
	t.Run("rollback агрегата intent и outbox", func(t *testing.T) {
		h := factory(t)
		req := mustRequest(t, "req-rollback-intent")
		intent := makeIntent(req.ID(), "cmd-rollback", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified})
		sentinel := errors.New("rollback")
		err := h.UnitOfWork.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			if err := tx.Requests().Save(txCtx, req, 0); err != nil {
				return err
			}
			if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, 0); err != nil {
				return err
			}
			if err := tx.Outbox().Append(txCtx, req.DomainEvents()...); err != nil {
				return err
			}
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("ошибка = %v", err)
		}
		if _, err = h.Repository.FindByID(ctx, req.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
			t.Fatalf("rollback сохранил aggregate: %v", err)
		}
		if _, err = h.Attempts.FindByCommand(ctx, "tenant-1", intent.CommandID()); !errors.Is(err, ports.ErrExecutionAttemptNotFound) {
			t.Fatalf("rollback сохранил intent: %v", err)
		}
		pending, err := h.Journal.Pending(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 0 {
			t.Fatalf("rollback сохранил %d outbox records", len(pending))
		}
	})
	t.Run("claim эксклюзивен и fenced", func(t *testing.T) {
		h := factory(t)
		intent := makeIntent("req-claim", "cmd-claim", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified})
		commit(t, h, intent)
		first, err := h.Attempts.ClaimNext(ctx, "dispatcher-a", 20*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.Attempts.ClaimNext(ctx, "dispatcher-b", time.Minute); !errors.Is(err, ports.ErrExecutionLeaseUnavailable) {
			t.Fatalf("параллельный claim = %v", err)
		}
		h.ExpireLease()
		second, err := h.Attempts.ClaimNext(ctx, "dispatcher-b", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if second.Fence <= first.Fence {
			t.Fatalf("новый fence %d <= старому %d", second.Fence, first.Fence)
		}
		if _, err := h.Attempts.RenewLease(ctx, first, time.Minute); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("устаревший lease продлён: %v", err)
		}
		oldUntil := second.Until
		h.AdvanceClock(time.Second)
		renewed, err := h.Attempts.RenewLease(ctx, second, time.Minute)
		if err != nil {
			t.Fatalf("RenewLease: %v", err)
		}
		if !renewed.Until.After(oldUntil) {
			t.Fatalf("renewed lease expiry %s не позже исходного %s", renewed.Until, oldUntil)
		}
		second = renewed
		if err := h.Attempts.BindRun(ctx, first, "run-stale"); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("устаревший lease изменил RunID: %v", err)
		}
		if err := h.Attempts.BindRun(ctx, second, "run-current"); err != nil {
			t.Fatalf("BindRun текущим lease: %v", err)
		}
		staleVersion := second
		staleVersion.RequestVersion++
		if err := h.Attempts.SetState(ctx, staleVersion, ports.AttemptUnknown); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("устаревшая request version принята: %v", err)
		}
		if err := h.Attempts.SetExpectedObjects(ctx, second, []string{"object-b", "object-a"}); err != nil {
			t.Fatalf("SetExpectedObjects: %v", err)
		}
		if err := h.Attempts.SetExpectedObjects(ctx, second, []string{"object-a", "object-b"}); err != nil {
			t.Fatalf("идемпотентный manifest: %v", err)
		}
		if err := h.Attempts.SetExpectedObjects(ctx, second, []string{"object-a"}); !errors.Is(err, ports.ErrExecutionManifestConflict) {
			t.Fatalf("изменённый manifest: %v", err)
		}
		if err := h.Attempts.RecordReceipt(ctx, second, "object-a", domain.ReceiptUnknown); err != nil {
			t.Fatalf("unknown receipt: %v", err)
		}
		if err := h.Attempts.RecordReceipt(ctx, second, "object-x", domain.ReceiptConfirmed); !errors.Is(err, ports.ErrExecutionReceiptConflict) {
			t.Fatalf("receipt вне manifest: %v", err)
		}
		if err := h.Attempts.RecordReceipt(ctx, second, "object-b", domain.ReceiptConfirmed); err != nil {
			t.Fatalf("confirmed receipt: %v", err)
		}
		if err := h.Attempts.RecordReceipt(ctx, second, "object-b", domain.ReceiptUnknown); !errors.Is(err, ports.ErrExecutionReceiptConflict) {
			t.Fatalf("подтверждённый receipt понижен: %v", err)
		}
		reloaded, err := h.Attempts.FindByCommand(ctx, "tenant-1", "cmd-claim")
		if err != nil {
			t.Fatal(err)
		}
		if len(reloaded.ExpectedObjects) != 2 || len(reloaded.Receipts) != 2 {
			t.Fatalf("manifest/receipts: %+v / %+v", reloaded.ExpectedObjects, reloaded.Receipts)
		}
	})
	t.Run("unknown legacy блокирует dispatcher", func(t *testing.T) {
		h := factory(t)
		intent := makeIntent("req-legacy", "cmd-legacy", 1, domain.LeadTarget{State: domain.LeadTargetUnknownLegacy})
		commit(t, h, intent)
		if _, err := h.Attempts.ClaimNext(ctx, "dispatcher", time.Minute); !errors.Is(err, ports.ErrExecutionLeaseUnavailable) {
			t.Fatalf("unknown legacy попал в claim: %v", err)
		}
	})
	t.Run("tenant и generation fence", func(t *testing.T) {
		h := factory(t)
		intent := makeIntent("req-tenant-fence", "cmd-fence", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified})
		commit(t, h, intent)
		if _, err := h.Attempts.FindByCommand(ctx, "tenant-other", intent.CommandID()); !errors.Is(err, ports.ErrExecutionAttemptNotFound) {
			t.Fatalf("чужой tenant прочитал intent: %v", err)
		}
		next, err := domain.NewExecutionIntent("cmd-next", "req-tenant-fence", "tenant-1", 3, domain.LeadTarget{State: domain.LeadTargetUnspecified}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = h.UnitOfWork.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			return tx.ExecutionAttempts().SaveIntent(txCtx, next, 0)
		}); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("скачок поколения принят: %v", err)
		}
	})
	t.Run("intent следующего поколения не допускает ранний start", func(t *testing.T) {
		for _, priorState := range []string{"claimed", "unknown"} {
			t.Run(priorState, func(t *testing.T) {
				h := factory(t)
				firstIntent := makeIntent("req-generation-gate", "cmd-generation-1", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified})
				commit(t, h, firstIntent)
				lease, err := h.Attempts.ClaimNext(ctx, "dispatcher", time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if priorState == "unknown" {
					if err := h.Attempts.SetState(ctx, lease, ports.AttemptUnknown); err != nil {
						t.Fatal(err)
					}
				}
				secondIntent, err := domain.NewExecutionIntent("cmd-generation-2", "req-generation-gate", "tenant-1", 2, domain.LeadTarget{State: domain.LeadTargetExplicit, Value: 4}, []byte(`{"offer_revision":1}`))
				if err != nil {
					t.Fatal(err)
				}
				err = h.UnitOfWork.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
					req, err := tx.Requests().FindByID(txCtx, "req-generation-gate")
					if err != nil {
						return err
					}
					if err = tx.Requests().Save(txCtx, req, req.Version()); err != nil {
						return err
					}
					return tx.ExecutionAttempts().SaveIntent(txCtx, secondIntent, req.Version()-1)
				})
				if err != nil {
					t.Fatalf("сохранить queued generation 2: %v", err)
				}
				if _, err = h.Attempts.ClaimNext(ctx, "dispatcher-2", time.Minute); !errors.Is(err, ports.ErrExecutionLeaseUnavailable) {
					t.Fatalf("generation 2 стартовала при %s generation 1: %v", priorState, err)
				}
				if priorState == "claimed" {
					if err = h.Attempts.SetState(ctx, lease, ports.AttemptTerminal); err != nil {
						t.Fatalf("terminal generation 1: %v", err)
					}
					claimed, err := h.Attempts.ClaimNext(ctx, "dispatcher-2", time.Minute)
					if err != nil {
						t.Fatalf("generation 2 после terminal: %v", err)
					}
					if claimed.Generation != 2 {
						t.Fatalf("claim generation=%d, ожидалась 2", claimed.Generation)
					}
				}
			})
		}
	})
	t.Run("terminal и unknown не имеют обратных переходов", func(t *testing.T) {
		h := factory(t)
		commit(t, h, makeIntent("req-state-fence", "cmd-state-fence", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified}))
		lease, err := h.Attempts.ClaimNext(ctx, "dispatcher", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = h.Attempts.SetState(ctx, lease, ports.AttemptUnknown); err != nil {
			t.Fatal(err)
		}
		if err = h.Attempts.BindRun(ctx, lease, "run-after-unknown"); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("unknown перешёл в started: %v", err)
		}
		if err = h.Attempts.SetState(ctx, lease, ports.AttemptTerminal); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("unknown перешёл в terminal неразрешённым портом: %v", err)
		}
		h2 := factory(t)
		commit(t, h2, makeIntent("req-terminal-fence", "cmd-terminal-fence", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified}))
		terminalLease, err := h2.Attempts.ClaimNext(ctx, "dispatcher", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if err = h2.Attempts.SetState(ctx, terminalLease, ports.AttemptTerminal); err != nil {
			t.Fatal(err)
		}
		if err = h2.Attempts.SetState(ctx, terminalLease, ports.AttemptUnknown); !errors.Is(err, ports.ErrExecutionFenceConflict) {
			t.Fatalf("terminal перешёл в unknown: %v", err)
		}
	})
	t.Run("одновременный claim единственный", func(t *testing.T) {
		h := factory(t)
		commit(t, h, makeIntent("req-race-claim", "cmd-race", 1, domain.LeadTarget{State: domain.LeadTargetUnspecified}))
		var wg sync.WaitGroup
		successes := make(chan struct{}, 2)
		for _, owner := range []string{"a", "b"} {
			wg.Add(1)
			go func(owner string) {
				defer wg.Done()
				if _, err := h.Attempts.ClaimNext(ctx, owner, time.Minute); err == nil {
					successes <- struct{}{}
				}
			}(owner)
		}
		wg.Wait()
		close(successes)
		n := 0
		for range successes {
			n++
		}
		if n != 1 {
			t.Fatalf("успешных claim = %d, ожидался 1", n)
		}
	})
}
