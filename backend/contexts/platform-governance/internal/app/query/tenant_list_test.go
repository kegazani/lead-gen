package query

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// fakeDirectory — управляемый порт списка тенантов для теста сценария.
type fakeDirectory struct {
	summaries []ports.TenantSummary
	err       error
}

// List реализует ports.TenantDirectory.
func (f fakeDirectory) List(context.Context) ([]ports.TenantSummary, error) {
	return f.summaries, f.err
}

// TestListTenantsMapsPlatformAttributes проверяет, что сценарий отдаёт только
// платформенные атрибуты тенанта (идентификатор, компания, состояние) в порядке
// источника: персональных данных в представлении нет по составу полей.
func TestListTenantsMapsPlatformAttributes(t *testing.T) {
	handler := NewListTenantsHandler(fakeDirectory{summaries: []ports.TenantSummary{
		{ID: "tenant-2", CompanyName: "ООО Ромашка", State: domain.TenantActive},
		{ID: "tenant-1", CompanyName: "ООО Василёк", State: domain.TenantDisabled},
	}})
	views, err := handler.Handle(context.Background(), ListTenants{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("строк = %d, ожидалось 2", len(views))
	}
	if views[0].ID != "tenant-2" || views[0].CompanyName != "ООО Ромашка" || views[0].State != string(domain.TenantActive) {
		t.Fatalf("первая строка = %+v", views[0])
	}
	if views[1].ID != "tenant-1" || views[1].State != string(domain.TenantDisabled) {
		t.Fatalf("вторая строка = %+v", views[1])
	}
}

// TestListTenantsEmpty проверяет, что отсутствие тенантов — пустой список, а не
// ошибка: экран покажет честное «пока нет».
func TestListTenantsEmpty(t *testing.T) {
	views, err := NewListTenantsHandler(fakeDirectory{}).Handle(context.Background(), ListTenants{})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("строк = %d, ожидалось 0", len(views))
	}
}

// TestListTenantsDirectoryError проверяет, что сбой источника не маскируется под
// пустой успех: ошибка проходит наружу без подмены данными.
func TestListTenantsDirectoryError(t *testing.T) {
	sentinel := errors.New("источник недоступен")
	if _, err := NewListTenantsHandler(fakeDirectory{err: sentinel}).Handle(context.Background(), ListTenants{}); !errors.Is(err, sentinel) {
		t.Fatalf("ошибка = %v, ожидалась sentinel", err)
	}
}
