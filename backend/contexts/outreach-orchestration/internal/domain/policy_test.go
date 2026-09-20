package domain

import (
	"errors"
	"testing"
)

func commandOf(kind CampaignCommandKind) CampaignCommand {
	return CampaignCommand{
		CommandID:  CommandID("command-1"),
		Kind:       kind,
		TenantID:   TenantID("tenant-1"),
		CampaignID: CampaignID("campaign-1"),
		DraftID:    DraftID("draft-1"),
	}
}

func TestOperatorApprovalRequiredMapsCommandToGateEvent(t *testing.T) {
	cases := []struct {
		kind     CampaignCommandKind
		required bool
	}{
		{CampaignCommandCreateCampaign, false},
		{CampaignCommandDraftAction, true},
		{CampaignCommandReviseDraft, true},
		{CampaignCommandExecuteDraft, true},
		{CampaignCommandApproveDraft, false},
		{CampaignCommandRejectDraft, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got, err := OperatorApprovalRequired(commandOf(tc.kind))
			if err != nil {
				t.Fatalf("OperatorApprovalRequired: %v", err)
			}
			if got.Kind != tc.kind {
				t.Fatalf("политика вернула вид %q", got.Kind)
			}
			if got.RequiresApproval != tc.required {
				t.Fatalf("RequiresApproval=%v, ожидалось %v", got.RequiresApproval, tc.required)
			}
			if got.GateEvent != EventActionApproved {
				t.Fatalf("событие-шлюз %q, ожидалось %q", got.GateEvent, EventActionApproved)
			}
			if got.Reason == "" {
				t.Fatal("политика должна объяснять требование")
			}
		})
	}
}

func TestOperatorApprovalRequiredIsPureFunction(t *testing.T) {
	cmd := commandOf(CampaignCommandExecuteDraft)

	first, err := OperatorApprovalRequired(cmd)
	if err != nil {
		t.Fatalf("OperatorApprovalRequired: %v", err)
	}
	second, err := OperatorApprovalRequired(cmd)
	if err != nil {
		t.Fatalf("OperatorApprovalRequired: %v", err)
	}
	if first != second {
		t.Fatalf("результат политики недетерминирован: %+v и %+v", first, second)
	}
	if cmd.Kind != CampaignCommandExecuteDraft {
		t.Fatal("политика изменила входную команду")
	}

	// Политика не зависит от хранилища: у неё нет других входов, кроме команды,
	// и она не удерживает состояние между вызовами.
	other, err := OperatorApprovalRequired(commandOf(CampaignCommandDraftAction))
	if err != nil {
		t.Fatalf("OperatorApprovalRequired: %v", err)
	}
	if other.RequiresApproval != first.RequiresApproval || other.GateEvent != first.GateEvent {
		t.Fatal("разные команды одного класса должны давать согласованный шлюз")
	}
}

func TestOperatorApprovalRequiredRejectsUnknownCommand(t *testing.T) {
	if _, err := OperatorApprovalRequired(commandOf("send-everything")); !errors.Is(err, ErrUnknownCampaignCommand) {
		t.Fatalf("ожидалась ErrUnknownCampaignCommand, получено %v", err)
	}
}

func TestOperatorApprovalRequiredRejectsIncompleteCommand(t *testing.T) {
	cases := map[string]func(*CampaignCommand){
		"без тенанта":   func(c *CampaignCommand) { c.TenantID = "" },
		"без кампании":  func(c *CampaignCommand) { c.CampaignID = "" },
		"без черновика": func(c *CampaignCommand) { c.DraftID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := commandOf(CampaignCommandDraftAction)
			mutate(&cmd)
			if _, err := OperatorApprovalRequired(cmd); !errors.Is(err, ErrEmptyID) {
				t.Fatalf("ожидалась ErrEmptyID, получено %v", err)
			}
		})
	}
}
