package domain

import "testing"

func TestDealRehydrateRoundTrip(t *testing.T) {
	deal := newTestDeal(t)
	advanceTo(t, deal, StageProposal)
	proof, err := NewAttributionProof(ActionRef("action-1"), EvidenceRef("evidence-1"), ExternalDealRef("crm-1"), testAt)
	if err != nil {
		t.Fatalf("NewAttributionProof: %v", err)
	}
	if _, err := deal.ConfirmAttribution(action("evt-attr"), proof); err != nil {
		t.Fatalf("ConfirmAttribution: %v", err)
	}

	restored, err := RehydrateDeal(deal.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateDeal: %v", err)
	}
	if restored.Stage() != StageProposal || restored.Version() != deal.Version() {
		t.Fatalf("восстановлено %+v", restored.Snapshot())
	}
	if !restored.ResultConfirmed() {
		t.Fatal("подтверждение результата потеряно")
	}
	if restored.External().Revision() != 1 {
		t.Fatalf("внешняя версия %d", restored.External().Revision())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
	if len(restored.Transitions()) != len(deal.Transitions()) {
		t.Fatalf("журнал переходов: %d, ожидалось %d", len(restored.Transitions()), len(deal.Transitions()))
	}
}

func TestRehydrateDealRejectsInvalidSnapshot(t *testing.T) {
	deal := newTestDeal(t)
	valid := deal.Snapshot()

	noVersion := valid
	noVersion.Version = 0
	if _, err := RehydrateDeal(noVersion); err == nil {
		t.Fatal("нулевая версия не отвергнута")
	}

	noTransitions := valid
	noTransitions.Transitions = nil
	if _, err := RehydrateDeal(noTransitions); err == nil {
		t.Fatal("пустой журнал переходов не отвергнут")
	}

	mismatch := valid
	mismatch.Stage = StageWon
	if _, err := RehydrateDeal(mismatch); err == nil {
		t.Fatal("этап без перехода не отвергнут")
	}
}
