package command

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// batchLimitsForTest — лимиты пакета для сценариев: минимум 2, шард 10, окно 100.
func batchLimitsForTest() domain.BatchLimits {
	return domain.BatchLimits{MinSize: 2, ShardSize: 10, MaxSize: 100}
}

// mustBatchCampaign готовит кампанию с count однородными черновиками
// (material-1 + segment-1) и сохраняет её в репозиторий теста.
func (k *testKit) mustBatchCampaign(t *testing.T, count int) *domain.Campaign {
	t.Helper()
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       domain.CampaignID("campaign-1"),
		TenantID: domain.TenantID("tenant-1"),
		Name:     "Пакетная кампания",
		Window:   testWindow(t),
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	for i := 0; i < count; i++ {
		spec := domain.DraftSpec{
			Material:  domain.MaterialID("material-1"),
			Segment:   domain.SegmentID("segment-1"),
			Recipient: domain.RecipientID(fmt.Sprintf("recipient-%d", i)),
			Channel:   channel,
		}
		if _, _, err := campaign.DraftAction(domain.DraftID(fmt.Sprintf("draft-%d", i)), spec, testNow()); err != nil {
			t.Fatalf("DraftAction(%d): %v", i, err)
		}
	}
	campaign.PullDomainEvents()
	if err := k.repo.Save(context.Background(), campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}
	return campaign
}

// riskFor возвращает предикат риска, помечающий перечисленные черновики
// обнаружением персональных данных.
func riskFor(ids ...domain.DraftID) domain.CanBeDecidedInPackage {
	marked := make(map[domain.DraftID]struct{}, len(ids))
	for _, id := range ids {
		marked[id] = struct{}{}
	}
	return func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if _, ok := marked[req.DraftID]; ok {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}
}

func buildBatchCommand() BuildBatch {
	return BuildBatch{
		CampaignID: domain.CampaignID("campaign-1"),
		TenantID:   domain.TenantID("tenant-1"),
		Actor:      domain.ActorID("operator-1"),
		Limits:     batchLimitsForTest(),
	}
}

// approveItems переводит показанный состав в позиции команды утверждения.
func approveItems(batch BatchComposition) []BatchApproveItem {
	items := make([]BatchApproveItem, 0, len(batch.Entries))
	for _, entry := range batch.Entries {
		items = append(items, BatchApproveItem{DraftID: entry.DraftID, Version: entry.Version})
	}
	return items
}

// draftItems строит состав из count черновиков первой версии для негативных
// сценариев, где важен не состав, а digest.
func draftItems(count int) []BatchApproveItem {
	items := make([]BatchApproveItem, 0, count)
	for i := 0; i < count; i++ {
		items = append(items, BatchApproveItem{DraftID: domain.DraftID(fmt.Sprintf("draft-%d", i)), Version: 1})
	}
	return items
}

func countStateChanged(events []domain.DomainEvent) int {
	n := 0
	for _, event := range events {
		if _, ok := event.(domain.CampaignStateChanged); ok {
			n++
		}
	}
	return n
}

func TestBuildBatchReturnsCompositionWithExclusions(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 4)
	handler := NewBuildBatchHandler(kit.repo, kit.access, riskFor(domain.DraftID("draft-3")))

	result, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.CampaignID != domain.CampaignID("campaign-1") {
		t.Fatalf("кампания: got %q", result.CampaignID)
	}
	if len(result.Batches) != 1 {
		t.Fatalf("шардов: got %d, want 1", len(result.Batches))
	}

	batch := result.Batches[0]
	if batch.Key.Material != domain.MaterialID("material-1") || batch.Key.Segment != domain.SegmentID("segment-1") {
		t.Fatalf("основание однородности: got %s", batch.Key)
	}
	if batch.Size != 3 {
		t.Fatalf("черновиков в составе: got %d, want 3", batch.Size)
	}
	if batch.Digest == "" {
		t.Fatal("digest состава не вычислен")
	}
	if batch.Sample.Recipient != domain.RecipientID("recipient-0") {
		t.Fatalf("образец содержимого: got %q", batch.Sample.Recipient)
	}
	// Отличия от образца: draft-1 и draft-2 различаются адресатом.
	if len(batch.Differences) != 2 {
		t.Fatalf("отличий внутри пакета: got %d, want 2", len(batch.Differences))
	}
	for _, difference := range batch.Differences {
		if difference.Field != "recipient" {
			t.Fatalf("неожиданное отличие по полю %q", difference.Field)
		}
	}

	if result.ExcludedCount != 1 || len(result.Excluded) != 1 {
		t.Fatalf("исключённых: got %d", result.ExcludedCount)
	}
	excluded := result.Excluded[0]
	if excluded.DraftID != domain.DraftID("draft-3") {
		t.Fatalf("исключён не тот черновик: %q", excluded.DraftID)
	}
	if len(excluded.Reasons) != 1 || excluded.Reasons[0] != domain.RiskStopRuleTriggered {
		t.Fatalf("причина исключения: got %v", excluded.Reasons)
	}
}

func TestBuildBatchBelowMinimumGoesToIndividual(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 1)
	handler := NewBuildBatchHandler(kit.repo, kit.access, riskFor())

	result, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(result.Batches) != 0 {
		t.Fatalf("пакет ниже минимума не формируется, got %d", len(result.Batches))
	}
	if len(result.Individual) != 1 || result.Individual[0].DraftID != domain.DraftID("draft-0") {
		t.Fatalf("черновик должен уйти на индивидуальное рассмотрение: %+v", result.Individual)
	}
}

func TestBuildBatchDigestDeterministicAndSensitiveToComposition(t *testing.T) {
	kit := newTestKit(t)
	campaign := kit.mustBatchCampaign(t, 3)
	handler := NewBuildBatchHandler(kit.repo, kit.access, riskFor())

	first, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	second, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("второй Handle: %v", err)
	}
	if first.Batches[0].Digest != second.Batches[0].Digest {
		t.Fatal("digest одного и того же состава должен совпадать")
	}

	if _, err := campaign.ReviseDraft(domain.DraftID("draft-1"), 1, domain.DraftSpec{
		Material:  domain.MaterialID("material-1"),
		Segment:   domain.SegmentID("segment-1"),
		Recipient: domain.RecipientID("recipient-changed"),
		Channel:   first.Batches[0].Sample.Channel,
	}); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}

	changed, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("Handle после правки: %v", err)
	}
	if changed.Batches[0].Digest == first.Batches[0].Digest {
		t.Fatal("digest должен различаться после изменения версии черновика")
	}
}

func TestBuildBatchDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)
	kit.access.allowed = false
	handler := NewBuildBatchHandler(kit.repo, kit.access, riskFor())

	if _, err := handler.Handle(context.Background(), buildBatchCommand()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
}

func TestApproveBatchRecordsDecisionsAndEvents(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	composition, err := NewBuildBatchHandler(kit.repo, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("BuildBatch: %v", err)
	}
	batch := composition.Batches[0]

	approved, err := NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor()).Handle(context.Background(), ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         batch.Key,
		Digest:      batch.Digest,
		Composition: approveItems(batch),
		Reason:      "пакет согласован",
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if approved.Recorded != 3 || len(approved.Decisions) != 3 {
		t.Fatalf("решений: recorded=%d, decisions=%d", approved.Recorded, len(approved.Decisions))
	}
	for _, decision := range approved.Decisions {
		if !decision.Batch {
			t.Fatalf("решение %s не помечено пакетным", decision.DecisionID)
		}
	}
	for i := 0; i < 3; i++ {
		draft, ok := kit.campaign(t).Draft(domain.DraftID(fmt.Sprintf("draft-%d", i)))
		if !ok || draft.State() != domain.DraftStateApproved {
			t.Fatalf("черновик draft-%d не утверждён: %+v", i, draft)
		}
	}
	if got := countApprovedEvents(kit.outbox.events); got != 3 {
		t.Fatalf("action-approved: got %d, want 3", got)
	}
	if got := countStateChanged(kit.outbox.events); got != 1 {
		t.Fatalf("campaign-state-changed: got %d, want 1", got)
	}
}

func TestApproveBatchRejectsDecisionWithoutDigest(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	_, err := NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor()).Handle(context.Background(), ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         domain.HomogeneityKey{Material: "material-1", Segment: "segment-1"},
		Composition: draftItems(3),
	})
	if !errors.Is(err, domain.ErrBatchDigestMismatch) {
		t.Fatalf("решение без digest должно быть отвергнуто, получено %v", err)
	}
	if got := len(kit.campaign(t).Decisions()); got != 0 {
		t.Fatalf("состояние не должно меняться, решений: %d", got)
	}
	if len(kit.outbox.events) != 0 {
		t.Fatalf("события не должны записываться: %d", len(kit.outbox.events))
	}
}

func TestApproveBatchRejectsUnknownDigest(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	_, err := NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor()).Handle(context.Background(), ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         domain.HomogeneityKey{Material: "material-1", Segment: "segment-1"},
		Digest:      "неверный-digest",
		Composition: draftItems(3),
	})
	if !errors.Is(err, domain.ErrBatchDigestMismatch) {
		t.Fatalf("ожидалась ErrBatchDigestMismatch, получено %v", err)
	}
	if got := len(kit.campaign(t).Decisions()); got != 0 {
		t.Fatalf("состояние не должно меняться, решений: %d", got)
	}
}

func TestApproveBatchRejectsChangedComposition(t *testing.T) {
	kit := newTestKit(t)
	campaign := kit.mustBatchCampaign(t, 3)

	composition, err := NewBuildBatchHandler(kit.repo, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("BuildBatch: %v", err)
	}
	batch := composition.Batches[0]

	// Черновик изменён после просмотра: digest старого состава больше не совпадает.
	if _, err := campaign.ReviseDraft(domain.DraftID("draft-1"), 1, domain.DraftSpec{
		Material:  domain.MaterialID("material-1"),
		Segment:   domain.SegmentID("segment-1"),
		Recipient: domain.RecipientID("recipient-2"),
		Channel:   batch.Sample.Channel,
	}); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}

	_, err = NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor()).Handle(context.Background(), ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         batch.Key,
		Digest:      batch.Digest,
		Composition: approveItems(batch),
	})
	if !errors.Is(err, domain.ErrDraftVersionMismatch) && !errors.Is(err, domain.ErrBatchDigestMismatch) {
		t.Fatalf("изменение состава должно отвергать решение, получено %v", err)
	}
	if got := len(kit.campaign(t).Decisions()); got != 0 {
		t.Fatalf("состояние не должно меняться, решений: %d", got)
	}
}

func TestApproveBatchRepeatIsIdempotent(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	composition, err := NewBuildBatchHandler(kit.repo, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("BuildBatch: %v", err)
	}
	batch := composition.Batches[0]
	handler := NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor())
	command := ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         batch.Key,
		Digest:      batch.Digest,
		Composition: approveItems(batch),
	}

	if _, err := handler.Handle(context.Background(), command); err != nil {
		t.Fatalf("первое Handle: %v", err)
	}
	repeat, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("повторное Handle: %v", err)
	}
	if repeat.Recorded != 0 || repeat.EventCount != 0 {
		t.Fatalf("повтор не должен создавать записи и события: recorded=%d, events=%d", repeat.Recorded, repeat.EventCount)
	}
	if got := len(kit.campaign(t).Decisions()); got != 3 {
		t.Fatalf("решений после повтора: got %d, want 3", got)
	}
	if got := countApprovedEvents(kit.outbox.events); got != 3 {
		t.Fatalf("action-approved после повтора: got %d, want 3", got)
	}
}

func TestApproveBatchDeniesWithoutAccess(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	composition, err := NewBuildBatchHandler(kit.repo, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("BuildBatch: %v", err)
	}
	batch := composition.Batches[0]
	kit.access.allowed = false

	_, err = NewApproveBatchHandler(kit.access, kit.clock, kit.uow, riskFor()).Handle(context.Background(), ApproveBatch{
		CampaignID:  domain.CampaignID("campaign-1"),
		TenantID:    domain.TenantID("tenant-1"),
		Actor:       domain.ActorID("operator-1"),
		Key:         batch.Key,
		Digest:      batch.Digest,
		Composition: approveItems(batch),
	})
	if !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидался отказ доступа, получено %v", err)
	}
	if got := len(kit.campaign(t).Decisions()); got != 0 {
		t.Fatalf("без права доступа решение не принимается, решений: %d", got)
	}
}
