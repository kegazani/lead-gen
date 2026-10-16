package domain

import (
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	rp "lead-gen/contracts/agent-orchestration/researchplan"
	"testing"
	"time"
)

func TestResearchCanonicalCreditUsesExplicitGroupsAndSurvivesSplit(t *testing.T) {
	mission := &ResearchMission{State: "running", Request: rm.StartRequest{Plan: rp.Plan{Brief: rp.SearchBrief{Goal: rp.Goal{Quantity: 3, Unit: rp.UnitCompanyWithContact}}}}, Members: []rm.Member{
		{EntityID: "main", CanonicalEntityID: "main", CriteriaAccepted: true},
		{EntityID: "contacts", CanonicalEntityID: "main", HasContact: true, HasEmail: true},
		{EntityID: "other", CanonicalEntityID: "other", CriteriaAccepted: true},
	}}
	mission.Recredit()
	snapshot := mission.Snapshot()
	if snapshot.Counters.Saved != 2 || snapshot.Counters.Accepted != 1 || len(snapshot.Members) != 2 || len(snapshot.Members[0].OriginalEntityIDs) != 2 {
		t.Fatalf("явная группа не зачтена один раз: %+v", snapshot)
	}
	// Отзыв тождества отделяет контакт от доказательства ICP, а не оставляет
	// незаконный контакт на компании, от которой его открепили.
	mission.Members[1].CanonicalEntityID = "contacts"
	mission.Recredit()
	snapshot = mission.Snapshot()
	if snapshot.Counters.Saved != 3 || snapshot.Counters.Accepted != 0 {
		t.Fatalf("разделение не пересчитало unit: %+v", snapshot)
	}
}
func TestResearchWithdrawnCriteriaCannotCreditAnotherAvailableAlias(t *testing.T) {
	mission := &ResearchMission{State: "running", Request: rm.StartRequest{Plan: rp.Plan{Brief: rp.SearchBrief{Goal: rp.Goal{Quantity: 3, Unit: rp.UnitCompany}}}}, Members: []rm.Member{
		{EntityID: "old-proof", CanonicalEntityID: "group", CriteriaAccepted: true, SourceUnavailable: true},
		{EntityID: "new-name", CanonicalEntityID: "group", CriteriaAccepted: false},
	}}
	mission.Recredit()
	if mission.Counters().Accepted != 0 {
		t.Fatal("доступное имя оживило отозванное основание критериев")
	}
}
func TestResearchTerminalCompositionKeepsOriginalIdentitySnapshot(t *testing.T) {
	mission := &ResearchMission{State: "running", Request: rm.StartRequest{Plan: rp.Plan{Brief: rp.SearchBrief{Goal: rp.Goal{Quantity: 2, Unit: rp.UnitCompany}}}}, Members: []rm.Member{{EntityID: "a", CriteriaAccepted: true}, {EntityID: "b", CriteriaAccepted: true}}}
	mission.Recredit()
	mission.Finish("completed", "goal-reached", time.Now().UTC())
	mission.Members[1].CanonicalEntityID = "a"
	mission.Recredit()
	if mission.Snapshot().Counters.Accepted != 2 || len(mission.Snapshot().Members) != 2 {
		t.Fatal("позднее тождество переписало завершённый состав")
	}
}

func TestResearchSameNameDoesNotCountTwiceWithoutIdentityGround(t *testing.T) {
	mission := &ResearchMission{State: "running", Request: rm.StartRequest{Plan: rp.Plan{Brief: rp.SearchBrief{Goal: rp.Goal{Quantity: 2, Unit: rp.UnitCompany}}}}, Members: []rm.Member{
		{EntityID: "home", Name: "ООО «Альфа»", CriteriaAccepted: true},
		{EntityID: "contacts", Name: "ооо Альфа", CriteriaAccepted: true, HasEmail: true},
	}}
	mission.Recredit()
	snapshot := mission.Snapshot()
	if snapshot.Counters.Accepted != 1 || snapshot.Counters.Saved != 2 || snapshot.Members[1].Reason != "identity-review-required" || len(snapshot.Warnings) != 1 {
		t.Fatalf("страницы удвоили компанию: %+v", snapshot)
	}
	if snapshot.Members[0].HasEmail {
		t.Fatal("контакт перенесён без решения о тождестве")
	}
	// Действующее человеческое решение о различии снимает удержание, не объединяет.
	mission.Members[1].DistinctCanonicalEntityIDs = []string{"home"}
	mission.Recredit()
	if mission.Counters().Accepted != 2 {
		t.Fatal("доказанное различие не сняло удержание")
	}
	// После отзыва решения удержание возвращается, а записи остаются двумя.
	mission.Members[1].DistinctCanonicalEntityIDs = nil
	mission.Recredit()
	if mission.Counters().Accepted != 1 || mission.Counters().Saved != 2 {
		t.Fatal("отзыв решения не пересмотрел credit")
	}
}
