package command

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

func approvalCommand() SubmitForApproval {
	return SubmitForApproval{
		TenantID:     domain.TenantID("tenant-1"),
		RunID:        "run-1",
		MissionID:    "mission-1",
		CompanyID:    "company-1",
		DraftRef:     domain.DraftID("company-1@v1"),
		DraftVersion: 1,
		MaterialRef:  domain.MaterialID("company-1@v1"),
		RecipientRef: domain.RecipientID("contact-1"),
	}
}

func TestSubmitForApprovalCreatesDraftInQueue(t *testing.T) {
	kit := newTestKit(t)
	handler := NewSubmitForApprovalHandler(kit.clock, kit.uow)

	result, err := handler.Handle(context.Background(), approvalCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Repeated {
		t.Fatal("первая подача помечена повтором")
	}
	if result.DraftVersion != 1 {
		t.Fatalf("версия черновика = %d, ожидалась 1", result.DraftVersion)
	}

	campaign, err := kit.repo.FindByID(context.Background(), result.CampaignID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if campaign.TenantID() != domain.TenantID("tenant-1") {
		t.Fatalf("тенант кампании = %q", campaign.TenantID())
	}
	draft, ok := campaign.Draft(domain.DraftID("company-1@v1"))
	if !ok {
		t.Fatal("черновик не поставлен в очередь")
	}
	if draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("состояние черновика = %q, ожидалось ожидание утверждения", draft.State())
	}
	// Состав несёт только непрозрачные ссылки: сегмент — компания, получатель —
	// ссылка получателя; персональные данные через границу не проходят.
	spec := draft.Spec()
	if spec.Segment != domain.SegmentID("company-1") {
		t.Fatalf("сегмент = %q", spec.Segment)
	}
	if spec.Recipient != domain.RecipientID("contact-1") {
		t.Fatalf("получатель = %q", spec.Recipient)
	}
	if spec.Material != domain.MaterialID("company-1@v1") {
		t.Fatalf("материал = %q", spec.Material)
	}
}

func TestSubmitForApprovalIsIdempotentOnRepeat(t *testing.T) {
	kit := newTestKit(t)
	handler := NewSubmitForApprovalHandler(kit.clock, kit.uow)

	first, err := handler.Handle(context.Background(), approvalCommand())
	if err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	savesAfterFirst := kit.repo.saves
	eventsAfterFirst := len(kit.outbox.events)

	second, err := handler.Handle(context.Background(), approvalCommand())
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if !second.Repeated {
		t.Fatal("повтор не помечен как повтор")
	}
	if second.CampaignID != first.CampaignID || second.DraftVersion != first.DraftVersion {
		t.Fatalf("повтор изменил идентичность: %+v != %+v", second, first)
	}
	if kit.repo.saves != savesAfterFirst {
		t.Fatalf("повтор записал состояние заново: сохранений %d", kit.repo.saves)
	}
	if len(kit.outbox.events) != eventsAfterFirst {
		t.Fatal("повтор породил второе событие")
	}
	campaign, _ := kit.repo.FindByID(context.Background(), first.CampaignID)
	if len(campaign.Drafts()) != 1 {
		t.Fatalf("в кампании %d черновиков, ожидался 1", len(campaign.Drafts()))
	}
}

func TestSubmitForApprovalRejectsConflictingComposition(t *testing.T) {
	kit := newTestKit(t)
	handler := NewSubmitForApprovalHandler(kit.clock, kit.uow)

	if _, err := handler.Handle(context.Background(), approvalCommand()); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	conflicting := approvalCommand()
	conflicting.MaterialRef = domain.MaterialID("company-1@v9")
	if _, err := handler.Handle(context.Background(), conflicting); !errors.Is(err, ErrApprovalRejected) {
		t.Fatalf("ожидалась ErrApprovalRejected, получено %v", err)
	}
}

func TestSubmitForApprovalRejectsInvalidForm(t *testing.T) {
	cases := map[string]func(*SubmitForApproval){
		"без тенанта":   func(c *SubmitForApproval) { c.TenantID = "" },
		"без запуска":   func(c *SubmitForApproval) { c.RunID = "" },
		"без компании":  func(c *SubmitForApproval) { c.CompanyID = "" },
		"без черновика": func(c *SubmitForApproval) { c.DraftRef = "" },
		"без материала": func(c *SubmitForApproval) { c.MaterialRef = "" },
		"нулевая версия": func(c *SubmitForApproval) {
			c.DraftVersion = 0
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			kit := newTestKit(t)
			handler := NewSubmitForApprovalHandler(kit.clock, kit.uow)
			cmd := approvalCommand()
			mutate(&cmd)

			if _, err := handler.Handle(context.Background(), cmd); !errors.Is(err, ErrApprovalRejected) {
				t.Fatalf("ожидалась ErrApprovalRejected, получено %v", err)
			}
			if kit.uow.commits != 0 || kit.repo.saves != 0 {
				t.Fatal("некорректная форма изменила состояние")
			}
		})
	}
}

func TestSubmitForApprovalFallsBackToCompanyRecipient(t *testing.T) {
	kit := newTestKit(t)
	handler := NewSubmitForApprovalHandler(kit.clock, kit.uow)

	cmd := approvalCommand()
	cmd.RecipientRef = ""
	result, err := handler.Handle(context.Background(), cmd)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	campaign, _ := kit.repo.FindByID(context.Background(), result.CampaignID)
	draft, _ := campaign.Draft(domain.DraftID("company-1@v1"))
	if draft.Spec().Recipient != domain.RecipientID("company-1") {
		t.Fatalf("получатель по умолчанию = %q, ожидалась компания", draft.Spec().Recipient)
	}
}
