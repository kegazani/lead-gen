package workflow

import (
	"context"
	"fmt"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
)

// Серверное чтение состояния долгого процесса через рантайм Temporal: список
// исполнений и разобранная история (шаги, таймеры, сигналы). Пакет остаётся
// техническим примитивом: он не знает доменных терминов и не толкует исходы —
// отдаёт нейтральные коды, которые экран переводит в подписи.
//
// Недоступность рантайма — честная ошибка (ErrNotConfigured или обёрнутая ошибка
// SDK): вызывающий показывает «недоступно», а не пустой список как успех.

// Нейтральные статусы исполнения процесса.
const (
	// ProcessRunning — исполнение выполняется.
	ProcessRunning = "running"
	// ProcessCompleted — исполнение завершено успешно.
	ProcessCompleted = "completed"
	// ProcessFailed — исполнение завершено отказом.
	ProcessFailed = "failed"
	// ProcessCanceled — исполнение отменено.
	ProcessCanceled = "canceled"
	// ProcessTerminated — исполнение прервано принудительно.
	ProcessTerminated = "terminated"
	// ProcessContinued — исполнение продолжено новым (continue-as-new).
	ProcessContinued = "continued_as_new"
	// ProcessTimedOut — исполнение превысило предел времени.
	ProcessTimedOut = "timed_out"
	// ProcessUnknown — статус не распознан.
	ProcessUnknown = "unknown"
)

// Виды шагов и ожиданий.
const (
	// StepKindActivity — шаг-активность.
	StepKindActivity = "activity"
	// WaitKindTimer — ожидание таймера.
	WaitKindTimer = "timer"
	// WaitKindSignal — ожидание/приём сигнала.
	WaitKindSignal = "signal"
)

// Исходы шага и состояния ожидания.
const (
	// StepScheduled — шаг запланирован.
	StepScheduled = "scheduled"
	// StepCompleted — шаг завершён успешно.
	StepCompleted = "completed"
	// StepFailed — шаг завершён отказом.
	StepFailed = "failed"
	// StepTimedOut — шаг превысил предел времени.
	StepTimedOut = "timed_out"
	// StepCanceled — шаг отменён.
	StepCanceled = "canceled"
	// WaitStarted — ожидание начато.
	WaitStarted = "started"
	// WaitFired — таймер сработал.
	WaitFired = "fired"
	// WaitReceived — сигнал получен.
	WaitReceived = "received"
)

// Пределы чтения: список исполнений и число событий истории на одно исполнение.
// Ограничение защищает экран от перегрузки большим ответом рантайма.
const (
	// DefaultListLimit — предельное число исполнений в списке.
	DefaultListLimit = 100
	// maxListLimit — верхняя граница запрошенного числа исполнений.
	maxListLimit = 500
	// maxTimelineEvents — предел числа событий истории на исполнение.
	maxTimelineEvents = 500
)

// ProcessExecution — обезличенное исполнение workflow: идентификаторы, тип, статус
// и времена. Содержимого payload и персональных данных здесь нет.
type ProcessExecution struct {
	WorkflowID string
	RunID      string
	Type       string
	Status     string
	StartTime  time.Time
	CloseTime  time.Time
	Running    bool
}

// ProcessStep — шаг процесса (активность) с исходом.
type ProcessStep struct {
	Name    string
	Kind    string
	Outcome string
	At      time.Time
}

// ProcessWait — ожидание процесса: таймер или сигнал.
type ProcessWait struct {
	Name  string
	Kind  string
	State string
	At    time.Time
}

// Process — разобранная история одного исполнения: шаги и ожидания.
type Process struct {
	WorkflowID string
	RunID      string
	Steps      []ProcessStep
	Waits      []ProcessWait
}

// HistoryPage — одна ограниченная страница полной истории точного workflow/run.
// Complete true только тогда, когда сервер вернул пустой continuation token.
type HistoryPage struct {
	WorkflowID string
	RunID      string
	Events     []*historypb.HistoryEvent
	NextToken  []byte
	Complete   bool
}

// ReadHistoryPage читает ровно одну страницу полной истории конкретного run.
// В отличие от Timeline, функция не усекает результат и отдаёт курсор Temporal;
// пустой runID запрещён, чтобы не переключиться незаметно на «последний run».
func (c *Client) ReadHistoryPage(ctx context.Context, workflowID, runID string, token []byte, pageSize int) (HistoryPage, error) {
	if c == nil || c.inner == nil {
		return HistoryPage{}, ErrNotConfigured
	}
	if workflowID == "" || runID == "" || pageSize < 1 || pageSize > 1000 {
		return HistoryPage{}, fmt.Errorf("workflow: требуется точный workflow/run и ограниченный размер страницы")
	}
	response, err := c.inner.WorkflowService().GetWorkflowExecutionHistory(ctx, &workflowservice.GetWorkflowExecutionHistoryRequest{
		Namespace:              c.namespace,
		Execution:              &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
		MaximumPageSize:        int32(pageSize),
		NextPageToken:          append([]byte(nil), token...),
		WaitNewEvent:           false,
		HistoryEventFilterType: enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT,
	})
	if err != nil {
		return HistoryPage{}, fmt.Errorf("workflow: страница полной истории %q/%q: %w", workflowID, runID, err)
	}
	return HistoryPage{
		WorkflowID: workflowID,
		RunID:      runID,
		Events:     response.GetHistory().GetEvents(),
		NextToken:  append([]byte(nil), response.GetNextPageToken()...),
		Complete:   len(response.GetNextPageToken()) == 0,
	}, nil
}

// List возвращает исполнения по видимости. query — выражение видимости Temporal
// (SQL-подобное); пусто — все исполнения. limit <= 0 → DefaultListLimit.
func (c *Client) List(ctx context.Context, query string, limit int) ([]ProcessExecution, error) {
	if c == nil || c.inner == nil {
		return nil, ErrNotConfigured
	}
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	response, err := c.inner.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: c.namespace,
		Query:     query,
		PageSize:  int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("workflow: список исполнений: %w", err)
	}
	return executionsFromInfos(response.GetExecutions(), limit), nil
}

// Timeline читает историю исполнения и разбирает её в шаги и ожидания. Пустой
// runID означает текущее (последнее) исполнение. Сбой чтения — ошибка, а не
// пустая история.
func (c *Client) Timeline(ctx context.Context, workflowID, runID string) (Process, error) {
	if c == nil || c.inner == nil {
		return Process{}, ErrNotConfigured
	}
	iterator := c.inner.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	events := make([]*historypb.HistoryEvent, 0, 64)
	for iterator.HasNext() {
		event, err := iterator.Next()
		if err != nil {
			return Process{}, fmt.Errorf("workflow: история %q: %w", workflowID, err)
		}
		events = append(events, event)
		if len(events) >= maxTimelineEvents {
			break
		}
	}
	process := timelineFromEvents(events)
	process.WorkflowID = workflowID
	process.RunID = runID
	return process, nil
}

// executionsFromInfos переводит исполнения SDK в нейтральные записи, ограничивая
// список. nil-элементы пропускаются.
func executionsFromInfos(infos []*workflowpb.WorkflowExecutionInfo, limit int) []ProcessExecution {
	out := make([]ProcessExecution, 0, len(infos))
	for index, info := range infos {
		if limit > 0 && index >= limit {
			break
		}
		if info == nil {
			continue
		}
		execution := ProcessExecution{
			WorkflowID: info.GetExecution().GetWorkflowId(),
			RunID:      info.GetExecution().GetRunId(),
			Type:       info.GetType().GetName(),
			Status:     normalizeStatus(info.GetStatus()),
			Running:    info.GetStatus() == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		}
		if info.GetStartTime() != nil {
			execution.StartTime = info.GetStartTime().AsTime()
		}
		if info.GetCloseTime() != nil {
			execution.CloseTime = info.GetCloseTime().AsTime()
		}
		out = append(out, execution)
	}
	return out
}

// timelineFromEvents разбирает события истории в шаги и ожидания. Исход активности
// привязывается к запланированному шагу по идентификатору события-триггера.
func timelineFromEvents(events []*historypb.HistoryEvent) Process {
	var process Process
	scheduled := make(map[int64]int)
	for _, event := range events {
		if event == nil {
			continue
		}
		at := eventTime(event)
		switch event.GetEventType() {
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			name := attrs.GetActivityType().GetName()
			if name == "" {
				name = StepKindActivity
			}
			process.Steps = append(process.Steps, ProcessStep{Name: name, Kind: StepKindActivity, Outcome: StepScheduled, At: at})
			scheduled[event.GetEventId()] = len(process.Steps) - 1
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			updateStepOutcome(&process, scheduled, event.GetActivityTaskCompletedEventAttributes().GetScheduledEventId(), StepCompleted)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			updateStepOutcome(&process, scheduled, event.GetActivityTaskFailedEventAttributes().GetScheduledEventId(), StepFailed)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			updateStepOutcome(&process, scheduled, event.GetActivityTaskTimedOutEventAttributes().GetScheduledEventId(), StepTimedOut)
		case enumspb.EVENT_TYPE_ACTIVITY_TASK_CANCELED:
			updateStepOutcome(&process, scheduled, event.GetActivityTaskCanceledEventAttributes().GetScheduledEventId(), StepCanceled)
		case enumspb.EVENT_TYPE_TIMER_STARTED:
			process.Waits = append(process.Waits, ProcessWait{Name: event.GetTimerStartedEventAttributes().GetTimerId(), Kind: WaitKindTimer, State: WaitStarted, At: at})
		case enumspb.EVENT_TYPE_TIMER_FIRED:
			process.Waits = append(process.Waits, ProcessWait{Name: event.GetTimerFiredEventAttributes().GetTimerId(), Kind: WaitKindTimer, State: WaitFired, At: at})
		case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			process.Waits = append(process.Waits, ProcessWait{Name: event.GetWorkflowExecutionSignaledEventAttributes().GetSignalName(), Kind: WaitKindSignal, State: WaitReceived, At: at})
		}
	}
	return process
}

// updateStepOutcome меняет исход шага по идентификатору запланированного события.
func updateStepOutcome(process *Process, scheduled map[int64]int, eventID int64, outcome string) {
	index, ok := scheduled[eventID]
	if !ok || index < 0 || index >= len(process.Steps) {
		return
	}
	process.Steps[index].Outcome = outcome
}

// eventTime возвращает время события; отсутствующее время — нулевое (без догадки).
func eventTime(event *historypb.HistoryEvent) time.Time {
	if event.GetEventTime() == nil {
		return time.Time{}
	}
	return event.GetEventTime().AsTime()
}

// normalizeStatus переводит статус SDK в нейтральный код.
func normalizeStatus(status enumspb.WorkflowExecutionStatus) string {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING:
		return ProcessRunning
	case enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED:
		return ProcessCompleted
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED:
		return ProcessFailed
	case enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED:
		return ProcessCanceled
	case enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED:
		return ProcessTerminated
	case enumspb.WORKFLOW_EXECUTION_STATUS_CONTINUED_AS_NEW:
		return ProcessContinued
	case enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT:
		return ProcessTimedOut
	default:
		return ProcessUnknown
	}
}
