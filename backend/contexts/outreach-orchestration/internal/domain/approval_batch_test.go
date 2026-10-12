package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func batchLimits(minSize, shardSize, maxSize int) BatchLimits {
	return BatchLimits{MinSize: minSize, ShardSize: shardSize, MaxSize: maxSize}
}

func batchSpec(t *testing.T, material, segment, recipient string) DraftSpec {
	t.Helper()
	return DraftSpec{
		Material:  MaterialID(material),
		Segment:   SegmentID(segment),
		Recipient: RecipientID(recipient),
		Channel:   mustChannel(t, ActionChannelEmailName),
	}
}

func addBatchDraft(t *testing.T, campaign *Campaign, id DraftID, spec DraftSpec) {
	t.Helper()
	if _, _, err := campaign.DraftAction(id, spec, testMoment()); err != nil {
		t.Fatalf("DraftAction(%s): %v", id, err)
	}
}

func riskPredicate(signs map[DraftID][]RiskReason) CanBeDecidedInPackage {
	return func(_ context.Context, req RiskFactsRequest) ([]RiskReason, error) {
		return signs[req.DraftID], nil
	}
}

// noRiskChecks — тестовый предикат без признаков риска: не заглушка продукта, а
// двойник теста, возвращающий пустой список без ошибки.
func noRiskChecks() CanBeDecidedInPackage { return riskPredicate(nil) }

// unavailableFacts — тестовый предикат недоступных фактов риска: ошибка, а не
// пустой список («рисков нет»).
func unavailableFacts() CanBeDecidedInPackage {
	return func(context.Context, RiskFactsRequest) ([]RiskReason, error) {
		return nil, fmt.Errorf("%w: владелец недоступен", ErrRiskFactsUnavailable)
	}
}

func itemFor(t *testing.T, campaign *Campaign, id DraftID) BatchApprovalItem {
	t.Helper()
	draft, ok := campaign.Draft(id)
	if !ok {
		t.Fatalf("черновик %s не найден", id)
	}
	return BatchApprovalItem{
		DecisionID:   DecisionID("decision-" + string(id)),
		DraftID:      id,
		DraftVersion: draft.Version(),
	}
}

func paramsForBatch(t *testing.T, batch ApprovalBatch) BatchApprovalParams {
	t.Helper()
	items := make([]BatchApprovalItem, 0, batch.Size())
	for _, entry := range batch.Entries() {
		items = append(items, BatchApprovalItem{
			DecisionID:   DecisionID("decision-" + string(entry.DraftID)),
			DraftID:      entry.DraftID,
			DraftVersion: entry.Version,
		})
	}
	return BatchApprovalParams{
		Actor:       ActorID("operator-1"),
		DecidedAt:   testDecisionTime(),
		Digest:      batch.Digest(),
		Composition: items,
	}
}

func batchByKey(t *testing.T, result BatchBuildResult, key HomogeneityKey) ApprovalBatch {
	t.Helper()
	for _, batch := range result.Batches() {
		if batch.Key() == key {
			return batch
		}
	}
	t.Fatalf("пакет для %s не сформирован", key)
	return ApprovalBatch{}
}

func TestBuildApprovalBatchGroupsHomogeneousDrafts(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))
	addBatchDraft(t, campaign, "d2", batchSpec(t, "m1", "s1", "r2"))
	addBatchDraft(t, campaign, "d3", batchSpec(t, "m1", "s1", "r3"))
	addBatchDraft(t, campaign, "d4", batchSpec(t, "m2", "s1", "r4"))
	addBatchDraft(t, campaign, "d5", batchSpec(t, "m1", "s2", "r5"))
	addBatchDraft(t, campaign, "d6", batchSpec(t, "m2", "s1", "r6"))
	addBatchDraft(t, campaign, "d7", batchSpec(t, "m1", "s2", "r7"))

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	if len(result.Batches()) != 3 {
		t.Fatalf("пакетов %d, ожидалось 3", len(result.Batches()))
	}
	byMaterialAndSegment := batchByKey(t, result, HomogeneityKey{Material: "m1", Segment: "s1"})
	if byMaterialAndSegment.Size() != 3 {
		t.Fatalf("размер пакета m1+s1 = %d, ожидалось 3", byMaterialAndSegment.Size())
	}
	if batchByKey(t, result, HomogeneityKey{Material: "m2", Segment: "s1"}).Size() != 2 {
		t.Fatal("пакет m2+s1 потерял черновик")
	}
	// m2+s1 и m1+s2 не должны были объединяться с m1+s1: разные ключи.
	for _, batch := range result.Batches() {
		for _, entry := range batch.Entries() {
			spec := entry.Spec
			if spec.Material != batch.Key().Material || spec.Segment != batch.Key().Segment {
				t.Fatalf("в пакет %s попал разнородный черновик %+v", batch.Key(), spec)
			}
		}
	}
	if len(result.Excluded()) != 0 {
		t.Fatalf("неожиданные исключения: %+v", result.Excluded())
	}
}

func TestBuildApprovalBatchSplitsIntoShardsAboveLimit(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2", "d3", "d4", "d5"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 2, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	if len(result.Batches()) != 2 {
		t.Fatalf("пакетов %d, ожидалось 2", len(result.Batches()))
	}
	for _, batch := range result.Batches() {
		if batch.Size() != 2 {
			t.Fatalf("размер шарда %d, ожидалось 2", batch.Size())
		}
	}
	if len(result.Individual()) != 1 {
		t.Fatalf("на индивидуальное рассмотрение ушло %d черновиков, ожидался 1 (хвост меньше минимума)", len(result.Individual()))
	}
}

func TestBuildApprovalBatchBelowMinimumGoesToIndividual(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	if len(result.Batches()) != 0 {
		t.Fatalf("пакет не должен формироваться из %d черновиков", result.Batches()[0].Size())
	}
	if len(result.Individual()) != 1 || result.Individual()[0].DraftID != "d1" {
		t.Fatalf("черновик не направлен на индивидуальное рассмотрение: %+v", result.Individual())
	}
}

func TestBuildApprovalBatchExcludesEachRiskReason(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "clean-1", batchSpec(t, "m1", "s1", "r1"))
	addBatchDraft(t, campaign, "clean-2", batchSpec(t, "m1", "s1", "r2"))
	// Признаки риска — только те, у которых есть владелец факта (design.md,
	// Решение 1): стоп-лист и верификация адреса.
	reasons := map[DraftID][]RiskReason{
		"risk-stop":    {RiskStopRuleTriggered},
		"risk-address": {RiskAddressUnverified},
	}
	for id := range reasons {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "recipient-"+string(id)))
	}

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), riskPredicate(reasons))
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	if len(result.Batches()) != 1 || result.Batches()[0].Size() != 2 {
		t.Fatalf("чистые черновики не собраны в пакет: %+v", result.Batches())
	}
	if len(result.Excluded()) != len(reasons) {
		t.Fatalf("исключено %d, ожидалось %d", len(result.Excluded()), len(reasons))
	}
	seen := make(map[DraftID][]RiskReason)
	for _, excluded := range result.Excluded() {
		seen[excluded.DraftID] = excluded.Reasons
	}
	for id, want := range reasons {
		got := seen[id]
		if len(got) != 1 || got[0] != want[0] {
			t.Fatalf("черновик %s исключён с причинами %v, ожидалась %v", id, got, want)
		}
	}
}

func TestBuildApprovalBatchValidatesLimits(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))

	for _, limits := range []BatchLimits{
		batchLimits(0, 10, 100),
		batchLimits(5, 2, 100),
		batchLimits(5, 20, 10),
	} {
		if _, err := campaign.BuildApprovalBatch(context.Background(), limits, noRiskChecks()); !errors.Is(err, ErrInvalidBatchLimits) {
			t.Fatalf("лимиты %+v: ожидалась ErrInvalidBatchLimits, получено %v", limits, err)
		}
	}
}

func TestApproveBatchCannotBypassRiskExclusion(t *testing.T) {
	campaign := newCampaign(t)
	cleanSpec := batchSpec(t, "m1", "s1", "r1")
	riskySpec := batchSpec(t, "m1", "s1", "r2")
	addBatchDraft(t, campaign, "clean", cleanSpec)
	addBatchDraft(t, campaign, "risky", riskySpec)

	key := HomogeneityKey{Material: "m1", Segment: "s1"}
	entries := []BatchEntry{
		{DraftID: "clean", Version: 1, Spec: cleanSpec},
		{DraftID: "risky", Version: 1, Spec: riskySpec},
	}
	params := BatchApprovalParams{
		Actor:     ActorID("operator-1"),
		DecidedAt: testDecisionTime(),
		Digest:    batchCompositionDigest(key, entries),
		Composition: []BatchApprovalItem{
			itemFor(t, campaign, "clean"),
			itemFor(t, campaign, "risky"),
		},
	}
	signs := map[DraftID][]RiskReason{"risky": {RiskStopRuleTriggered}}

	_, _, err := campaign.ApproveBatch(context.Background(), params, riskPredicate(signs))
	if !errors.Is(err, ErrBatchDraftNotEligible) {
		t.Fatalf("ожидалась ErrBatchDraftNotEligible, получено %v", err)
	}
	if len(campaign.Decisions()) != 0 {
		t.Fatal("отказ не должен создавать решения")
	}
	for _, id := range []DraftID{"clean", "risky"} {
		draft, _ := campaign.Draft(id)
		if draft.State() != DraftStatePendingApproval {
			t.Fatalf("черновик %s изменил состояние на %q при отказе", id, draft.State())
		}
	}
}

func TestApproveBatchCreatesOneDecisionPerDraft(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2", "d3"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	batch := result.Batches()[0]

	decisions, _, err := campaign.ApproveBatch(context.Background(), paramsForBatch(t, batch), noRiskChecks())
	if err != nil {
		t.Fatalf("ApproveBatch: %v", err)
	}
	if len(decisions) != 3 || len(campaign.Decisions()) != 3 {
		t.Fatalf("решений %d, ожидалось ровно 3 отдельных", len(campaign.Decisions()))
	}
	for _, id := range []DraftID{"d1", "d2", "d3"} {
		own := campaign.DecisionsForDraft(id)
		if len(own) != 1 || own[0].DraftID() != id || own[0].DraftVersion() != 1 {
			t.Fatalf("журнал черновика %s: %+v", id, own)
		}
		if !own[0].IsBatchDecision() {
			t.Fatalf("решение по %s не помечено пакетным", id)
		}
		if own[0].BatchDigest() != batch.Digest() {
			t.Fatalf("провенанс решения по %s не ссылается на digest состава", id)
		}
		draft, _ := campaign.Draft(id)
		if draft.State() != DraftStateApproved {
			t.Fatalf("черновик %s в состоянии %q", id, draft.State())
		}
	}
}

func TestSingleDecisionCarriesNoBatchProvenance(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))

	decision, _, err := campaign.ApproveDraft(decisionParamsFor("d1", 1, ""))
	if err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if decision.IsBatchDecision() || decision.BatchDigest() != "" {
		t.Fatalf("одиночное решение несёт признак пакета: %+v", decision)
	}
}

func TestBatchDigestDeterministicAndSensitiveToComposition(t *testing.T) {
	first := newCampaign(t)
	second := newCampaign(t)
	for _, campaign := range []*Campaign{first, second} {
		for _, id := range []DraftID{"d1", "d2", "d3"} {
			addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
		}
	}
	firstBatch := batchByKey(t, mustBuild(t, first), HomogeneityKey{Material: "m1", Segment: "s1"})
	secondBatch := batchByKey(t, mustBuild(t, second), HomogeneityKey{Material: "m1", Segment: "s1"})
	if firstBatch.Digest() != secondBatch.Digest() {
		t.Fatal("digest одного и того же состава должен совпадать")
	}

	if _, err := second.ReviseDraft("d3", 1, batchSpec(t, "m1", "s1", "r-changed")); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}
	changedBatch := batchByKey(t, mustBuild(t, second), HomogeneityKey{Material: "m1", Segment: "s1"})
	if changedBatch.Digest() == secondBatch.Digest() {
		t.Fatal("digest должен различаться после изменения версии черновика")
	}
}

func mustBuild(t *testing.T, campaign *Campaign) BatchBuildResult {
	t.Helper()
	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	return result
}

func TestApproveBatchIsIdempotent(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	params := paramsForBatch(t, batch)
	campaign.PullDomainEvents()

	if _, events, err := campaign.ApproveBatch(context.Background(), params, noRiskChecks()); err != nil || len(events) != 3 {
		t.Fatalf("первое утверждение: events=%d, err=%v", len(events), err)
	}
	decisions, events, err := campaign.ApproveBatch(context.Background(), params, noRiskChecks())
	if err != nil {
		t.Fatalf("повторное утверждение: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("повтор эмитировал %d событий, ожидалось 0", len(events))
	}
	if len(decisions) != 2 || len(campaign.Decisions()) != 2 {
		t.Fatalf("повтор создал лишние решения: %d", len(campaign.Decisions()))
	}
}

func TestApproveBatchRejectsDigestMismatch(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	params := paramsForBatch(t, batch)
	params.Digest = "неверный-digest"

	if _, _, err := campaign.ApproveBatch(context.Background(), params, noRiskChecks()); !errors.Is(err, ErrBatchDigestMismatch) {
		t.Fatalf("ожидалась ErrBatchDigestMismatch, получено %v", err)
	}
	if len(campaign.Decisions()) != 0 {
		t.Fatal("отказ по digest не должен менять состояние")
	}
}

func TestApproveBatchRejectsChangedComposition(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	params := paramsForBatch(t, batch)

	if _, err := campaign.ReviseDraft("d2", 1, batchSpec(t, "m1", "s1", "r-d2-v2")); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}
	if _, _, err := campaign.ApproveBatch(context.Background(), params, noRiskChecks()); !errors.Is(err, ErrDraftVersionMismatch) {
		t.Fatalf("ожидалась ErrDraftVersionMismatch, получено %v", err)
	}
	if len(campaign.Decisions()) != 0 {
		t.Fatal("изменение состава не должно создавать решения")
	}
}

func TestRejectWithinBatchUsesOrdinaryRejectDraft(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	params := paramsForBatch(t, batch)

	rejected, _, err := campaign.RejectDraft(decisionParamsFor("d1", 1, "нужна другая формулировка"))
	if err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	if rejected.IsApproval() || rejected.IsBatchDecision() {
		t.Fatalf("отклонение внутри пакета не должно быть утверждением: %+v", rejected)
	}
	draft, _ := campaign.Draft("d1")
	if draft.State() != DraftStateRejected || campaign.DecisionsForDraft("d1")[0].Reason() == "" {
		t.Fatalf("черновик не возвращён на доработку с причиной: %+v", draft)
	}

	// Пакет, включающий отклонённый черновик, больше не принимается.
	if _, _, err := campaign.ApproveBatch(context.Background(), params, noRiskChecks()); !errors.Is(err, ErrBatchCompositionChanged) {
		t.Fatalf("ожидалась ErrBatchCompositionChanged, получено %v", err)
	}
}

func TestApproveBatchEmitsNActionApprovedAndStateChanged(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2", "d3"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	// События постановки черновиков отработаны: проверяем именно событийный
	// след пакетного утверждения.
	campaign.PullDomainEvents()

	if _, _, err := campaign.ApproveBatch(context.Background(), paramsForBatch(t, batch), noRiskChecks()); err != nil {
		t.Fatalf("ApproveBatch: %v", err)
	}
	events := campaign.DomainEvents()
	if got := countEvents[ActionApproved](events); got != 3 {
		t.Fatalf("action-approved %d, ожидалось ровно 3", got)
	}
	if got := countEvents[CampaignStateChanged](events); got != 1 {
		t.Fatalf("campaign-state-changed %d, ожидалось 1", got)
	}
	if len(events) != 4 {
		t.Fatalf("всего событий %d, ожидалось 4 (N action-approved + campaign-state-changed)", len(events))
	}
	stateChanged, ok := eventOfType[CampaignStateChanged](events)
	if !ok || stateChanged.From != stateChanged.To {
		t.Fatalf("пакетное изменение не зафиксировано событием кампании: %+v", stateChanged)
	}
	approved, _ := eventOfType[ActionApproved](events)
	if approved.DraftVersion != 1 {
		t.Fatalf("событие без точной версии черновика: %+v", approved)
	}
	for _, event := range events {
		if event.Name() != EventActionApproved && event.Name() != EventCampaignStateChanged {
			t.Fatalf("появилось новое событие %q", event.Name())
		}
	}
}

func TestApproveBatchPreservesI1AndI2(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	if _, _, err := campaign.ApproveBatch(context.Background(), paramsForBatch(t, batch), noRiskChecks()); err != nil {
		t.Fatalf("ApproveBatch: %v", err)
	}

	// I1: действие исполняется по точной версии с положительным решением.
	if err := campaign.ExecuteDraft("d1", 1); err != nil {
		t.Fatalf("ExecuteDraft после пакетного решения: %v", err)
	}

	// I2: изменённый черновик требует нового решения по новой версии.
	if _, err := campaign.ReviseDraft("d2", 1, batchSpec(t, "m1", "s1", "r-d2-v2")); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}
	if err := campaign.ExecuteDraft("d2", 2); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("ожидалась ErrApprovalRequired для новой версии, получено %v", err)
	}
}

func TestBuildApprovalBatchLeavesExcessPendingBeyondWindow(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2", "d3", "d4"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 3, 3), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	if len(result.Batches()) != 1 || result.Batches()[0].Size() != 3 {
		t.Fatalf("в пределах окна ожидался один пакет из 3: %+v", result.Batches())
	}
	pending := campaign.PendingApproval()
	if len(pending) != 4 {
		t.Fatalf("окно не должно менять состояние очереди: ожидалось 4 ожидающих, получено %d", len(pending))
	}
}

func TestBuildApprovalBatchFromDraftsMatchesAggregateDigest(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2", "d3"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	limits := batchLimits(2, 10, 100)

	// Путь агрегата: черновики берутся из самого агрегата.
	viaAggregate, err := campaign.BuildApprovalBatch(context.Background(), limits, noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}
	// Путь окна: тот же набор черновиков приходит слайсом, как из репозитория.
	viaDrafts, err := BuildApprovalBatchFromDrafts(context.Background(), campaign.Drafts(), campaign.ID(), campaign.TenantID(), limits, noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatchFromDrafts: %v", err)
	}

	aggregateBatches := viaAggregate.Batches()
	draftBatches := viaDrafts.Batches()
	if len(aggregateBatches) != len(draftBatches) {
		t.Fatalf("пакетов из окна %d, из агрегата %d", len(draftBatches), len(aggregateBatches))
	}
	for i := range aggregateBatches {
		fromAggregate, fromWindow := aggregateBatches[i], draftBatches[i]
		if fromAggregate.Key() != fromWindow.Key() || fromAggregate.Size() != fromWindow.Size() {
			t.Fatalf("состав из окна отличается от состава из агрегата: %+v / %+v", fromWindow, fromAggregate)
		}
		if fromAggregate.Digest() != fromWindow.Digest() {
			t.Fatalf("digest одного состава должен совпадать: агрегат %s, окно %s", fromAggregate.Digest(), fromWindow.Digest())
		}
	}
}

func TestBuildApprovalBatchFromDraftsFocusesOnPendingApproval(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))
	addBatchDraft(t, campaign, "d2", batchSpec(t, "m1", "s1", "r2"))
	if _, _, err := campaign.RejectDraft(decisionParamsFor("d2", 1, "нужна доработка")); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}

	result, err := BuildApprovalBatchFromDrafts(context.Background(), campaign.Drafts(), campaign.ID(), campaign.TenantID(), batchLimits(2, 10, 100), noRiskChecks())
	if err != nil {
		t.Fatalf("BuildApprovalBatchFromDrafts: %v", err)
	}
	if len(result.Batches()) != 0 {
		t.Fatalf("решённые черновики не входят в состав: %+v", result.Batches())
	}
	if len(result.Individual()) != 1 || result.Individual()[0].DraftID != "d1" {
		t.Fatalf("ожидался один ожидающий черновик: %+v", result.Individual())
	}
}

func TestBuildApprovalBatchFromDraftsRejectsForeignCampaign(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))

	_, err := BuildApprovalBatchFromDrafts(context.Background(), campaign.Drafts(), CampaignID("campaign-2"), campaign.TenantID(), batchLimits(2, 10, 100), noRiskChecks())
	if !errors.Is(err, ErrDraftNotFound) {
		t.Fatalf("черновик чужой кампании должен быть отвергнут, получено %v", err)
	}
}

// TestBuildApprovalBatchNilPredicateIsUnavailable проверяет, что незаданный
// предикат — недоступность фактов, а не «рисков нет».
func TestBuildApprovalBatchNilPredicateIsUnavailable(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))

	if _, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), nil); !errors.Is(err, ErrRiskFactsUnavailable) {
		t.Fatalf("nil-предикат: ошибка = %v, ожидалась ErrRiskFactsUnavailable", err)
	}
}

// TestBuildApprovalBatchUnavailableFactsIsErrorNotExclusion проверяет, что
// недоступность фактов не даёт исключения и не трактуется как «рисков нет»:
// состав не выдаётся, вызывающий получает ошибку.
func TestBuildApprovalBatchUnavailableFactsIsErrorNotExclusion(t *testing.T) {
	campaign := newCampaign(t)
	addBatchDraft(t, campaign, "d1", batchSpec(t, "m1", "s1", "r1"))
	addBatchDraft(t, campaign, "d2", batchSpec(t, "m1", "s1", "r2"))

	result, err := campaign.BuildApprovalBatch(context.Background(), batchLimits(2, 10, 100), unavailableFacts())
	if !errors.Is(err, ErrRiskFactsUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrRiskFactsUnavailable", err)
	}
	if len(result.Excluded()) != 0 || len(result.Batches()) != 0 {
		t.Fatalf("недоступность фактов не должна давать ни исключения, ни пакета: %+v", result)
	}
}

// TestApproveBatchUnavailableFactsIsError проверяет, что недоступность фактов
// останавливает пакетное решение с ошибкой, а не с «рисков нет».
func TestApproveBatchUnavailableFactsIsError(t *testing.T) {
	campaign := newCampaign(t)
	for _, id := range []DraftID{"d1", "d2"} {
		addBatchDraft(t, campaign, id, batchSpec(t, "m1", "s1", "r-"+string(id)))
	}
	batch := batchByKey(t, mustBuild(t, campaign), HomogeneityKey{Material: "m1", Segment: "s1"})
	campaign.PullDomainEvents()

	if _, _, err := campaign.ApproveBatch(context.Background(), paramsForBatch(t, batch), unavailableFacts()); !errors.Is(err, ErrRiskFactsUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrRiskFactsUnavailable", err)
	}
	if len(campaign.Decisions()) != 0 {
		t.Fatalf("при недоступности фактов решения записаны: %d", len(campaign.Decisions()))
	}
}
