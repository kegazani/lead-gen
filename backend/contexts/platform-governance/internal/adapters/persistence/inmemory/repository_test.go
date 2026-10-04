package inmemory_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/platform-governance/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// newTenant собирает тенант для проверок репозитория.
func newTenant(t *testing.T) *domain.Tenant {
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

// TestRepositorySaveAndFind проверяет сохранение и загрузку тенанта.
func TestRepositorySaveAndFind(t *testing.T) {
	repository := inmemory.New()
	tenant := newTenant(t)
	if err := repository.Save(context.Background(), tenant); err != nil {
		t.Fatalf("Save: %v", err)
	}
	found, err := repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.CompanyName() != "ООО Ромашка" || len(found.Users()) != 1 {
		t.Fatalf("загруженный тенант = %+v", found)
	}
}

// TestRepositoryNotFound проверяет, что отсутствие тенанта — это ErrTenantNotFound.
func TestRepositoryNotFound(t *testing.T) {
	repository := inmemory.New()
	if _, err := repository.FindByID(context.Background(), "ghost"); !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantNotFound", err)
	}
}

// TestRepositorySaveNil проверяет, что сохранение nil-тенанта отвергается.
func TestRepositorySaveNil(t *testing.T) {
	repository := inmemory.New()
	if err := repository.Save(context.Background(), nil); err == nil {
		t.Fatal("сохранение nil-тенанта не отвергнуто")
	}
}
