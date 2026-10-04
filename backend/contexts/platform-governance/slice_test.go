package platformgovernance_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/command"
	"lead-gen/backend/contexts/platform-governance/internal/app/query"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// fixedClock — детерминированные часы теста.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

// seqIDs — детерминированный генератор идентификаторов.
type seqIDs struct{ n int }

func (s *seqIDs) next(prefix string) string {
	s.n++
	return prefix + "-" + strconv.Itoa(s.n)
}

func (s *seqIDs) NewTenantID() domain.TenantID { return domain.TenantID(s.next("tenant")) }
func (s *seqIDs) NewUserID() domain.UserID     { return domain.UserID(s.next("user")) }
func (s *seqIDs) NewEventID() domain.EventID   { return domain.EventID(s.next("evt")) }
func (s *seqIDs) NewAuditRecordID() domain.AuditRecordID {
	return domain.AuditRecordID(s.next("audit"))
}
func (s *seqIDs) NewRequestID() string { return s.next("req") }
func (s *seqIDs) NewOfferID() string   { return s.next("offer") }

// newSlice собирает срез с платформенным администратором admin-1 и
// детерминированными часами и идентификаторами.
func newSlice(t *testing.T) *platformgovernance.Slice {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants: adminGrants,
		Clock:  fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		IDs:    &seqIDs{},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	return slice
}

// createTenant создаёт тенант с первым клиентом через сценарий.
func createTenant(t *testing.T, slice *platformgovernance.Slice, commandID, tenantID string) command.CreateTenantResult {
	t.Helper()
	result, err := slice.CreateTenant.Handle(context.Background(), command.CreateTenant{
		Actor:       "admin-1",
		CommandID:   commandID,
		TenantID:    tenantID,
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
		UserID:      "user-1",
		Login:       "client-1",
		Roles:       []string{string(domain.RoleClient)},
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	return result
}

// TestCreateTenantPersistsAndPublishes проверяет создание тенанта: агрегат
// сохранён, события (аудит, изменение доступа, снимок правил) попали в outbox.
func TestCreateTenantPersistsAndPublishes(t *testing.T) {
	slice := newSlice(t)
	result := createTenant(t, slice, "cmd-1", "tenant-1")
	if !result.Created || result.TenantID != "tenant-1" {
		t.Fatalf("результат = %+v", result)
	}
	tenant, err := slice.Repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if tenant.State() != domain.TenantActive || len(tenant.AuditJournal()) != 1 {
		t.Fatalf("состояние агрегата = %+v", tenant)
	}
	if slice.Journal == nil || slice.Journal.PendingCount() == 0 {
		t.Fatal("события не попали в outbox")
	}
}

// TestCreateTenantIdempotent проверяет идемпотентность по (tenant_id, command_id):
// повтор команды второй тенант не создаёт.
func TestCreateTenantIdempotent(t *testing.T) {
	slice := newSlice(t)
	if first := createTenant(t, slice, "cmd-1", "tenant-1"); !first.Created {
		t.Fatal("первый вызов должен создать тенант")
	}
	if second := createTenant(t, slice, "cmd-1", "tenant-1"); second.Created {
		t.Fatal("повтор команды не должен создавать эффект")
	}
}

// TestCreateTenantForbidden проверяет отказ без права актора.
func TestCreateTenantForbidden(t *testing.T) {
	slice := newSlice(t)
	_, err := slice.CreateTenant.Handle(context.Background(), command.CreateTenant{
		Actor: "stranger", CommandID: "cmd-1", TenantID: "tenant-1",
		CompanyName: "ООО Ромашка", Description: "Описание",
	})
	if !errors.Is(err, app.ErrForbidden) {
		t.Fatalf("ошибка = %v, ожидалась ErrForbidden", err)
	}
	if _, err := slice.Repository.FindByID(context.Background(), "tenant-1"); !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("тенант создан без права: %v", err)
	}
}

// TestCreateTenantRequiresCommandID проверяет, что команда без ключа
// идемпотентности отклоняется.
func TestCreateTenantRequiresCommandID(t *testing.T) {
	slice := newSlice(t)
	if _, err := slice.CreateTenant.Handle(context.Background(), command.CreateTenant{
		Actor: "admin-1", TenantID: "tenant-1", CompanyName: "ООО", Description: "Описание",
	}); !errors.Is(err, app.ErrMissingCommandID) {
		t.Fatalf("ошибка = %v, ожидалась ErrMissingCommandID", err)
	}
}

// TestAccessLifecycleScenario проверяет сквозной сценарий: создание, назначение
// роли, отзыв, отключение и запись аудита по каждому значимому шагу.
func TestAccessLifecycleScenario(t *testing.T) {
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")
	if err := slice.AssignRole.Handle(context.Background(), command.AssignRole{
		Actor: "admin-1", CommandID: "cmd-assign", TenantID: "tenant-1", UserID: "user-1", Role: string(domain.RoleAdmin),
	}); err != nil {
		t.Fatalf("AssignRole: %v", err)
	}
	if err := slice.RevokeRole.Handle(context.Background(), command.RevokeRole{
		Actor: "admin-1", CommandID: "cmd-revoke", TenantID: "tenant-1", UserID: "user-1", Role: string(domain.RoleClient),
	}); err != nil {
		t.Fatalf("RevokeRole: %v", err)
	}
	if err := slice.DisableTenant.Handle(context.Background(), command.DisableTenant{
		Actor: "admin-1", CommandID: "cmd-disable", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	tenant, err := slice.Repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	actions := map[domain.AuditAction]int{}
	for _, record := range tenant.AuditJournal() {
		actions[record.Action()]++
	}
	for _, want := range []domain.AuditAction{
		domain.ActionTenantCreated, domain.ActionRoleAssigned, domain.ActionRoleRevoked, domain.ActionTenantDisabled,
	} {
		if actions[want] == 0 {
			t.Fatalf("в аудите нет действия %q: %+v", want, actions)
		}
	}
}

// TestAssignRoleIdempotentByCommandKey проверяет, что повтор команды с тем же
// ключом не поднимает версию правил повторно.
func TestAssignRoleIdempotentByCommandKey(t *testing.T) {
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")
	assign := command.AssignRole{
		Actor: "admin-1", CommandID: "cmd-assign", TenantID: "tenant-1", UserID: "user-1", Role: string(domain.RoleAdmin),
	}
	if err := slice.AssignRole.Handle(context.Background(), assign); err != nil {
		t.Fatalf("AssignRole #1: %v", err)
	}
	afterFirst, err := slice.Repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if err := slice.AssignRole.Handle(context.Background(), assign); err != nil {
		t.Fatalf("AssignRole #2: %v", err)
	}
	afterSecond, err := slice.Repository.FindByID(context.Background(), "tenant-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if afterFirst.Version() != afterSecond.Version() {
		t.Fatalf("версия поднялась повторно: %d → %d", afterFirst.Version(), afterSecond.Version())
	}
}

// TestDecideAccessCanAct проверяет решение доступа и форму порта потребителя.
func TestDecideAccessCanAct(t *testing.T) {
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")

	allowed, err := slice.DecideAccess.CanAct(context.Background(), "user-1", "tenant-1")
	if err != nil || !allowed {
		t.Fatalf("CanAct члена тенанта = %v, %v", allowed, err)
	}
	allowed, err = slice.DecideAccess.CanAct(context.Background(), "stranger", "tenant-1")
	if err != nil || allowed {
		t.Fatalf("CanAct постороннего = %v, %v", allowed, err)
	}
	if _, err := slice.DecideAccess.CanAct(context.Background(), "", "tenant-1"); !errors.Is(err, app.ErrMissingActor) {
		t.Fatalf("пустой актор: ошибка = %v, ожидалась ErrMissingActor", err)
	}
	if _, err := slice.DecideAccess.Handle(context.Background(), query.DecideAccess{TenantID: "tenant-1", Actor: "user-1"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
}

// TestDecideAccessAfterDisable проверяет, что после отключения решение
// отрицательное.
func TestDecideAccessAfterDisable(t *testing.T) {
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")
	if err := slice.DisableTenant.Handle(context.Background(), command.DisableTenant{
		Actor: "admin-1", CommandID: "cmd-disable", TenantID: "tenant-1",
	}); err != nil {
		t.Fatalf("DisableTenant: %v", err)
	}
	allowed, err := slice.DecideAccess.CanAct(context.Background(), "user-1", "tenant-1")
	if err != nil || allowed {
		t.Fatalf("CanAct после отключения = %v, %v, ожидалось false", allowed, err)
	}
}

// TestAuditJournal проверяет чтение журнала аудита своего тенанта и отсутствие
// доступа к чужому.
func TestAuditJournal(t *testing.T) {
	slice := newSlice(t)
	createTenant(t, slice, "cmd-create", "tenant-1")

	entries, err := slice.AuditJournal.Handle(context.Background(), query.AuditJournal{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "tenant-created" || entries[0].Actor != "admin-1" {
		t.Fatalf("журнал = %+v", entries)
	}
	if _, err := slice.AuditJournal.Handle(context.Background(), query.AuditJournal{TenantID: "tenant-2"}); !errors.Is(err, ports.ErrTenantNotFound) {
		t.Fatalf("чужой тенант: ошибка = %v, ожидалась ErrTenantNotFound", err)
	}
	if _, err := slice.AuditJournal.Handle(context.Background(), query.AuditJournal{}); !errors.Is(err, domain.ErrEmptyID) {
		t.Fatalf("пустой тенант: ошибка = %v, ожидалась ErrEmptyID", err)
	}
}

// TestRegisterClientPublishes проверяет, что самообслуживание клиента сохраняет
// тенант и публикует события без платформенного гранта.
func TestRegisterClientPublishes(t *testing.T) {
	slice := newSlice(t)
	result, err := slice.RegisterClient.Handle(context.Background(), command.RegisterClient{
		CompanyName: "ООО Ромашка",
		Description: "Поставщик промышленного оборудования",
	})
	if err != nil {
		t.Fatalf("RegisterClient: %v", err)
	}
	if result.TenantID == "" || result.UserID == "" {
		t.Fatalf("результат = %+v", result)
	}
	if _, err := slice.Repository.FindByID(context.Background(), domain.TenantID(result.TenantID)); err != nil {
		t.Fatalf("тенант не сохранён: %v", err)
	}
	if slice.Journal.PendingCount() == 0 {
		t.Fatal("регистрация не опубликовала события")
	}
}
