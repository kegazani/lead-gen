package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	outboxinmemory "lead-gen/backend/contexts/platform-governance/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// testTenant собирает активный тенант с клиентом для проверок транзакции.
func testTenant(t *testing.T) *domain.Tenant {
	t.Helper()
	roles, err := domain.NewRoleSet(domain.RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
		At:          time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Actor:       "user-1",
		EventID:     "evt-1",
		AuditID:     "audit-1",
		User:        &domain.NewUser{ID: "user-1", Login: "client-1", Roles: roles},
	})
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	return tenant
}

// TestUnitOfWorkCommits проверяет, что после успешного commit сохранены агрегат,
// сообщения outbox и ключ идемпотентности.
func TestUnitOfWorkCommits(t *testing.T) {
	uow := outboxinmemory.NewUnitOfWork()
	ctx := context.Background()
	tenant := testTenant(t)

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Tenants().Save(ctx, tenant); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, ports.Message{Contract: "tenant-access-changed", EventID: "evt-1", TenantID: "tenant-1"}); err != nil {
			return err
		}
		return tx.Idempotency().Remember(ctx, ports.CommandKey{TenantID: "tenant-1", CommandID: "cmd-1"})
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}
	if _, err := uow.Repository().FindByID(ctx, "tenant-1"); err != nil {
		t.Fatalf("тенант не сохранён: %v", err)
	}
	if uow.Journal().PendingCount() != 1 {
		t.Fatalf("сообщений в outbox = %d, ожидалось 1", uow.Journal().PendingCount())
	}

	seen := false
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		var err error
		seen, err = tx.Idempotency().Seen(ctx, ports.CommandKey{TenantID: "tenant-1", CommandID: "cmd-1"})
		return err
	}); err != nil {
		t.Fatalf("WithinTransaction #2: %v", err)
	}
	if !seen {
		t.Fatal("ключ команды не сохранён после commit")
	}
}

// TestUnitOfWorkRollback проверяет атомарность: ошибка функции откатывает и
// агрегат, и сообщения, и ключ.
func TestUnitOfWorkRollback(t *testing.T) {
	uow := outboxinmemory.NewUnitOfWork()
	ctx := context.Background()
	tenant := testTenant(t)
	sentinel := errors.New("сбой сценария")

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Tenants().Save(ctx, tenant); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, ports.Message{Contract: "tenant-access-changed", EventID: "evt-1", TenantID: "tenant-1"}); err != nil {
			return err
		}
		if err := tx.Idempotency().Remember(ctx, ports.CommandKey{TenantID: "tenant-1", CommandID: "cmd-1"}); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ошибка = %v, ожидалась sentinel", err)
	}
	if _, err := uow.Repository().FindByID(ctx, "tenant-1"); !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("агрегат сохранён при откате: %v", err)
	}
	if uow.Journal().PendingCount() != 0 {
		t.Fatalf("сообщения сохранены при откате: %d", uow.Journal().PendingCount())
	}
	seen := true
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		var err error
		seen, err = tx.Idempotency().Seen(ctx, ports.CommandKey{TenantID: "tenant-1", CommandID: "cmd-1"})
		return err
	}); err != nil {
		t.Fatalf("WithinTransaction #2: %v", err)
	}
	if seen {
		t.Fatal("ключ команды сохранён при откате")
	}
}

// TestJournalMarkPublished проверяет подтверждение публикации записей.
func TestJournalMarkPublished(t *testing.T) {
	uow := outboxinmemory.NewUnitOfWork()
	ctx := context.Background()
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Outbox().Append(ctx, ports.Message{Contract: "audit-recorded", EventID: "evt-1", TenantID: "tenant-1"})
	}); err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}
	pending, err := uow.Journal().Pending(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("Pending = %d, %v", len(pending), err)
	}
	if err := uow.Journal().MarkPublished(ctx, pending[0].ID); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	if uow.Journal().PendingCount() != 0 {
		t.Fatalf("неподтверждённых записей = %d", uow.Journal().PendingCount())
	}
}
