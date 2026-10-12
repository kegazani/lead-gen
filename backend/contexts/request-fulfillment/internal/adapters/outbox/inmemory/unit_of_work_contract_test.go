package inmemory

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports/contracttest"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

type failingCommitJournal struct {
	*TransactionalJournal
	err error
}

func (j *failingCommitJournal) CommitBatchUnderGate(context.Context, []platformoutbox.Message, func()) error {
	return j.err
}

type cancelCommitJournal struct {
	*TransactionalJournal
	cancel context.CancelFunc
}

func (j *cancelCommitJournal) CommitBatchUnderGate(ctx context.Context, messages []platformoutbox.Message, commit func()) error {
	j.cancel()
	return j.TransactionalJournal.CommitBatchUnderGate(ctx, messages, commit)
}

func newGateTestUOW(hooks *persistence.GateHooks) (*persistence.Repository, *TransactionalJournal, *UnitOfWork) {
	clock := contracttest.NewManualClock(time.Now().UTC())
	gate := persistence.NewGateWithHooks(hooks)
	repo := persistence.NewWithClockAndGate(clock, gate)
	journal := NewTransactionalJournal()
	return repo, journal, NewUnitOfWorkWith(repo, journal)
}

func TestUnitOfWorkCancellationBeforeAcquireSkipsCallback(t *testing.T) {
	repo, _, uow := newGateTestUOW(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	err := uow.WithinTransaction(ctx, func(context.Context, ports.Transaction) error { called.Store(true); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка=%v, ожидалась context.Canceled", err)
	}
	if called.Load() {
		t.Fatal("callback выполнен при уже отменённом context")
	}
	if _, err := repo.CountInWork(context.Background(), "tenant-1"); err != nil {
		t.Fatalf("gate не освободился: %v", err)
	}
}

func TestUnitOfWorkCancellationWhileContendedSkipsCallback(t *testing.T) {
	contended := make(chan struct{}, 2)
	repo, journal, firstUOW := newGateTestUOW(&persistence.GateHooks{Contended: func() { contended <- struct{}{} }})
	secondUOW := NewUnitOfWorkWith(repo, journal)
	release := make(chan struct{})
	firstEntered := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- firstUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { close(firstEntered); <-release; return nil })
	}()
	select {
	case <-firstEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("первый UoW callback не захватил gate")
	}
	secondCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var callbackCalled atomic.Bool
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- secondUOW.WithinTransaction(secondCtx, func(context.Context, ports.Transaction) error { callbackCalled.Store(true); return nil })
	}()
	select {
	case <-contended:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("второй UoW не дошёл до ожидания gate")
	}
	cancel()
	if err := <-secondDone; !errors.Is(err, context.Canceled) {
		close(release)
		t.Fatalf("отмена ожидания вернула %v", err)
	}
	if callbackCalled.Load() {
		close(release)
		t.Fatal("callback contention-вызова выполнен после отмены")
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := secondUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("gate не освободился после отменённого ожидания: %v", err)
	}
}

func TestUnitOfWorkCancellationAfterAcquireBeforeCallbackReleasesGate(t *testing.T) {
	var cancel context.CancelFunc
	var cancelOnce sync.Once
	repo, _, uow := newGateTestUOW(&persistence.GateHooks{Acquired: func() {
		if cancel != nil {
			cancelOnce.Do(cancel)
		}
	}})
	ctx, cancelCtx := context.WithCancel(context.Background())
	cancel = cancelCtx
	var called atomic.Bool
	err := uow.WithinTransaction(ctx, func(context.Context, ports.Transaction) error { called.Store(true); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка=%v, ожидалась отмена после acquire", err)
	}
	if called.Load() {
		t.Fatal("callback выполнен после отмены на acquire barrier")
	}
	if err := uow.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("gate не освободился после отмены: %v", err)
	}
	if _, err := repo.CountInWork(context.Background(), "tenant-1"); err != nil {
		t.Fatal(err)
	}
}

func TestNestedSameAndOtherUoWReturnTypedError(t *testing.T) {
	repo, journal, first := newGateTestUOW(nil)
	second := NewUnitOfWorkWith(repo, journal)
	var sameErr, otherErr error
	err := first.WithinTransaction(context.Background(), func(txCtx context.Context, _ ports.Transaction) error {
		child, cancel := context.WithCancel(txCtx)
		defer cancel()
		sameErr = first.WithinTransaction(child, func(context.Context, ports.Transaction) error {
			t.Error("nested callback same-UoW запущен")
			return nil
		})
		derived, cancelDerived := context.WithCancel(txCtx)
		defer cancelDerived()
		otherErr = second.WithinTransaction(derived, func(context.Context, ports.Transaction) error {
			t.Error("nested callback other-UoW запущен")
			return nil
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(sameErr, ports.ErrNestedTransaction) {
		t.Fatalf("same-UoW error=%v", sameErr)
	}
	if !errors.Is(otherErr, ports.ErrNestedTransaction) {
		t.Fatalf("other-UoW error=%v", otherErr)
	}
}

// TestExecutionAttemptContract запускает общий storage-контракт на memory
// адаптере (только тестовый/dev контур).
func TestExecutionAttemptContract(t *testing.T) {
	contracttest.RunExecutionAttemptContract(t, func(t *testing.T) contracttest.ExecutionAttemptHarness {
		t.Helper()
		clock := contracttest.NewManualClock(time.Now().UTC())
		repo := persistence.NewWithClock(clock)
		journal := NewTransactionalJournal()
		return contracttest.ExecutionAttemptHarness{UnitOfWork: NewUnitOfWorkWith(repo, journal), Repository: repo, Attempts: repo, Journal: journal, ExpireLease: func() { clock.Advance(time.Minute) }, AdvanceClock: clock.Advance}
	})
}

func TestTransactionalJournalFailureRollsBackAggregateIntentAndOutbox(t *testing.T) {
	repo := persistence.New()
	journal := NewTransactionalJournal()
	sentinel := errors.New("ошибка batch commit")
	uow := NewUnitOfWorkWith(repo, &failingCommitJournal{TransactionalJournal: journal, err: sentinel})
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "req-journal-error", OfferID: "offer-journal-error", TenantID: "tenant-1", Content: "Оффер", Target: "IT", Actor: "actor", At: time.Now().UTC(), EventID: "evt-journal-error"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.NewExecutionIntent("cmd-journal-error", request.ID(), request.TenantID(), 1, domain.LeadTarget{State: domain.LeadTargetUnspecified}, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = uow.WithinTransaction(context.Background(), func(txCtx context.Context, tx ports.Transaction) error {
		if err := tx.Requests().Save(txCtx, request, 0); err != nil {
			return err
		}
		if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, 0); err != nil {
			return err
		}
		return tx.Outbox().Append(txCtx, request.DomainEvents()...)
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("UoW error=%v, ожидалась ошибка batch append", err)
	}
	if _, err := repo.FindByID(context.Background(), request.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("после append failure aggregate виден: %v", err)
	}
	if _, err := repo.FindByCommand(context.Background(), request.TenantID(), intent.CommandID()); !errors.Is(err, ports.ErrExecutionAttemptNotFound) {
		t.Fatalf("после append failure intent видна: %v", err)
	}
	pending, err := journal.Pending(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("после append failure опубликовано %d сообщений", len(pending))
	}
}

func TestUnitOfWorkCommitCancellationAndCallbackPanicReleaseGate(t *testing.T) {
	ctx := context.Background()
	repo, journal, _ := newGateTestUOW(nil)
	cancelledCtx, cancel := context.WithCancel(ctx)
	uow := NewUnitOfWorkWith(repo, &cancelCommitJournal{TransactionalJournal: journal, cancel: cancel})
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "req-cancel-commit", OfferID: "offer-cancel-commit", TenantID: "tenant-1", Content: "Оффер", Target: "IT", Actor: "actor", At: time.Now().UTC(), EventID: "evt-cancel-commit"})
	if err != nil {
		t.Fatal(err)
	}
	err = uow.WithinTransaction(cancelledCtx, func(txCtx context.Context, tx ports.Transaction) error { return tx.Requests().Save(txCtx, request, 0) })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("commit после отмены вернул %v", err)
	}
	if _, err = repo.FindByID(ctx, request.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("отменённый commit опубликовал aggregate: %v", err)
	}
	if _, err := journal.Pending(ctx, 0); err != nil {
		t.Fatalf("outbox read после cancellation: %v", err)
	}
	uow = NewUnitOfWorkWith(repo, journal)
	if err = uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error { return tx.Requests().Save(txCtx, request, 0) }); err != nil {
		t.Fatalf("после отмены gate не освободился: %v", err)
	}
	if _, err = repo.FindByID(ctx, request.ID()); err != nil {
		t.Fatalf("успешный commit после cancellation: %v", err)
	}

	panicRequest, err := domain.NewRequest(domain.NewRequestParams{ID: "req-panic-callback", OfferID: "offer-panic-callback", TenantID: "tenant-1", Content: "Оффер", Target: "IT", Actor: "actor", At: time.Now().UTC(), EventID: "evt-panic-callback"})
	if err != nil {
		t.Fatal(err)
	}
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		_ = uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			if err := tx.Requests().Save(txCtx, panicRequest, 0); err != nil {
				return err
			}
			panic("callback panic")
		})
	}()
	if !panicked {
		t.Fatal("ожидалась паника callback")
	}
	if _, err = repo.FindByID(ctx, panicRequest.ID()); !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("panic callback опубликовал aggregate: %v", err)
	}
	if err = uow.WithinTransaction(ctx, func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("gate не освободился после panic: %v", err)
	}
}

func TestUnitOfWorkSerializesCloneCallbackCommitWithDirectClaim(t *testing.T) {
	ctx := context.Background()
	clock := contracttest.NewManualClock(time.Now().UTC())
	contended := make(chan struct{}, 8)
	gate := persistence.NewGateWithHooks(&persistence.GateHooks{Contended: func() { contended <- struct{}{} }})
	repo := persistence.NewWithClockAndGate(clock, gate)
	journal := NewTransactionalJournal()
	firstUOW := NewUnitOfWorkWith(repo, journal)
	secondUOW := NewUnitOfWorkWith(repo, journal)
	request, err := domain.NewRequest(domain.NewRequestParams{ID: "req-lost-claim", OfferID: "offer-lost-claim", TenantID: "tenant-1", Content: "Оффер", Target: "IT", Actor: "actor", At: clock.Now(), EventID: "evt-lost-claim"})
	if err != nil {
		t.Fatal(err)
	}
	intent, err := domain.NewExecutionIntent("cmd-lost-claim", request.ID(), request.TenantID(), 1, domain.LeadTarget{State: domain.LeadTargetUnspecified}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = firstUOW.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		if err := tx.Requests().Save(txCtx, request, 0); err != nil {
			return err
		}
		if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, 0); err != nil {
			return err
		}
		return tx.Outbox().Append(txCtx, request.DomainEvents()...)
	}); err != nil {
		t.Fatalf("seed transaction: %v", err)
	}

	firstEntered, releaseFirst := make(chan struct{}), make(chan struct{})
	var firstOnce sync.Once
	finishFirst := func() { firstOnce.Do(func() { close(releaseFirst) }) }
	defer finishFirst()
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- firstUOW.WithinTransaction(ctx, func(context.Context, ports.Transaction) error { close(firstEntered); <-releaseFirst; return nil })
	}()
	<-firstEntered // Clone первого read-only UoW завершён до входа в callback.

	secondEntered, releaseSecond := make(chan struct{}), make(chan struct{})
	var secondOnce sync.Once
	finishSecond := func() { secondOnce.Do(func() { close(releaseSecond) }) }
	defer finishSecond()
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- secondUOW.WithinTransaction(ctx, func(context.Context, ports.Transaction) error { close(secondEntered); <-releaseSecond; return nil })
	}()
	select {
	case <-contended:
	case <-time.After(5 * time.Second):
		t.Fatal("второй UoW не сообщил реальное ожидание gate")
	}
	finishFirst()
	if err := <-firstDone; err != nil {
		t.Fatalf("first read-only UoW: %v", err)
	}
	select {
	case <-secondEntered:
	case <-time.After(time.Second):
		t.Fatal("второй UoW не вошёл после освобождения первого")
	}

	claimDone := make(chan struct {
		lease ports.ExecutionLease
		err   error
	}, 1)
	go func() {
		lease, err := repo.ClaimNext(ctx, "dispatcher", time.Minute)
		claimDone <- struct {
			lease ports.ExecutionLease
			err   error
		}{lease, err}
	}()
	readerDone := make(chan error, 1)
	go func() { _, err := repo.FindByCommand(ctx, request.TenantID(), intent.CommandID()); readerDone <- err }()
	journalReadDone := make(chan struct {
		messages []platformoutbox.Message
		err      error
	}, 1)
	go func() {
		messages, err := journal.Pending(ctx, 0)
		journalReadDone <- struct {
			messages []platformoutbox.Message
			err      error
		}{messages, err}
	}()
	for i := 0; i < 3; i++ {
		select {
		case <-contended:
		case <-time.After(5 * time.Second):
			t.Fatal("прямой writer/reader не дошёл до занятого gate")
		}
	}
	finishSecond()
	if err := <-secondDone; err != nil {
		t.Fatalf("second read-only UoW: %v", err)
	}
	if err := <-readerDone; err != nil {
		t.Fatalf("reader после commit: %v", err)
	}
	journalRead := <-journalReadDone
	if journalRead.err != nil || len(journalRead.messages) != 1 {
		t.Fatalf("journal reader после commit: len=%d err=%v", len(journalRead.messages), journalRead.err)
	}
	claimed := <-claimDone
	if claimed.err != nil {
		t.Fatalf("ClaimNext после commit: %v", claimed.err)
	}
	stored, err := repo.FindByCommand(ctx, request.TenantID(), intent.CommandID())
	if err != nil {
		t.Fatal(err)
	}
	if stored.State != ports.AttemptClaimed || stored.Fence != claimed.lease.Fence {
		t.Fatalf("claim потерян после read-only ReplaceWith: state=%s fence=%d, lease fence=%d", stored.State, stored.Fence, claimed.lease.Fence)
	}
	if _, err := repo.ClaimNext(ctx, "second-dispatcher", time.Minute); !errors.Is(err, ports.ErrExecutionLeaseUnavailable) {
		t.Fatalf("дублирующий claim после commit: %v", err)
	}
}

func TestJournalSameGateRebindIsRaceFreeWithOperationsAndSecondUoW(t *testing.T) {
	repo, journal, firstUOW := newGateTestUOW(nil)
	secondUOW := NewUnitOfWorkWith(repo, journal)
	if secondUOW.journalErr != nil {
		t.Fatalf("повторная привязка того же gate: %v", secondUOW.journalErr)
	}
	gate := repo.VisibilityGate()
	const workers, iterations = 8, 250
	start := make(chan struct{})
	errs := make(chan error, workers*2)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				u := NewUnitOfWorkWith(repo, journal)
				if u.journalErr != nil {
					errs <- u.journalErr
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < iterations; i++ {
				if _, err := journal.Pending(context.Background(), 0); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if journal.VisibilityGate() != gate {
		t.Fatal("повторная привязка изменила gate")
	}
	if err := secondUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("второй UoW на общем gate: %v", err)
	}
	if err := firstUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("первый UoW после повторных bind: %v", err)
	}
}

func TestJournalRejectsDifferentGateWithoutSwitch(t *testing.T) {
	firstRepo, journal, firstUOW := newGateTestUOW(nil)
	secondRepo := persistence.New()
	secondUOW := NewUnitOfWorkWith(secondRepo, journal)
	if err := secondUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); !errors.Is(err, ErrJournalBoundToDifferentGate) {
		t.Fatalf("different gate error=%v", err)
	}
	if journal.VisibilityGate() != firstRepo.VisibilityGate() {
		t.Fatal("rejected binding switched journal gate")
	}
	if err := firstUOW.WithinTransaction(context.Background(), func(context.Context, ports.Transaction) error { return nil }); err != nil {
		t.Fatalf("original UoW after rejected binding: %v", err)
	}
}

// TestUnitOfWorkContract прогоняет общий contract suite порта UnitOfWork против
// in-memory-реализации. Тот же suite проходит и против PostgreSQL-реализации:
// запись агрегата и события атомарны в обеих.
func TestUnitOfWorkContract(t *testing.T) {
	contracttest.RunUnitOfWorkContract(t, func(t *testing.T) contracttest.UnitOfWorkHarness {
		t.Helper()
		repo := persistence.New()
		journal := NewTransactionalJournal()
		return contracttest.UnitOfWorkHarness{
			UnitOfWork: NewUnitOfWorkWith(repo, journal),
			Repository: repo,
			Journal:    journal,
		}
	})
}
