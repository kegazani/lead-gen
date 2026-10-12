package outreachhandoff

import (
	"context"
	"errors"
	"testing"
)

func validCommand() Command {
	return Command{
		RequestID: "req-1",
		TenantID:  "tenant-1",
		Actor:     "operator-1",
		Name:      "Кампания заявки",
		Items: []Item{
			{LeadID: "example.com", RecipientRef: "info@example.com", MaterialRef: "asset-1@v1"},
		},
	}
}

func TestCommandValidateAcceptsValid(t *testing.T) {
	if err := validCommand().Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
}

func TestCommandValidateRejectsMissingFields(t *testing.T) {
	command := validCommand()
	command.RequestID = " "
	command.Items[0].RecipientRef = ""
	err := command.Validate()
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestEditorialCommandAllowsNoRecipient(t *testing.T) {
	command := Command{RequestID: "request-1", TenantID: "tenant-1", ExecutionPurpose: ExecutionPurposeEditorial, Items: []Item{{LeadID: "lead-1", MaterialRef: "asset-1@version-1"}}}
	if err := command.Validate(); err != nil {
		t.Fatalf("редакторский материал без email должен быть пригоден к review: %v", err)
	}
	command.ExecutionPurpose = ""
	if err := command.Validate(); !errors.Is(err, ErrRejected) {
		t.Fatalf("legacy delivery без получателя должен отклоняться: %v", err)
	}
}

func TestInProcUnavailableWithoutFunc(t *testing.T) {
	_, err := InProc(InProcHandler{}).Submit(context.Background(), validCommand())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestInProcDelegates(t *testing.T) {
	port := InProc(InProcHandler{SubmitFunc: func(_ context.Context, command Command) (Result, error) {
		return Result{CampaignID: "camp-" + command.RequestID, Accepted: len(command.Items)}, nil
	}})
	result, err := port.Submit(context.Background(), validCommand())
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.CampaignID != "camp-req-1" || result.Accepted != 1 {
		t.Fatalf("результат неполон: %+v", result)
	}
}
