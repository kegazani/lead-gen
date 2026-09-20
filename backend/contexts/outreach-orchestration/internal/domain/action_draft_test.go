package domain

import (
	"errors"
	"testing"
)

func draftInState(t *testing.T, state DraftState) *ActionDraft {
	t.Helper()
	d, err := newActionDraft(DraftID("draft-1"), CampaignID("campaign-1"), testSpec(t))
	if err != nil {
		t.Fatalf("newActionDraft: %v", err)
	}
	apply := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("переход в %q: %v", state, err)
		}
	}
	switch state {
	case DraftStateProposed:
	case DraftStatePendingApproval:
		apply(d.submit())
	case DraftStateApproved:
		apply(d.submit())
		apply(d.approve())
	case DraftStateRejected:
		apply(d.submit())
		apply(d.reject())
	case DraftStateExpired:
		apply(d.submit())
		apply(d.expire())
	case DraftStateExecuted:
		apply(d.submit())
		apply(d.approve())
		apply(d.markExecuted())
	case DraftStateFailed:
		apply(d.submit())
		apply(d.approve())
		apply(d.markFailed())
	default:
		t.Fatalf("неизвестное состояние %q", state)
	}
	if d.State() != state {
		t.Fatalf("ожидалось состояние %q, получено %q", state, d.State())
	}
	return d
}

func TestNewActionDraftStartsProposedWithVersionOne(t *testing.T) {
	d, err := newActionDraft(DraftID("draft-1"), CampaignID("campaign-1"), testSpec(t))
	if err != nil {
		t.Fatalf("newActionDraft: %v", err)
	}
	if d.State() != DraftStateProposed {
		t.Fatalf("начальное состояние %q, ожидалось %q", d.State(), DraftStateProposed)
	}
	if d.Version() != 1 {
		t.Fatalf("начальная версия %d, ожидалась 1", d.Version())
	}
	if d.Channel().String() != ActionChannelEmailName {
		t.Fatalf("канал %q", d.Channel())
	}
}

func TestNewActionDraftRejectsIncompleteSpec(t *testing.T) {
	spec := testSpec(t)
	cases := map[string]func(DraftSpec) DraftSpec{
		"без материала": func(s DraftSpec) DraftSpec { s.Material = ""; return s },
		"без сегмента":  func(s DraftSpec) DraftSpec { s.Segment = ""; return s },
		"без адресата":  func(s DraftSpec) DraftSpec { s.Recipient = ""; return s },
		"без канала":    func(s DraftSpec) DraftSpec { s.Channel = ActionChannel{}; return s },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := newActionDraft(DraftID("d1"), CampaignID("c1"), mutate(spec)); err == nil {
				t.Fatal("ожидалась доменная ошибка")
			}
		})
	}
	if _, err := newActionDraft("", CampaignID("c1"), spec); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("ожидалась ErrEmptyID, получено %v", err)
	}
	if _, err := newActionDraft(DraftID("d1"), "", spec); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("ожидалась ErrEmptyID, получено %v", err)
	}
}

func TestActionDraftValidTransitions(t *testing.T) {
	states := []DraftState{
		DraftStatePendingApproval,
		DraftStateApproved,
		DraftStateRejected,
		DraftStateExpired,
		DraftStateExecuted,
		DraftStateFailed,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			draftInState(t, state)
		})
	}
}

func TestActionDraftRejectsInvalidTransitions(t *testing.T) {
	cases := []struct {
		name string
		from DraftState
		move func(*ActionDraft) error
	}{
		{"утвердить до постановки", DraftStateProposed, func(d *ActionDraft) error { return d.approve() }},
		{"отклонить до постановки", DraftStateProposed, func(d *ActionDraft) error { return d.reject() }},
		{"исполнить до постановки", DraftStateProposed, func(d *ActionDraft) error { return d.markExecuted() }},
		{"поставить повторно", DraftStatePendingApproval, func(d *ActionDraft) error { return d.submit() }},
		{"исполнить без решения", DraftStatePendingApproval, func(d *ActionDraft) error { return d.markExecuted() }},
		{"сбой без решения", DraftStatePendingApproval, func(d *ActionDraft) error { return d.markFailed() }},
		{"просрочить утверждённый", DraftStateApproved, func(d *ActionDraft) error { return d.expire() }},
		{"утвердить утверждённый", DraftStateApproved, func(d *ActionDraft) error { return d.approve() }},
		{"отклонить утверждённый", DraftStateApproved, func(d *ActionDraft) error { return d.reject() }},
		{"исполнить повторно", DraftStateExecuted, func(d *ActionDraft) error { return d.markExecuted() }},
		{"доработать исполненный", DraftStateExecuted, func(d *ActionDraft) error { return d.revise(testSpec(t)) }},
		{"доработать сбойный", DraftStateFailed, func(d *ActionDraft) error { return d.revise(testSpec(t)) }},
		{"исполнить отклонённый", DraftStateRejected, func(d *ActionDraft) error { return d.markExecuted() }},
		{"просрочить отклонённый", DraftStateRejected, func(d *ActionDraft) error { return d.expire() }},
		{"просрочить просроченный", DraftStateExpired, func(d *ActionDraft) error { return d.expire() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			draft := draftInState(t, tc.from)
			state, version := draft.State(), draft.Version()
			if err := tc.move(draft); !errors.Is(err, ErrInvalidDraftTransition) {
				t.Fatalf("ожидалась ErrInvalidDraftTransition, получено %v", err)
			}
			if draft.State() != state || draft.Version() != version {
				t.Fatalf("состояние изменилось при запрещённом переходе: %q v%d", draft.State(), draft.Version())
			}
		})
	}
}

func TestActionDraftRevisionBumpsVersionAndReturnsToApproval(t *testing.T) {
	for _, from := range []DraftState{
		DraftStateProposed,
		DraftStatePendingApproval,
		DraftStateApproved,
		DraftStateRejected,
	} {
		t.Run(string(from), func(t *testing.T) {
			draft := draftInState(t, from)
			version := draft.Version()

			revised := testSpec(t)
			revised.Channel = mustChannel(t, ActionChannelCallName)
			if err := draft.revise(revised); err != nil {
				t.Fatalf("revise: %v", err)
			}
			if draft.Version() != version+1 {
				t.Fatalf("версия %d, ожидалась %d", draft.Version(), version+1)
			}
			if draft.State() != DraftStatePendingApproval {
				t.Fatalf("состояние %q, ожидалось %q", draft.State(), DraftStatePendingApproval)
			}
			if draft.Channel().String() != ActionChannelCallName {
				t.Fatalf("состав не заменён: %q", draft.Channel())
			}

			// Каждая следующая правка повышает версию ещё раз.
			if err := draft.revise(testSpec(t)); err != nil {
				t.Fatalf("повторная revise: %v", err)
			}
			if draft.Version() != version+2 {
				t.Fatalf("версия %d, ожидалась %d", draft.Version(), version+2)
			}
		})
	}
}

func TestDraftCheckVersion(t *testing.T) {
	draft := draftInState(t, DraftStatePendingApproval)
	if err := draft.checkVersion(1); err != nil {
		t.Fatalf("совпадающая версия должна приниматься: %v", err)
	}
	for _, stale := range []int{0, -1, 2} {
		if err := draft.checkVersion(stale); !errors.Is(err, ErrDraftVersionMismatch) {
			t.Fatalf("версия %d: ожидалась ErrDraftVersionMismatch, получено %v", stale, err)
		}
	}
}
