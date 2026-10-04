package domain

import (
	"errors"
	"testing"
)

// TestRoleSetHasRight проверяет политику least-privilege: права следуют только из
// назначенных ролей.
func TestRoleSetHasRight(t *testing.T) {
	admin, err := NewRoleSet(RoleAdmin)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	client, err := NewRoleSet(RoleClient)
	if err != nil {
		t.Fatalf("NewRoleSet: %v", err)
	}
	if !RoleSetHasRight(admin, RightManageTenants) || !RoleSetHasRight(admin, RightManageRoles) {
		t.Fatal("роль управления платформой не даёт прав управления")
	}
	if RoleSetHasRight(client, RightManageTenants) || RoleSetHasRight(client, RightManageRoles) {
		t.Fatal("клиент не должен получать права управления")
	}
	if !RoleSetHasRight(client, RightUsePortal) {
		t.Fatal("клиент должен иметь право самообслуживания")
	}
	if RoleSetHasRight(RoleSet{}, RightUsePortal) {
		t.Fatal("пустой набор не должен давать прав")
	}
}

// TestTenantIsolationRequiresTenant проверяет политику tenant-isolation: действие
// без контекста тенанта отвергается, а с контекстом даёт запись аудита.
func TestTenantIsolationRequiresTenant(t *testing.T) {
	if _, err := TenantIsolation(TenantIsolationParams{
		Actor: "admin-1", Action: ActionTenantCreated, At: at, AuditRecordID: "audit-1",
	}); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmptyID", err)
	}
	record, err := TenantIsolation(TenantIsolationParams{
		TenantID: "tenant-1", Actor: "admin-1", Action: ActionTenantCreated,
		Object: "tenant-1", At: at, AuditRecordID: "audit-1", Outcome: OutcomeSucceeded,
	})
	if err != nil {
		t.Fatalf("TenantIsolation: %v", err)
	}
	if record.Action() != ActionTenantCreated || record.Actor() != "admin-1" {
		t.Fatalf("запись аудита = %+v", record)
	}
}

// TestAccessDecisionSnapshot проверяет снимок правил: состояние, монотонную
// версию и список «актор → роли».
func TestAccessDecisionSnapshot(t *testing.T) {
	tenant, err := RegisterTenant(validParams(t))
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.AssignRole(change(), "user-1", RoleAdmin); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	snapshot := tenant.AccessDecisionSnapshot(at)
	if snapshot.TenantID != "tenant-1" || snapshot.State != TenantActive || snapshot.Version != 2 {
		t.Fatalf("снимок = %+v", snapshot)
	}
	if len(snapshot.Assignments) != 1 || snapshot.Assignments[0].Actor != "user-1" || len(snapshot.Assignments[0].Roles) != 2 {
		t.Fatalf("правила снимка = %+v", snapshot.Assignments)
	}
	if err := tenant.Disable(change()); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	after := tenant.AccessDecisionSnapshot(at)
	if after.State != TenantDisabled || after.Version <= snapshot.Version {
		t.Fatalf("снимок после отключения = %+v", after)
	}
}

// TestRehydrateRejectsBrokenAuditCorrection проверяет, что снимок с
// корректировкой, ссылающейся на несуществующую запись, отвергается.
func TestRehydrateRejectsBrokenAuditCorrection(t *testing.T) {
	snapshot := TenantSnapshot{
		ID:          "tenant-1",
		CompanyName: "ООО Ромашка",
		Description: "Поставщик",
		State:       TenantActive,
		CreatedAt:   at,
		Audit: []AuditRecordSnapshot{
			{ID: "audit-2", Actor: "admin-1", Action: ActionAuditCorrected, At: at,
				Outcome: OutcomeRejected, CorrectionOf: "audit-missing"},
		},
	}
	if _, err := RehydrateTenant(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}
