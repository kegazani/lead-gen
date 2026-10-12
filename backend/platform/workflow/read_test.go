package workflow

import (
	"context"
	"errors"
	"testing"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestNilClientIsNotConfigured проверяет честное поведение без рантайма:
// непригодный клиент не выдумывает данные.
func TestNilClientIsNotConfigured(t *testing.T) {
	var client *Client
	if _, err := client.List(context.Background(), "", 0); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("List: %v", err)
	}
	if _, err := client.Timeline(context.Background(), "wf", ""); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Timeline: %v", err)
	}
	if _, err := client.ReadHistoryPage(context.Background(), "wf", "run", nil, 10); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ReadHistoryPage: %v", err)
	}
}

func TestReadHistoryPageRequiresExactExecutionAndBoundedPage(t *testing.T) {
	client := &Client{}
	if _, err := client.ReadHistoryPage(context.Background(), "wf", "", nil, 10); err == nil {
		t.Fatal("пустой runID должен быть отклонён")
	}
	if _, err := client.ReadHistoryPage(context.Background(), "wf", "run", nil, 1001); err == nil {
		t.Fatal("неограниченный размер страницы должен быть отклонён")
	}
}

// TestExecutionsFromInfos проверяет перевод записей SDK и ограничение списка.
func TestExecutionsFromInfos(t *testing.T) {
	start := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	infos := []*workflowpb.WorkflowExecutionInfo{
		{
			Execution: &commonpb.WorkflowExecution{WorkflowId: "wf-1", RunId: "run-1"},
			Type:      &commonpb.WorkflowType{Name: "AgentRun"},
			Status:    enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
			StartTime: timestamppb.New(start),
		},
		nil,
		{
			Execution: &commonpb.WorkflowExecution{WorkflowId: "wf-2", RunId: "run-2"},
			Type:      &commonpb.WorkflowType{Name: "AgentRun"},
			Status:    enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED,
			CloseTime: timestamppb.New(start.Add(time.Hour)),
		},
	}
	got := executionsFromInfos(infos, 0)
	if len(got) != 2 {
		t.Fatalf("записей: %d, ожидалось 2", len(got))
	}
	if got[0].WorkflowID != "wf-1" || got[0].Type != "AgentRun" || !got[0].Running || got[0].Status != ProcessRunning {
		t.Fatalf("первая запись: %+v", got[0])
	}
	if !got[0].StartTime.Equal(start) {
		t.Fatalf("время старта: %v", got[0].StartTime)
	}
	if got[1].Status != ProcessCompleted || got[1].Running {
		t.Fatalf("вторая запись: %+v", got[1])
	}

	if limited := executionsFromInfos(infos, 1); len(limited) != 1 || limited[0].WorkflowID != "wf-1" {
		t.Fatalf("ограничение списка не сработало: %+v", limited)
	}
}

// TestTimelineFromEvents проверяет разбор истории: исход активности привязывается
// к шагу, таймеры и сигналы попадают в ожидания.
func TestTimelineFromEvents(t *testing.T) {
	t0 := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	events := []*historypb.HistoryEvent{
		{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			EventTime: timestamppb.New(t0),
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "search"},
				},
			},
		},
		{
			EventId:   2,
			EventType: enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED,
			EventTime: timestamppb.New(t0.Add(time.Minute)),
			Attributes: &historypb.HistoryEvent_ActivityTaskFailedEventAttributes{
				ActivityTaskFailedEventAttributes: &historypb.ActivityTaskFailedEventAttributes{ScheduledEventId: 1},
			},
		},
		{
			EventId:   3,
			EventType: enumspb.EVENT_TYPE_TIMER_STARTED,
			EventTime: timestamppb.New(t0.Add(2 * time.Minute)),
			Attributes: &historypb.HistoryEvent_TimerStartedEventAttributes{
				TimerStartedEventAttributes: &historypb.TimerStartedEventAttributes{TimerId: "await-approval"},
			},
		},
		{
			EventId:   4,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED,
			EventTime: timestamppb.New(t0.Add(3 * time.Minute)),
			Attributes: &historypb.HistoryEvent_WorkflowExecutionSignaledEventAttributes{
				WorkflowExecutionSignaledEventAttributes: &historypb.WorkflowExecutionSignaledEventAttributes{SignalName: "approved"},
			},
		},
	}
	process := timelineFromEvents(events)
	if len(process.Steps) != 1 {
		t.Fatalf("шагов: %d, ожидался 1", len(process.Steps))
	}
	step := process.Steps[0]
	if step.Name != "search" || step.Kind != StepKindActivity || step.Outcome != StepFailed {
		t.Fatalf("шаг разобран неверно: %+v", step)
	}
	if len(process.Waits) != 2 {
		t.Fatalf("ожиданий: %d, ожидалось 2", len(process.Waits))
	}
	if process.Waits[0].Kind != WaitKindTimer || process.Waits[0].Name != "await-approval" {
		t.Fatalf("таймер разобран неверно: %+v", process.Waits[0])
	}
	if process.Waits[1].Kind != WaitKindSignal || process.Waits[1].Name != "approved" {
		t.Fatalf("сигнал разобран неверно: %+v", process.Waits[1])
	}
}

// TestNormalizeStatus проверяет перевод статусов SDK в нейтральные коды.
func TestNormalizeStatus(t *testing.T) {
	cases := map[enumspb.WorkflowExecutionStatus]string{
		enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:     ProcessRunning,
		enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:   ProcessCompleted,
		enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:      ProcessFailed,
		enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:   ProcessTimedOut,
		enumspb.WORKFLOW_EXECUTION_STATUS_UNSPECIFIED: ProcessUnknown,
		enumspb.WorkflowExecutionStatus(99):           ProcessUnknown,
	}
	for status, want := range cases {
		if got := normalizeStatus(status); got != want {
			t.Errorf("normalizeStatus(%v) = %q, ожидалось %q", status, got, want)
		}
	}
}
