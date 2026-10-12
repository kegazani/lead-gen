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
	return newTenantNamed(t, "tenant-1", "ООО Ромашка")
}

// newTenantNamed собирает тенант с заданными идентификатором и компанией.
func newTenantNamed(t *testing.T, id, company string) *domain.Tenant {
	t.Helper()
	roles, err := domain.NewRoleSet(domain.RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	tenant, err := domain.RegisterTenant(domain.RegisterTenantParams{
		ID:          domain.TenantID(id),
		CompanyName: company,
		Description: "Поставщик промышленного оборудования",
		At:          time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		Actor:       "user-1",
		EventID:     domain.EventID("evt-" + id),
		AuditID:     domain.AuditRecordID("audit-" + id),
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

// TestRepositoryList проверяет список тенантов: только платформенные атрибуты,
// устойчивый порядок и состояние тенанта, включая отключённый.
func TestRepositoryList(t *testing.T) {
	repository := inmemory.New()
	ctx := context.Background()

	// Компании намеренно идут не в лексикографическом порядке создания: список
	// обязан сортироваться по компании, затем по идентификатору.
	second := newTenantNamed(t, "tenant-2", "ООО Василёк")
	first := newTenantNamed(t, "tenant-3", "АО Альфа")
	if err := first.Disable(domain.ChangeContext{
		At:      time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC),
		Actor:   "admin-1",
		EventID: "evt-disable",
		AuditID: "audit-disable",
	}); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	for _, tenant := range []*domain.Tenant{second, first} {
		if err := repository.Save(ctx, tenant); err != nil {
			t.Fatalf("Save %s: %v", tenant.ID(), err)
		}
	}

	summaries, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("тенантов = %d, ожидалось 2", len(summaries))
	}
	if summaries[0].ID != "tenant-3" || summaries[0].CompanyName != "АО Альфа" || summaries[0].State != domain.TenantDisabled {
		t.Fatalf("первый тенант = %+v", summaries[0])
	}
	if summaries[1].ID != "tenant-2" || summaries[1].CompanyName != "ООО Василёк" || summaries[1].State != domain.TenantActive {
		t.Fatalf("второй тенант = %+v", summaries[1])
	}
}
