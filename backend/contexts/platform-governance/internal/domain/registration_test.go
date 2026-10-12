package domain

import (
	"errors"
	"testing"
)

// identity — идентифицирующие данные компании для клиентской регистрации.
func identity(p *RegisterTenantParams) {
	p.INN = "7701234567"
	p.CompanyEmail = "hello@romashka.ru"
	p.Website = "https://romashka.ru"
	p.LegalData = "ООО «Ромашка», ИНН 7701234567"
}

func TestRegisterTenantPending(t *testing.T) {
	params := validParams(t)
	identity(&params)
	params.Pending = true
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if tenant.State() != TenantPending {
		t.Fatalf("состояние = %q, ожидалось pending", tenant.State())
	}
	if allowed, _ := tenant.DecideAccess("user-1"); allowed {
		t.Fatal("проверяемый тенант не должен выдавать доступ")
	}
	if tenant.INN() != "7701234567" || tenant.CompanyEmail() != "hello@romashka.ru" {
		t.Fatalf("идентифицирующие данные не сохранены: %+v", tenant)
	}
}

func TestApproveRegistration(t *testing.T) {
	params := validParams(t)
	identity(&params)
	params.Pending = true
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.ApproveRegistration(change()); err != nil {
		t.Fatalf("ApproveRegistration: %v", err)
	}
	if tenant.State() != TenantActive {
		t.Fatalf("состояние = %q, ожидалось active", tenant.State())
	}
	if allowed, _ := tenant.DecideAccess("user-1"); !allowed {
		t.Fatal("активный тенант должен выдавать доступ члену")
	}
}

func TestRejectRegistrationRequiresReason(t *testing.T) {
	params := validParams(t)
	identity(&params)
	params.Pending = true
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.RejectRegistration(change(), "   "); !errors.Is(err, ErrRegistrationReasonRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrRegistrationReasonRequired", err)
	}
	if err := tenant.RejectRegistration(change(), "компания уже зарегистрирована"); err != nil {
		t.Fatalf("RejectRegistration: %v", err)
	}
	if tenant.State() != TenantRejected {
		t.Fatalf("состояние = %q, ожидалось rejected", tenant.State())
	}
	if tenant.RegistrationReason() != "компания уже зарегистрирована" {
		t.Fatalf("причина = %q", tenant.RegistrationReason())
	}
}

func TestProfileLifecycle(t *testing.T) {
	params := validParams(t)
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if tenant.ProfileStatus() != ProfileEmpty {
		t.Fatalf("начальное состояние профиля = %q", tenant.ProfileStatus())
	}

	profile := NewProfile("Описание", "Ищем интеграторов", []Service{
		{Name: "База контактов", Short: "Готовая база", Full: "Полное описание базы контактов"},
	})
	if err := tenant.SaveProfile(profile); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := tenant.SubmitProfile(); err != nil {
		t.Fatalf("SubmitProfile: %v", err)
	}
	if tenant.ProfileStatus() != ProfilePending {
		t.Fatalf("состояние = %q, ожидалось pending", tenant.ProfileStatus())
	}
	if err := tenant.SaveProfile(profile); !errors.Is(err, ErrProfilePending) {
		t.Fatalf("правка на согласовании должна быть запрещена, err = %v", err)
	}
	if err := tenant.ApproveProfile(change()); err != nil {
		t.Fatalf("ApproveProfile: %v", err)
	}
	if tenant.ProfileStatus() != ProfileActive {
		t.Fatalf("состояние = %q, ожидалось active", tenant.ProfileStatus())
	}
}

func TestProfileRequiresCompleteBeforeSubmit(t *testing.T) {
	params := validParams(t)
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	if err := tenant.SaveProfile(NewProfile("", "", nil)); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := tenant.SubmitProfile(); !errors.Is(err, ErrProfileIncomplete) {
		t.Fatalf("ошибка = %v, ожидалась ErrProfileIncomplete", err)
	}
}

func TestPrepareUserStoresPasswordHash(t *testing.T) {
	params := validParams(t)
	params.User.PasswordHash = "salt:hash"
	tenant, err := RegisterTenant(params)
	if err != nil {
		t.Fatalf("RegisterTenant: %v", err)
	}
	users := tenant.Users()
	if len(users) != 1 || users[0].PasswordHash() != "salt:hash" {
		t.Fatalf("хеш пароля не сохранён: %+v", users)
	}
}
