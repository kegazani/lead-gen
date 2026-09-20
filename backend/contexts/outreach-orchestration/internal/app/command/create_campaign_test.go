package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func TestCreateCampaignDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.access.allowed = false

	_, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), createCommand(t))
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
	if kit.repo.saves != 0 {
		t.Fatalf("без права доступа кампания не должна сохраняться, saves=%d", kit.repo.saves)
	}
	if len(kit.outbox.events) != 0 {
		t.Fatalf("без права доступа событий быть не должно, got %d", len(kit.outbox.events))
	}
}

func TestCreateCampaignPersistsWithinTransaction(t *testing.T) {
	kit := newTestKit(t)

	result, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), createCommand(t))
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !result.Created {
		t.Fatal("первая команда должна создать кампанию")
	}
	if result.State != domain.CampaignStateDraft {
		t.Fatalf("состояние кампании: got %q, want %q", result.State, domain.CampaignStateDraft)
	}
	if kit.repo.saves != 1 || kit.uow.commits != 1 {
		t.Fatalf("ожидались одна запись и один commit, saves=%d commits=%d", kit.repo.saves, kit.uow.commits)
	}
	kit.campaign(t)
}

func TestCreateCampaignRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	handler := NewCreateCampaignHandler(kit.access, kit.uow)
	command := createCommand(t)

	if _, err := handler.Handle(context.Background(), command); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	result, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if result.Created {
		t.Fatal("повтор команды не должен создавать вторую кампанию")
	}
	if kit.repo.saves != 1 {
		t.Fatalf("ожидалась одна запись, saves=%d", kit.repo.saves)
	}
}

func TestCreateCampaignRollsBackOnOutboxFailure(t *testing.T) {
	kit := newTestKit(t)
	kit.uow.txOutboxErr = errors.New("outbox недоступен")

	_, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), createCommand(t))
	if err == nil {
		t.Fatal("ожидалась ошибка записи событий")
	}
	if kit.uow.rollbacks != 1 {
		t.Fatalf("ожидался откат транзакции, rollbacks=%d", kit.uow.rollbacks)
	}
	if len(kit.repo.campaigns) != 0 {
		t.Fatal("при откате кампания не должна сохраняться")
	}
	if len(kit.outbox.events) != 0 {
		t.Fatal("при откате события не должны публиковаться")
	}
}

func TestCreateCampaignPropagatesAccessInfrastructureError(t *testing.T) {
	kit := newTestKit(t)
	kit.access.err = errors.New("governance недоступен")

	_, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), createCommand(t))
	if err == nil || errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ошибка инфраструктуры доступа должна пробрасываться как есть, got %v", err)
	}
	if kit.repo.saves != 0 {
		t.Fatal("при ошибке проверки доступа кампания не создаётся")
	}
}

func TestCreateCampaignRejectsForeignTenantCampaign(t *testing.T) {
	kit := newTestKit(t)
	if _, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), createCommand(t)); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	command := createCommand(t)
	command.TenantID = domain.TenantID("tenant-2")
	_, err := NewCreateCampaignHandler(kit.access, kit.uow).Handle(context.Background(), command)
	if !errors.Is(err, ErrCampaignTenantMismatch) {
		t.Fatalf("ожидался отказ по тенанту, получено %v", err)
	}
	if kit.repo.saves != 1 {
		t.Fatalf("вторая запись недопустима, saves=%d", kit.repo.saves)
	}
}
