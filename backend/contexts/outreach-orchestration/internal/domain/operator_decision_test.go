package domain

import (
	"errors"
	"testing"
	"time"
)

func testDecisionTime() time.Time {
	return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
}

func newValidDecision(t *testing.T, outcome DecisionOutcome, reason string) OperatorDecision {
	t.Helper()
	d, err := newOperatorDecision(
		DecisionID("decision-1"),
		CampaignID("campaign-1"),
		DraftID("draft-1"),
		3,
		ActorID("operator-1"),
		outcome,
		reason,
		testDecisionTime(),
	)
	if err != nil {
		t.Fatalf("newOperatorDecision: %v", err)
	}
	return d
}

func TestOperatorDecisionCarriesExactDraftVersion(t *testing.T) {
	d := newValidDecision(t, DecisionApproved, "")

	if d.DraftVersion() != 3 {
		t.Fatalf("версия черновика %d, ожидалась 3", d.DraftVersion())
	}
	if d.DraftID() != DraftID("draft-1") || d.Actor() != ActorID("operator-1") {
		t.Fatalf("решение потеряло автора или черновик: %+v", d)
	}
	if !d.IsApproval() {
		t.Fatal("положительное решение должно распознаваться как утверждение")
	}
	if !d.DecidedAt().Equal(testDecisionTime()) {
		t.Fatalf("время решения %s", d.DecidedAt())
	}

	rejected := newValidDecision(t, DecisionRejected, "нужен другой тон письма")
	if rejected.IsApproval() {
		t.Fatal("отклонение не должно распознаваться как утверждение")
	}
	if rejected.Reason() == "" {
		t.Fatal("причина отклонения должна сохраняться")
	}
}

func TestOperatorDecisionWithoutDraftVersionIsRejected(t *testing.T) {
	for _, version := range []int{0, -1} {
		_, err := newOperatorDecision(
			DecisionID("decision-1"),
			CampaignID("campaign-1"),
			DraftID("draft-1"),
			version,
			ActorID("operator-1"),
			DecisionApproved,
			"",
			testDecisionTime(),
		)
		if !errors.Is(err, ErrDecisionWithoutVersion) {
			t.Fatalf("версия %d: ожидалась ErrDecisionWithoutVersion, получено %v", version, err)
		}
	}
}

func TestOperatorDecisionValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*OperatorDecision) error
		wantErr error
	}{
		{
			name: "без идентификатора решения",
			mutate: func(d *OperatorDecision) error {
				_, err := newOperatorDecision("", d.campaign, d.draft, 1, d.actor, d.outcome, d.reason, d.decidedAt)
				return err
			},
			wantErr: ErrEmptyID,
		},
		{
			name: "без автора",
			mutate: func(d *OperatorDecision) error {
				_, err := newOperatorDecision(d.id, d.campaign, d.draft, 1, "", d.outcome, d.reason, d.decidedAt)
				return err
			},
			wantErr: ErrEmptyID,
		},
		{
			name: "неизвестный результат",
			mutate: func(d *OperatorDecision) error {
				_, err := newOperatorDecision(d.id, d.campaign, d.draft, 1, d.actor, "maybe", d.reason, d.decidedAt)
				return err
			},
			wantErr: ErrInvalidDecisionOutcome,
		},
		{
			name: "отклонение без причины",
			mutate: func(d *OperatorDecision) error {
				_, err := newOperatorDecision(d.id, d.campaign, d.draft, 1, d.actor, DecisionRejected, "  ", d.decidedAt)
				return err
			},
			wantErr: ErrDecisionReasonRequired,
		},
		{
			name: "без времени",
			mutate: func(d *OperatorDecision) error {
				_, err := newOperatorDecision(d.id, d.campaign, d.draft, 1, d.actor, d.outcome, d.reason, time.Time{})
				return err
			},
			wantErr: ErrDecisionTimeRequired,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := newValidDecision(t, DecisionApproved, "")
			if err := tc.mutate(&base); !errors.Is(err, tc.wantErr) {
				t.Fatalf("ожидалась %v, получено %v", tc.wantErr, err)
			}
		})
	}
}
