package domain

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

// snapshotOf собирает кампанию с разнообразной историей: исполненный черновик,
// отклонённый и доработанный черновик, черновик в очереди утверждения.
func snapshotOf(t *testing.T) *Campaign {
	t.Helper()
	campaign := newCampaign(t)

	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	if err := campaign.ExecuteDraft(DraftID("draft-1"), 1); err != nil {
		t.Fatalf("ExecuteDraft: %v", err)
	}

	if _, _, err := campaign.DraftAction(DraftID("draft-2"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.RejectDraft(decisionParamsFor(DraftID("draft-2"), 1, "нужна другая формулировка")); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	revised := testSpec(t)
	revised.Channel = mustChannel(t, ActionChannelCallName)
	if _, err := campaign.ReviseDraft(DraftID("draft-2"), 1, revised); err != nil {
		t.Fatalf("ReviseDraft: %v", err)
	}

	if _, _, err := campaign.DraftAction(DraftID("draft-3"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	campaign.PullDomainEvents()
	return campaign
}

// TestRehydrateCampaignPreservesEditsAndPostponement проверяет, что отдельная
// запись правок (с автором) и срок отложения переживают сохранение и загрузку.
func TestRehydrateCampaignPreservesEditsAndPostponement(t *testing.T) {
	campaign := newCampaign(t)
	generated := testSpec(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), generated, testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	edited := testSpec(t)
	edited.Channel = mustChannel(t, ActionChannelCallName)
	if _, err := campaign.ReviseDraftRecorded(DraftID("draft-1"), 1, edited, ActorID("operator-1"), testMoment()); err != nil {
		t.Fatalf("ReviseDraftRecorded: %v", err)
	}
	until := testMoment().Add(24 * time.Hour)
	if err := campaign.PostponeDraft(DraftID("draft-1"), 2, until); err != nil {
		t.Fatalf("PostponeDraft: %v", err)
	}
	campaign.PullDomainEvents()

	restored, err := RehydrateCampaign(campaign.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateCampaign: %v", err)
	}
	draft, ok := restored.Draft(DraftID("draft-1"))
	if !ok {
		t.Fatal("черновик не восстановлен")
	}
	edits := draft.Edits()
	if len(edits) != 1 {
		t.Fatalf("записей правок после round-trip: got %d, want 1", len(edits))
	}
	if edits[0].Actor != ActorID("operator-1") || edits[0].From != generated || edits[0].To != edited {
		t.Fatalf("запись правки восстановлена неверно: %+v", edits[0])
	}
	if !draft.PostponedUntil().Equal(until.UTC()) {
		t.Fatalf("срок отложения после round-trip: got %v, want %v", draft.PostponedUntil(), until.UTC())
	}
}

// Round-trip: восстановленный агрегат равен сохранённому и ведёт себя так же.
func TestRehydrateCampaignRoundTrip(t *testing.T) {
	campaign := snapshotOf(t)
	before := campaign.Snapshot()

	restored, err := RehydrateCampaign(before)
	if err != nil {
		t.Fatalf("RehydrateCampaign: %v", err)
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не должно эмитить события")
	}
	if !reflect.DeepEqual(before, restored.Snapshot()) {
		t.Fatalf("снимок после round-trip не совпал:\nwas: %+v\ngot: %+v", before, restored.Snapshot())
	}

	// Повтор полностью идентичного решения идемпотентен и не оживляет события.
	existing, events, err := restored.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, ""))
	if err != nil {
		t.Fatalf("повтор решения после восстановления: %v", err)
	}
	if existing.DraftID() != DraftID("draft-1") || len(events) != 0 {
		t.Fatalf("ожидалось прежнее решение без событий, получено %+v %v", existing, events)
	}

	// Новое поведение работает на восстановленном агрегате: утвердить и
	// исполнить доработанную версию черновика.
	if _, _, err := restored.ApproveDraft(decisionParamsFor(DraftID("draft-2"), 2, "")); err != nil {
		t.Fatalf("ApproveDraft v2 после восстановления: %v", err)
	}
	if err := restored.ExecuteDraft(DraftID("draft-2"), 2); err != nil {
		t.Fatalf("ExecuteDraft v2 после восстановления: %v", err)
	}
	draft, _ := restored.Draft(DraftID("draft-2"))
	if draft.State() != DraftStateExecuted {
		t.Fatalf("состояние восстановленного черновика %q", draft.State())
	}
}

func TestRehydrateActionDraftRoundTrip(t *testing.T) {
	draft := draftInState(t, DraftStateApproved)
	restored, err := RehydrateActionDraft(draft.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateActionDraft: %v", err)
	}
	if !reflect.DeepEqual(draft.Snapshot(), restored.Snapshot()) {
		t.Fatalf("снимок черновика не совпал: %+v и %+v", draft.Snapshot(), restored.Snapshot())
	}
	if err := restored.revise(testSpec(t)); err != nil {
		t.Fatalf("revise после восстановления: %v", err)
	}
	if restored.Version() != draft.Version()+1 || restored.State() != DraftStatePendingApproval {
		t.Fatalf("поведение восстановленного черновика отличается: v%d %q", restored.Version(), restored.State())
	}
}

func TestRehydrateOperatorDecisionRoundTrip(t *testing.T) {
	decision := newValidDecision(t, DecisionRejected, "нужна другая формулировка")
	restored, err := RehydrateOperatorDecision(decision.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateOperatorDecision: %v", err)
	}
	if restored != decision {
		t.Fatalf("решение не совпало: %+v и %+v", decision, restored)
	}
}

// approvedSnapshot возвращает корректный снимок для проверки отказов.
func approvedSnapshot(t *testing.T) CampaignSnapshot {
	t.Helper()
	campaign := newCampaign(t)
	if _, _, err := campaign.DraftAction(DraftID("draft-1"), testSpec(t), testMoment()); err != nil {
		t.Fatalf("DraftAction: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(decisionParamsFor(DraftID("draft-1"), 1, "")); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	return campaign.Snapshot()
}

func TestRehydrateCampaignRejectsInvalidSnapshot(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*CampaignSnapshot)
		wantErr error
	}{
		{
			name:    "неизвестное состояние кампании",
			mutate:  func(s *CampaignSnapshot) { s.State = "weird" },
			wantErr: ErrInvalidCampaignState,
		},
		{
			name:    "пустое название",
			mutate:  func(s *CampaignSnapshot) { s.Name = "   " },
			wantErr: ErrEmptyCampaignName,
		},
		{
			name:    "нулевое окно",
			mutate:  func(s *CampaignSnapshot) { s.Window = CampaignWindow{} },
			wantErr: ErrInvalidCampaignWindow,
		},
		{
			name:    "черновик чужой кампании",
			mutate:  func(s *CampaignSnapshot) { s.Drafts[0].Campaign = CampaignID("campaign-2") },
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "неизвестное состояние черновика",
			mutate:  func(s *CampaignSnapshot) { s.Drafts[0].State = "weird" },
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "два черновика с одним id",
			mutate:  func(s *CampaignSnapshot) { s.Drafts = append(s.Drafts, s.Drafts[0]) },
			wantErr: ErrDuplicateDraft,
		},
		{
			name: "два решения по одной версии",
			mutate: func(s *CampaignSnapshot) {
				s.Decisions = append(s.Decisions, s.Decisions[0])
			},
			wantErr: ErrDecisionConflict,
		},
		{
			name:    "решение по версии из будущего",
			mutate:  func(s *CampaignSnapshot) { s.Decisions[0].DraftVersion = 5 },
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "решение по отсутствующему черновику",
			mutate:  func(s *CampaignSnapshot) { s.Decisions[0].Draft = DraftID("draft-9") },
			wantErr: ErrInvalidSnapshot,
		},
		{
			name:    "черновики при состоянии draft",
			mutate:  func(s *CampaignSnapshot) { s.State = CampaignStateDraft },
			wantErr: ErrInvalidSnapshot,
		},
		{
			name: "утверждённый черновик без решения",
			mutate: func(s *CampaignSnapshot) {
				s.Decisions = nil
			},
			wantErr: ErrInvalidSnapshot,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := approvedSnapshot(t)
			tc.mutate(&snapshot)
			if _, err := RehydrateCampaign(snapshot); !errors.Is(err, tc.wantErr) {
				t.Fatalf("ожидалась %v, получено %v", tc.wantErr, err)
			}
		})
	}
}
