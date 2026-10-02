package domain

import (
	"errors"
	"testing"
	"time"
)

// at — фиксированное время для детерминированных проверок.
var at = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// validParams возвращает корректные данные регистрации тенанта.
func validParams() RegisterTenantParams {
	return RegisterTenantParams{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
		UserID:      "user-1",
		Login:       "client-1",
		Role:        RoleClient,
		At:          at,
		EventID:     "evt-1",
	}
}

// TestRegisterTenant проверяет создание тенанта: обязательные поля, состав
// пользователей и событие tenant-access-changed.
func TestRegisterTenant(t *testing.T) {
	tenant, err := RegisterTenant(validParams())
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if tenant.ID() != "tenant-1" || tenant.CompanyName() != "ООО Ромашка" {
		t.Fatalf("тенант = %+v", tenant)
	}
	users := tenant.Users()
	if len(users) != 1 || users[0].Role() != RoleClient || users[0].Login() != "client-1" {
		t.Fatalf("пользователи = %+v", users)
	}
	events := tenant.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("события = %d, ожидалось 1", len(events))
	}
	if events[0].Name() != EventTenantAccessChanged {
		t.Fatalf("имя события = %q, ожидалось %q", events[0].Name(), EventTenantAccessChanged)
	}
	if events[0].Header().TenantID != "tenant-1" || events[0].Header().OccurredAt != at {
		t.Fatalf("конверт события = %+v", events[0].Header())
	}
}

// TestRegisterTenantValidation проверяет инвариант «тенант без названия и
// описания компании не создаётся».
func TestRegisterTenantValidation(t *testing.T) {
	cases := map[string]func(*RegisterTenantParams){
		"пустое название":      func(p *RegisterTenantParams) { p.CompanyName = "   " },
		"пустое описание":      func(p *RegisterTenantParams) { p.Description = "" },
		"пустой идентификатор": func(p *RegisterTenantParams) { p.ID = "" },
		"неизвестная роль":     func(p *RegisterTenantParams) { p.Role = Role("admin") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := validParams()
			mutate(&params)
			if _, err := RegisterTenant(params); err == nil {
				t.Fatal("некорректная регистрация не отвергнута")
			}
		})
	}
}

// TestTenantSnapshotRoundTrip проверяет восстановление агрегата из снимка без
// эмиссии событий.
func TestTenantSnapshotRoundTrip(t *testing.T) {
	tenant, err := RegisterTenant(validParams())
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	restored, err := RehydrateTenant(tenant.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateTenant: %v", err)
	}
	if restored.ID() != tenant.ID() || restored.CompanyName() != tenant.CompanyName() {
		t.Fatalf("восстановленный тенант = %+v", restored)
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
	if len(restored.Users()) != 1 {
		t.Fatalf("пользователи = %+v", restored.Users())
	}
}

// TestRehydrateTenantRejectsDuplicates проверяет инвариант «роль принадлежит
// ровно одному тенанту»: дублирующийся пользователь делает снимок некорректным.
func TestRehydrateTenantRejectsDuplicates(t *testing.T) {
	snapshot := TenantSnapshot{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик",
		CreatedAt:   at,
		Users: []TenantUserSnapshot{
			{ID: "user-1", Login: "client-1", Role: RoleClient},
			{ID: "user-1", Login: "client-2", Role: RoleClient},
		},
	}
	if _, err := RehydrateTenant(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}

// TestAddUserRejectsDuplicate проверяет, что повторное добавление пользователя
// в тот же тенант отвергается доменом.
func TestAddUserRejectsDuplicate(t *testing.T) {
	tenant, err := RegisterTenant(validParams())
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	err = tenant.addUser(TenantUser{id: "user-1", login: "client-1", role: RoleClient}, at, "evt-2")
	if !errors.Is(err, ErrDuplicateUser) {
		t.Fatalf("ошибка = %v, ожидалась ErrDuplicateUser", err)
	}
}
