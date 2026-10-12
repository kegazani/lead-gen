package agentcommand

import (
	"context"
	"errors"
	"testing"
)

func TestCommandValidate(t *testing.T) {
	valid := Command{Action: ActionStart, TenantID: "tenant-1", MissionID: "mission-1", CompanyID: "company-1", PipelineID: "pipe-1"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("корректная команда отвергнута: %v", err)
	}
	invalid := Command{Action: ActionStop, TenantID: "tenant-1"} // нет run_id и reason
	if err := invalid.Validate(); !errors.Is(err, ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
	if err := (Command{}).Validate(); !errors.Is(err, ErrRejected) {
		t.Fatalf("пустая команда: ошибка = %v, ожидалась ErrRejected", err)
	}
}

func TestCommandValidateApprovalDecisions(t *testing.T) {
	base := Command{Action: ActionApprove, TenantID: "tenant-1", RunID: "run-1", DraftRef: "company-1@v2", DraftVersion: 2}
	if err := base.Validate(); err != nil {
		t.Fatalf("корректное утверждение отвергнуто: %v", err)
	}

	reject := base
	reject.Action = ActionReject
	if err := reject.Validate(); !errors.Is(err, ErrRejected) {
		t.Fatalf("отказ без причины: ошибка = %v, ожидалась ErrRejected", err)
	}
	reject.Reason = "не тот тон письма"
	if err := reject.Validate(); err != nil {
		t.Fatalf("корректный отказ отвергнут: %v", err)
	}

	cases := map[string]func(*Command){
		"без запуска":    func(c *Command) { c.RunID = "" },
		"без черновика":  func(c *Command) { c.DraftRef = "" },
		"нулевая версия": func(c *Command) { c.DraftVersion = 0 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			cmd := base
			mutate(&cmd)
			if err := cmd.Validate(); !errors.Is(err, ErrRejected) {
				t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
			}
		})
	}
}

func TestExistingActionsUnchanged(t *testing.T) {
	if err := (Command{Action: ActionStart, TenantID: "t", MissionID: "m", CompanyID: "c", PipelineID: "p"}).Validate(); err != nil {
		t.Fatalf("start отвергнут расширением: %v", err)
	}
	if err := (Command{Action: ActionStop, TenantID: "t", RunID: "r", Reason: "причина"}).Validate(); err != nil {
		t.Fatalf("stop отвергнут расширением: %v", err)
	}
	if err := (Command{Action: ActionRetry, TenantID: "t", RunID: "r"}).Validate(); err != nil {
		t.Fatalf("retry отвергнут расширением: %v", err)
	}
}

func TestInProcUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Command(context.Background(), Command{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
