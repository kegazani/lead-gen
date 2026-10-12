package query

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

func batchLimitsForTest() domain.BatchLimits {
	return domain.BatchLimits{MinSize: 2, ShardSize: 10, MaxSize: 100}
}

// batchCampaign готовит кампанию с count однородными черновиками
// (material-1 + segment-1) с разными адресатами.
func batchCampaign(t *testing.T, count int) *domain.Campaign {
	t.Helper()
	campaign := newCampaign(t, domain.CampaignID("campaign-1"), testTenant)
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
		mustDraft(t, campaign, domain.DraftID(fmt.Sprintf("draft-%d", i)), spec)
	}
	return campaign
}

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

func batchQueueRequest() BatchQueue {
	return BatchQueue{
		CampaignID: domain.CampaignID("campaign-1"),
		TenantID:   testTenant,
		Actor:      testOperator,
		Limits:     batchLimitsForTest(),
	}
}

func TestBatchQueueIsBuiltFromAggregateResult(t *testing.T) {
	campaign := batchCampaign(t, 4)
	risk := riskFor(domain.DraftID("draft-3"))
	handler := NewBatchQueueHandler(newFakeRepository(campaign), risk, allowAccess())

	projection, err := handler.Handle(context.Background(), batchQueueRequest())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(projection.Batches) != 1 {
		t.Fatalf("шардов: got %d, want 1", len(projection.Batches))
	}

	// Проекция обязана совпасть с результатом агрегата: query не фильтрует сам.
	built, err := campaign.BuildApprovalBatch(context.Background(), batchLimitsForTest(), risk)
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	fromAggregate := built.Batches()[0]
	shard := projection.Batches[0]
	if shard.Digest != fromAggregate.Digest() {
		t.Fatalf("digest проекции %q не совпал с digest агрегата %q", shard.Digest, fromAggregate.Digest())
	}
	if shard.Size != fromAggregate.Size() || len(shard.Items) != fromAggregate.Size() {
		t.Fatalf("состав проекции: size=%d, items=%d", shard.Size, len(shard.Items))
	}
	if shard.Key != fromAggregate.Key() {
		t.Fatalf("основание однородности: got %s, want %s", shard.Key, fromAggregate.Key())
	}

	// Исключённые приходят из агрегата с причинами, а не отбрасываются запросом.
	if len(projection.Excluded) != 1 || projection.Excluded[0].DraftID != domain.DraftID("draft-3") {
		t.Fatalf("исключённые: %+v", projection.Excluded)
	}
	if len(projection.Excluded[0].Reasons) != 1 || projection.Excluded[0].Reasons[0] != domain.RiskStopRuleTriggered {
		t.Fatalf("причина исключения: %v", projection.Excluded[0].Reasons)
	}
}

func TestBatchQueueBelowMinimumIsIndividual(t *testing.T) {
	campaign := batchCampaign(t, 1)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor(), allowAccess())

	projection, err := handler.Handle(context.Background(), batchQueueRequest())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(projection.Batches) != 0 {
		t.Fatalf("пакет ниже минимума не формируется: %d", len(projection.Batches))
	}
	if len(projection.Individual) != 1 {
		t.Fatalf("черновик должен уйти на индивидуальное рассмотрение: %+v", projection.Individual)
	}
}

func TestBatchQueueRequiresTenant(t *testing.T) {
	campaign := batchCampaign(t, 2)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor(), allowAccess())

	request := batchQueueRequest()
	request.TenantID = ""
	if _, err := handler.Handle(context.Background(), request); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("ожидалась ErrTenantRequired, получено %v", err)
	}
}

func TestBatchQueueRequiresActor(t *testing.T) {
	campaign := batchCampaign(t, 2)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor(), allowAccess())

	request := batchQueueRequest()
	request.Actor = ""
	if _, err := handler.Handle(context.Background(), request); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("ожидалась ErrActorRequired, получено %v", err)
	}
}

func TestBatchQueueDeniesActorWithoutRight(t *testing.T) {
	campaign := batchCampaign(t, 2)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor(), &stubAccess{allowed: false})

	if _, err := handler.Handle(context.Background(), batchQueueRequest()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидалась ErrAccessDenied, получено %v", err)
	}
}

func TestBatchQueueDeniesWithoutAccessPort(t *testing.T) {
	campaign := batchCampaign(t, 2)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor())

	if _, err := handler.Handle(context.Background(), batchQueueRequest()); !errors.Is(err, ErrAccessDenied) {
		t.Fatalf("ожидалась ErrAccessDenied, получено %v", err)
	}
}

func TestBatchQueueRejectsForeignTenant(t *testing.T) {
	campaign := batchCampaign(t, 2)
	handler := NewBatchQueueHandler(newFakeRepository(campaign), riskFor(), allowAccess())

	request := batchQueueRequest()
	request.TenantID = domain.TenantID("tenant-2")
	if _, err := handler.Handle(context.Background(), request); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("ожидался отказ по тенанту, получено %v", err)
	}
}

// approveBatchDirect утверждает пакет на уровне домена, чтобы проверить
// признак пакетного принятия в журнале без прикладных сценариев команды.
func approveBatchDirect(t *testing.T, campaign *domain.Campaign) {
	t.Helper()
	built, err := campaign.BuildApprovalBatch(context.Background(), batchLimitsForTest(), riskFor())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	batch := built.Batches()[0]
	items := make([]domain.BatchApprovalItem, 0, batch.Size())
	for _, entry := range batch.Entries() {
		items = append(items, domain.BatchApprovalItem{
			DecisionID:   domain.DecisionID("decision-" + string(entry.DraftID)),
			DraftID:      entry.DraftID,
			DraftVersion: entry.Version,
		})
	}
	if _, _, err := campaign.ApproveBatch(context.Background(), domain.BatchApprovalParams{
		Actor:       testOperator,
		DecidedAt:   testNow(),
		Digest:      batch.Digest(),
		Composition: items,
	}, riskFor()); err != nil {
		t.Fatalf("ApproveBatch: %v", err)
	}
	campaign.PullDomainEvents()
}

func TestDraftDecisionJournalMarksBatchDecision(t *testing.T) {
	campaign := batchCampaign(t, 2)
	approveBatchDirect(t, campaign)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	entries, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-0"),
		Actor:      testOperator,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей журнала: got %d, want 1", len(entries))
	}
	if !entries[0].Batch {
		t.Fatal("пакетное решение должно быть отличимо от одиночного")
	}
}

func TestDraftDecisionJournalSingleDecisionHasNoBatchFlag(t *testing.T) {
	campaign := campaignWithDrafts(t)
	handler := NewDraftDecisionHandler(newFakeRepository(campaign), allowAccess())

	entries, err := handler.Handle(context.Background(), DraftDecisionJournal{
		CampaignID: campaign.ID(),
		TenantID:   testTenant,
		DraftID:    domain.DraftID("draft-approved"),
		Actor:      testOperator,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("записей журнала: got %d, want 1", len(entries))
	}
	if entries[0].Batch {
		t.Fatal("одиночное решение не несёт признак пакетного принятия")
	}
}
