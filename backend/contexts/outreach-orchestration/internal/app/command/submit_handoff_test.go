package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// handoffCommand — команда передачи с непустым составом из двух писем.
func handoffCommand() SubmitHandoff {
	return SubmitHandoff{
		RequestID: "req-1",
		TenantID:  "tenant-1",
		Actor:     "operator-1",
		Name:      "Передача заявки req-1",
		Items: []SubmitHandoffItem{
			{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-a"},
			{LeadID: "lead-b", RecipientRef: "recipient-b", MaterialRef: "material-b"},
		},
	}
}

func newHandoffHandler(kit *testKit) *SubmitHandoffHandler {
	return NewSubmitHandoffHandler(kit.access, kit.clock, kit.uow)
}

// handoffCampaign достаёт кампанию заявки req-1 из тестового репозитория.
func handoffCampaign(t *testing.T, kit *testKit) *domain.Campaign {
	t.Helper()
	campaign, err := kit.repo.FindByID(context.Background(), domain.CampaignID("handoff-req-1"))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	return campaign
}

func TestSubmitHandoffCreatesCampaignAndPendingDrafts(t *testing.T) {
	kit := newTestKit(t)

	result, err := newHandoffHandler(kit).Handle(context.Background(), handoffCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.Repeated {
		t.Fatal("первая передача не должна быть повторной")
	}
	if result.Accepted != 2 || result.Skipped != 0 {
		t.Fatalf("исход = %+v, ожидалось accepted=2 skipped=0", result)
	}
	if result.CampaignID != domain.CampaignID("handoff-req-1") {
		t.Fatalf("campaign_id = %q", result.CampaignID)
	}

	campaign := handoffCampaign(t, kit)
	if campaign.ID() != domain.CampaignID("handoff-req-1") || campaign.TenantID() != domain.TenantID("tenant-1") {
		t.Fatalf("кампания = %s тенант %s", campaign.ID(), campaign.TenantID())
	}
	if campaign.State() != domain.CampaignStateActive {
		t.Fatalf("состояние кампании = %q, ожидалось active (есть черновики)", campaign.State())
	}
	if len(campaign.Drafts()) != 2 {
		t.Fatalf("черновиков = %d, ожидалось 2", len(campaign.Drafts()))
	}
	pending := campaign.PendingApproval()
	if len(pending) != 2 {
		t.Fatalf("черновиков в ожидании утверждения = %d, ожидалось 2", len(pending))
	}
	for _, draft := range pending {
		spec := draft.Spec()
		if !spec.Channel.Equals(mustEmailChannel(t)) {
			t.Fatalf("канал черновика %s = %q, ожидался email", draft.ID(), spec.Channel)
		}
		// draft_id — ссылка получателя; сегментом служит соответствующий лид передачи.
		wantSegment := domain.SegmentID(strings.Replace(string(draft.ID()), "recipient-", "lead-", 1))
		if spec.Segment != wantSegment {
			t.Fatalf("сегмент черновика %s = %q, ожидался %q", draft.ID(), spec.Segment, wantSegment)
		}
	}
	if got := countApprovedEvents(kit.outbox.events); got != 0 {
		t.Fatalf("при приёме передачи исполнение не запускается: action-approved %d", got)
	}
}

func TestSubmitDeliveryHandoffRejectsEditorialPurposeBeforeWrites(t *testing.T) {
	kit := newTestKit(t)
	cmd := handoffCommand()
	cmd.ExecutionPurpose = "editorial"
	cmd.Items[0].RecipientRef = ""
	if _, err := newHandoffHandler(kit).Handle(context.Background(), cmd); !errors.Is(err, ErrHandoffRejected) {
		t.Fatalf("editorial purpose must not fall through to delivery: %v", err)
	}
	if kit.repo.saves != 0 || len(kit.repo.campaigns) != 0 {
		t.Fatal("delivery campaign was created for editorial request")
	}
	if len(kit.outbox.events) != 0 {
		t.Fatalf("delivery/outbox events were emitted: %d", len(kit.outbox.events))
	}
}

func TestSubmitHandoffRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	handler := newHandoffHandler(kit)

	if _, err := handler.Handle(context.Background(), handoffCommand()); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	result, err := handler.Handle(context.Background(), handoffCommand())
	if err != nil {
		t.Fatalf("повторный Handle: %v", err)
	}
	if !result.Repeated {
		t.Fatal("повтор по request_id должен быть отмечен Repeated")
	}
	if result.Accepted != 0 || result.Skipped != 2 {
		t.Fatalf("исход повтора = %+v, ожидалось accepted=0 skipped=2", result)
	}
	if len(handoffCampaign(t, kit).Drafts()) != 2 {
		t.Fatalf("повтор не должен дублировать черновики: %d", len(handoffCampaign(t, kit).Drafts()))
	}
	if kit.repo.saves != 1 {
		t.Fatalf("повтор без новых черновиков не должен писать состояние: saves=%d", kit.repo.saves)
	}
}

func TestSubmitHandoffEmptyCompositionCreatesNothing(t *testing.T) {
	kit := newTestKit(t)

	command := handoffCommand()
	command.Items = nil
	result, err := newHandoffHandler(kit).Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.CampaignID != "" || result.Accepted != 0 || result.Skipped != 0 || result.Repeated {
		t.Fatalf("исход пустого состава = %+v, ожидалась пустота", result)
	}
	if len(kit.repo.campaigns) != 0 || kit.repo.saves != 0 {
		t.Fatalf("пустой состав не создаёт кампанию: campaigns=%d saves=%d", len(kit.repo.campaigns), kit.repo.saves)
	}
	if len(kit.outbox.events) != 0 {
		t.Fatalf("пустой состав не порождает событий: %d", len(kit.outbox.events))
	}
}

func TestSubmitHandoffPartialRepeatKeepsExistingDrafts(t *testing.T) {
	kit := newTestKit(t)
	handler := newHandoffHandler(kit)

	if _, err := handler.Handle(context.Background(), handoffCommand()); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}

	partial := handoffCommand()
	partial.Items = partial.Items[:1] // передан неполный состав: только lead-a
	result, err := handler.Handle(context.Background(), partial)
	if err != nil {
		t.Fatalf("частичный повтор: %v", err)
	}
	if !result.Repeated || result.Accepted != 0 || result.Skipped != 1 {
		t.Fatalf("исход частичного повтора = %+v", result)
	}
	campaign := handoffCampaign(t, kit)
	if len(campaign.Drafts()) != 2 {
		t.Fatalf("частичный повтор удалил черновики: %d", len(campaign.Drafts()))
	}
	if _, ok := campaign.Draft(domain.DraftID("recipient-b")); !ok {
		t.Fatal("черновик recipient-b, отсутствовавший в частичном повторе, должен сохраниться")
	}
}

func TestSubmitHandoffRepeatAddsNewDraftWithoutDuplicating(t *testing.T) {
	kit := newTestKit(t)
	handler := newHandoffHandler(kit)

	if _, err := handler.Handle(context.Background(), handoffCommand()); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}

	extended := handoffCommand()
	extended.Items = append(extended.Items, SubmitHandoffItem{
		LeadID: "lead-c", RecipientRef: "recipient-c", MaterialRef: "material-c",
	})
	result, err := handler.Handle(context.Background(), extended)
	if err != nil {
		t.Fatalf("повтор с новым письмом: %v", err)
	}
	if !result.Repeated || result.Accepted != 1 || result.Skipped != 2 {
		t.Fatalf("исход = %+v, ожидалось repeated/1/2", result)
	}
	if len(handoffCampaign(t, kit).Drafts()) != 3 {
		t.Fatalf("черновиков = %d, ожидалось 3", len(handoffCampaign(t, kit).Drafts()))
	}
}

func TestSubmitHandoffRejectsInvalidCommand(t *testing.T) {
	cases := map[string]SubmitHandoff{
		"пустой request_id": func() SubmitHandoff { c := handoffCommand(); c.RequestID = "  "; return c }(),
		"пустой tenant_id":  func() SubmitHandoff { c := handoffCommand(); c.TenantID = ""; return c }(),
		"письмо без lead": func() SubmitHandoff {
			c := handoffCommand()
			c.Items = []SubmitHandoffItem{{RecipientRef: "r", MaterialRef: "m"}}
			return c
		}(),
		"письмо без получателя": func() SubmitHandoff {
			c := handoffCommand()
			c.Items = []SubmitHandoffItem{{LeadID: "l", MaterialRef: "m"}}
			return c
		}(),
		"письмо без материала": func() SubmitHandoff {
			c := handoffCommand()
			c.Items = []SubmitHandoffItem{{LeadID: "l", RecipientRef: "r"}}
			return c
		}(),
	}

	for name, command := range cases {
		t.Run(name, func(t *testing.T) {
			kit := newTestKit(t)
			_, err := newHandoffHandler(kit).Handle(context.Background(), command)
			if !errors.Is(err, ErrHandoffRejected) {
				t.Fatalf("ошибка = %v, ожидалась ErrHandoffRejected", err)
			}
			if len(kit.repo.campaigns) != 0 {
				t.Fatal("некорректная команда не должна создавать кампанию")
			}
		})
	}
}

func TestSubmitHandoffRejectsConflictingDraftComposition(t *testing.T) {
	kit := newTestKit(t)
	handler := newHandoffHandler(kit)

	if _, err := handler.Handle(context.Background(), handoffCommand()); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}

	changed := handoffCommand()
	changed.Items = []SubmitHandoffItem{{LeadID: "lead-a", RecipientRef: "recipient-a", MaterialRef: "material-z"}}
	_, err := handler.Handle(context.Background(), changed)
	if !errors.Is(err, ErrHandoffRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrHandoffRejected", err)
	}
	campaign := handoffCampaign(t, kit)
	if draft, ok := campaign.Draft(domain.DraftID("recipient-a")); !ok || draft.Spec().Material != domain.MaterialID("material-a") {
		t.Fatalf("конфликтующий повтор изменил существующий черновик: %+v", draft)
	}
}

func TestSubmitHandoffRejectsForeignTenantCampaign(t *testing.T) {
	kit := newTestKit(t)
	handler := newHandoffHandler(kit)

	if _, err := handler.Handle(context.Background(), handoffCommand()); err != nil {
		t.Fatalf("первый Handle: %v", err)
	}

	foreign := handoffCommand()
	foreign.TenantID = "tenant-2"
	_, err := handler.Handle(context.Background(), foreign)
	if !errors.Is(err, ErrHandoffRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrHandoffRejected (чужой тенант)", err)
	}
}

func TestSubmitHandoffDeniesWithoutAccessWhenActorNamed(t *testing.T) {
	kit := newTestKit(t)
	kit.access.allowed = false

	_, err := newHandoffHandler(kit).Handle(context.Background(), handoffCommand())
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ошибка = %v, ожидался отказ доступа", err)
	}
	if len(kit.repo.campaigns) != 0 {
		t.Fatal("без права доступа кампания не создаётся")
	}
}

func TestSubmitHandoffWithoutActorSkipsOperatorAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.access.allowed = false // пустой актор — машинная передача без оператора

	command := handoffCommand()
	command.Actor = ""
	result, err := newHandoffHandler(kit).Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("Handle без актора: %v", err)
	}
	if result.Accepted != 2 {
		t.Fatalf("исход = %+v, ожидалось accepted=2", result)
	}
	if kit.access.calls != 0 {
		t.Fatalf("при пустом акторе проверка права оператора не выполняется: calls=%d", kit.access.calls)
	}
}

func TestSubmitHandoffRollsBackOnStorageFailure(t *testing.T) {
	kit := newTestKit(t)
	kit.uow.txOutboxErr = errors.New("outbox недоступен")

	_, err := newHandoffHandler(kit).Handle(context.Background(), handoffCommand())
	if err == nil {
		t.Fatal("ожидалась ошибка записи событий")
	}
	if errors.Is(err, ErrHandoffRejected) {
		t.Fatal("сбой хранилища — недоступность, а не доменный отказ")
	}
	if kit.uow.rollbacks != 1 {
		t.Fatalf("ожидался откат транзакции, rollbacks=%d", kit.uow.rollbacks)
	}
	if len(kit.repo.campaigns) != 0 {
		t.Fatal("частично созданное состояние не должно фиксироваться")
	}
	if len(kit.outbox.events) != 0 {
		t.Fatal("при откате события не публикуются")
	}
}

func TestSubmitHandoffPropagatesStorageErrorOnLoad(t *testing.T) {
	kit := newTestKit(t)
	kit.repo.findErr = errors.New("хранилище недоступно")

	_, err := newHandoffHandler(kit).Handle(context.Background(), handoffCommand())
	if err == nil || errors.Is(err, ErrHandoffRejected) {
		t.Fatalf("сбой загрузки должен пробрасываться как недоступность, got %v", err)
	}
	if !strings.Contains(err.Error(), "хранилище недоступно") {
		t.Fatalf("ошибка хранилища потеряна: %v", err)
	}
}

func mustEmailChannel(t *testing.T) domain.ActionChannel {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return channel
}
