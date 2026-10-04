package domain

import (
	"errors"
	"testing"
	"time"
)

// at — фиксированное время для детерминированных проверок.
var at = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// clientRoles возвращает набор из одной роли клиента.
func clientRoles(t *testing.T) RoleSet {
	t.Helper()
	roles, err := NewRoleSet(RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	return roles
}

// validParams возвращает корректные данные создания тенанта с первым клиентом.
func validParams(t *testing.T) RegisterTenantParams {
	t.Helper()
	return RegisterTenantParams{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
		At:          at,
		Actor:       "user-1",
		EventID:     "evt-1",
		AuditID:     "audit-1",
		User:        &NewUser{ID: "user-1", Login: "client-1", Roles: clientRoles(t)},
	}
}

// change собирает контекст изменения доступа для проверок.
func change() ChangeContext {
	return ChangeContext{At: at, Actor: "admin-1", EventID: "evt-2", AuditID: "audit-2"}
}

// TestRegisterTenant проверяет создание тенанта: активное состояние, состав
// пользователей, событие tenant-access-changed и запись аудита.
func TestRegisterTenant(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if tenant.ID() != "tenant-1" || tenant.CompanyName() != "ООО Ромашка" {
		t.Fatalf("тенант = %+v", tenant)
	}
	if tenant.State() != TenantActive {
		t.Fatalf("состояние = %q, ожидалось active", tenant.State())
	}
	users := tenant.Users()
	if len(users) != 1 || !users[0].Roles().Has(RoleClient) || users[0].Login() != "client-1" {
		t.Fatalf("пользователи = %+v", users)
	}
	events := tenant.DomainEvents()
	if len(events) != 2 {
		t.Fatalf("события = %d, ожидалось 2", len(events))
	}
	if events[0].Name() != EventTenantAccessChanged || events[1].Name() != EventAuditRecorded {
		t.Fatalf("имена событий = %q, %q", events[0].Name(), events[1].Name())
	}
	journal := tenant.AuditJournal()
	if len(journal) != 1 || journal[0].Action() != ActionTenantCreated || journal[0].Actor() != "user-1" {
		t.Fatalf("журнал аудита = %+v", journal)
	}
}

// TestRegisterTenantValidation проверяет инвариант I1 и обязательность субъекта.
func TestRegisterTenantValidation(t *testing.T) {
	cases := map[string]func(*RegisterTenantParams){
		"пустое название":      func(p *RegisterTenantParams) { p.CompanyName = "   " },
		"пустое описание":      func(p *RegisterTenantParams) { p.Description = "" },
		"пустой идентификатор": func(p *RegisterTenantParams) { p.ID = "" },
		"пустой субъект":       func(p *RegisterTenantParams) { p.Actor = "" },
		"неизвестная роль": func(p *RegisterTenantParams) {
			p.User = &NewUser{ID: "user-1", Login: "client-1", Roles: RoleSet{roles: []Role{Role("admin")}}}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			params := validParams(t)
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
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	restored, err := RehydrateTenant(tenant.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateTenant: %v", err)
	}
	if restored.ID() != tenant.ID() || restored.CompanyName() != tenant.CompanyName() || restored.State() != tenant.State() {
		t.Fatalf("восстановленный тенант = %+v", restored)
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
	if len(restored.Users()) != 1 || len(restored.AuditJournal()) != 1 {
		t.Fatalf("восстановленное состояние = %+v", restored)
	}
}

// TestRehydrateTenantRejectsDuplicates проверяет инвариант «роль принадлежит
// ровно одному тенанту»: дублирующийся пользователь делает снимок некорректным.
func TestRehydrateTenantRejectsDuplicates(t *testing.T) {
	snapshot := TenantSnapshot{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик",
		State:       TenantActive,
		CreatedAt:   at,
		Users: []TenantUserSnapshot{
			{ID: "user-1", Login: "client-1", Roles: []Role{RoleClient}},
			{ID: "user-1", Login: "client-2", Roles: []Role{RoleClient}},
		},
	}
	if _, err := RehydrateTenant(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}

// TestRehydrateTenantRejectsForeignRole проверяет, что снимок с ролью, не
// принадлежащей тенанту (объявленной вне домена), отвергается.
func TestRehydrateTenantRejectsForeignRole(t *testing.T) {
	snapshot := TenantSnapshot{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик",
		State:       TenantActive,
		CreatedAt:   at,
		Users: []TenantUserSnapshot{
			{ID: "user-1", Login: "client-1", Roles: []Role{Role("tenant-2:client")}},
		},
	}
	if _, err := RehydrateTenant(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}

// TestAssignRoleKeepsPreviousSet проверяет неизменяемость набора ролей: изменение
// создаёт новый набор, прежний не меняется.
func TestAssignRoleKeepsPreviousSet(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	before := tenant.Users()[0].Roles().Roles()
	if err := tenant.AssignRole(change(), "user-1", RoleAdmin); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	// Прежняя копия набора не изменилась.
	if len(before) != 1 || before[0] != RoleClient {
		t.Fatalf("прежний набор изменился: %+v", before)
	}
	after := tenant.Users()[0].Roles()
	if !after.Has(RoleClient) || !after.Has(RoleAdmin) || after.Len() != 2 {
		t.Fatalf("новый набор = %+v", after.Roles())
	}
	if tenant.Version() != 2 {
		t.Fatalf("версия = %d, ожидалось 2", tenant.Version())
	}
}

// TestAssignRoleRejectedWhenDisabled проверяет инвариант I5: отключённый тенант
// не принимает новых назначений.
func TestAssignRoleRejectedWhenDisabled(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.Disable(change()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if err := tenant.AssignRole(change(), "user-1", RoleAdmin); !errors.Is(err, ErrTenantDisabled) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantDisabled", err)
	}
}

// TestRevokeRole проверяет отзыв роли и отказ при отсутствующей роли.
func TestRevokeRole(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.AssignRole(change(), "user-1", RoleAdmin); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if err := tenant.RevokeRole(change(), "user-1", RoleClient); err != nil {
		t.Fatalf("RevokeRole: %v", err)
	}
	if tenant.Users()[0].Roles().Has(RoleClient) {
		t.Fatal("роль не отозвана")
	}
	if err := tenant.RevokeRole(change(), "user-1", RoleClient); !errors.Is(err, ErrRoleNotAssigned) {
		t.Fatalf("ошибка = %v, ожидалась ErrRoleNotAssigned", err)
	}
}

// TestAssignRoleUnknownUser проверяет инвариант «роль принадлежит ровно одному
// тенанту»: назначение роли пользователю, которого нет в тенанте, отвергается.
func TestAssignRoleUnknownUser(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.AssignRole(change(), "ghost", RoleAdmin); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrUserNotFound", err)
	}
}

// TestDisableIdempotent проверяет идемпотентность отключения и запись аудита.
func TestDisableIdempotent(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.Disable(change()); err != nil {
		t.Fatalf("Disable #1: %v", err)
	}
	if err := tenant.Disable(change()); err != nil {
		t.Fatalf("Disable #2: %v", err)
	}
	disabled := 0
	for _, record := range tenant.AuditJournal() {
		if record.Action() == ActionTenantDisabled {
			disabled++
		}
	}
	if disabled != 1 {
		t.Fatalf("записей об отключении = %d, ожидалось 1", disabled)
	}
	if tenant.State() != TenantDisabled {
		t.Fatalf("состояние = %q", tenant.State())
	}
}

// TestDecideAccess проверяет решение доступа по ролям, отказ после отключения и
// ошибку пустого субъекта.
func TestDecideAccess(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	allowed, err := tenant.DecideAccess("user-1")
	if err != nil || !allowed {
		t.Fatalf("решение для члена тенанта = %v, %v", allowed, err)
	}
	allowed, err = tenant.DecideAccess("stranger")
	if err != nil || allowed {
		t.Fatalf("решение для постороннего = %v, %v", allowed, err)
	}
	if _, err := tenant.DecideAccess(""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("пустой субъект: ошибка = %v, ожидалась ErrEmptyID", err)
	}
	if err := tenant.Disable(change()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	allowed, err = tenant.DecideAccess("user-1")
	if err != nil || allowed {
		t.Fatalf("после отключения решение = %v, %v, ожидалось false", allowed, err)
	}
}

// TestAuditCorrectionAppends проверяет неизменяемость аудита: корректировка —
// новая запись со ссылкой на исходную, прежняя не меняется.
func TestAuditCorrectionAppends(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	original := tenant.AuditJournal()[0]
	if err := tenant.CorrectAudit(change(), original.ID(), original.Object(), OutcomeRejected); err != nil {
		t.Fatalf("CorrectAudit: %v", err)
	}
	journal := tenant.AuditJournal()
	if len(journal) != 2 {
		t.Fatalf("журнал = %d записей, ожидалось 2", len(journal))
	}
	if journal[0].Outcome() != OutcomeSucceeded || journal[0].IsCorrection() {
		t.Fatal("исходная запись изменена")
	}
	correction := journal[1]
	if !correction.IsCorrection() || correction.CorrectionOf() != original.ID() || correction.Outcome() != OutcomeRejected {
		t.Fatalf("корректирующая запись = %+v", correction)
	}
}

// TestAddUserRejectsDuplicate проверяет, что повторное добавление пользователя
// в тот же тенант отвергается доменом.
func TestAddUserRejectsDuplicate(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	err = tenant.AddUser(change(), NewUser{ID: "user-1", Login: "client-1", Roles: clientRoles(t)})
	if !errors.Is(err, ErrDuplicateUser) {
		t.Fatalf("ошибка = %v, ожидалась ErrDuplicateUser", err)
	}
}
