package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// e2eTime — детерминированное время переходов сквозного сценария.
var e2eTime = time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

// fixedClock — часы с фиксированным моментом: время переходов в сквозном сценарии
// проверяемо, а не приходит из системных часов.
type fixedClock struct{ at time.Time }

// Now реализует ports.Clock.
func (c fixedClock) Now() time.Time { return c.at }

// statefulWorkflow — заглушка workflow с состоянием: фаза исполнения выводится из
// доменного статуса заявки, а сигнал решения применяется к агрегату через команды
// среза. Это позволяет провести сквозной сценарий без Temporal и проверить, что
// повторная отправка решения не применяется дважды.
type statefulWorkflow struct {
	mu         sync.Mutex
	slice      *requestfulfillment.Slice
	tenant     string
	started    []ports.StartFulfillment
	decisions  []ports.WorkflowDecision
	deliveries []ports.DeliveryOutcome
}

// StartFulfillment реализует ports.WorkflowStarter.
func (f *statefulWorkflow) StartFulfillment(_ context.Context, in ports.StartFulfillment) (ports.WorkflowRun, error) {
	f.mu.Lock()
	f.started = append(f.started, in)
	f.mu.Unlock()
	return ports.WorkflowRun{WorkflowID: "fulfillment-" + in.RequestID, RunID: "run-1"}, nil
}

// WorkflowStatus реализует ports.WorkflowStatusReader: фаза следует из доменного
// статуса заявки. Пока заявка в processing, исполнение стоит на человеческой паузе;
// после решения фаза терминальна. Заявку вне processing исполнения не имеет.
func (f *statefulWorkflow) WorkflowStatus(ctx context.Context, requestID string) (ports.WorkflowStatus, error) {
	request, err := f.slice.Repository.FindByID(ctx, domain.RequestID(requestID))
	if err != nil {
		if errors.Is(err, ports.ErrRequestNotFound) {
			return ports.WorkflowStatus{}, ports.ErrWorkflowNotFound
		}
		return ports.WorkflowStatus{}, err
	}
	status := ports.WorkflowStatus{
		RequestID: requestID, WorkflowID: "fulfillment-" + requestID, RunID: "run-1", Found: 5,
	}
	switch request.Status() {
	case domain.StatusProcessing:
		status.Phase, status.Execution = query.ApprovalPhaseAwaitingApproval, "RUNNING"
	case domain.StatusFulfilled:
		status.Phase, status.Execution = "fulfilled", "COMPLETED"
	case domain.StatusCancelled:
		status.Phase, status.Execution = "cancelled", "CANCELED"
	default:
		return ports.WorkflowStatus{}, ports.ErrWorkflowNotFound
	}
	return status, nil
}

// SignalApprovalDecision реализует ports.WorkflowSignaler: решение подаётся в
// агрегат командой среза, поэтому доменные инварианты (идемпотентность повторного
// решения, терминальность) действуют и в тесте. Актор и время берутся из команды.
func (f *statefulWorkflow) SignalApprovalDecision(ctx context.Context, in ports.WorkflowDecision) error {
	f.mu.Lock()
	f.decisions = append(f.decisions, in)
	f.mu.Unlock()

	fields := command.LifecycleFields{
		RequestID: domain.RequestID(in.RequestID),
		TenantID:  domain.TenantID(f.tenant),
		Actor:     domain.ActorID(in.Actor),
	}
	if in.Approved {
		_, err := f.slice.FulfillRequest.Handle(ctx, command.FulfillRequest{LifecycleFields: fields})
		return err
	}
	_, err := f.slice.CancelRequest.Handle(ctx, command.CancelRequest{
		LifecycleFields: fields,
		Reason:          "решение оператора: отклонено",
	})
	return err
}

// SignalDeliveryOutcome реализует ports.WorkflowSignaler: исход доставки в тесте
// завершает заявку через ту же команду, что и граф исполнения (все письма
// доставлены → fulfilled; недоставка → no-op, решение остаётся за графом).
func (f *statefulWorkflow) SignalDeliveryOutcome(ctx context.Context, in ports.DeliveryOutcome) error {
	f.mu.Lock()
	f.deliveries = append(f.deliveries, in)
	f.mu.Unlock()
	if !in.Delivered {
		// Недоставка завершением не считается — решение остаётся за графом.
		return nil
	}
	fields := command.LifecycleFields{
		RequestID: domain.RequestID(in.RequestID),
		TenantID:  domain.TenantID(f.tenant),
		Actor:     "delivery",
	}
	_, err := f.slice.FulfillRequest.Handle(ctx, command.FulfillRequest{LifecycleFields: fields})
	return err
}

// Осознанная проверка: заглушка реализует все три порта workflow.
var (
	_ ports.WorkflowStarter      = (*statefulWorkflow)(nil)
	_ ports.WorkflowStatusReader = (*statefulWorkflow)(nil)
	_ ports.WorkflowSignaler     = (*statefulWorkflow)(nil)
)

// newStatefulApprovalServer собирает сервер среза с состоятельной заглушкой
// workflow и фиксированными часами; возвращает её для проверки переходов агрегата.
func newStatefulApprovalServer(t *testing.T) (*httptest.Server, *statefulWorkflow) {
	t.Helper()
	fake := &statefulWorkflow{tenant: tenantID}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		WorkflowStarter:  fake,
		WorkflowStatus:   fake,
		WorkflowSignaler: fake,
		Clock:            fixedClock{at: e2eTime},
		ConsoleUser:      consoleUser,
		ConsolePassword:  consolePassword,
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	fake.slice = slice
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server, fake
}

// loadFulfillmentRequest загружает агрегат сквозного сценария из репозитория среза.
func loadFulfillmentRequest(t *testing.T, fake *statefulWorkflow) *domain.Request {
	t.Helper()
	request, err := fake.slice.Repository.FindByID(context.Background(), domain.RequestID(requestID))
	if err != nil {
		t.Fatalf("загрузка заявки: %v", err)
	}
	return request
}

// lastTransition возвращает последний зафиксированный переход заявки.
func lastTransition(t *testing.T, request *domain.Request) domain.Transition {
	t.Helper()
	transitions := request.Transitions()
	if len(transitions) == 0 {
		t.Fatal("журнал переходов заявки пуст")
	}
	return transitions[len(transitions)-1]
}

// countTransitions считает переходы заявки в заданный статус.
func countTransitions(request *domain.Request, to domain.RequestStatus) int {
	count := 0
	for _, transition := range request.Transitions() {
		if transition.To() == to {
			count++
		}
	}
	return count
}

// postApprovalDecision подаёт решение оператора с валидным CSRF-токеном сессии.
func postApprovalDecision(t *testing.T, client *consoleClient, decision string) *http.Response {
	t.Helper()
	return client.postForm(t, "/requests/"+requestID+"/approval/"+decision, url.Values{
		"tenant_id": {tenantID}, "csrf": {client.csrf},
	})
}

// TestApprovalEndToEndApprove проводит сквозной сценарий среза: подача → валидация →
// processing → фаза awaiting-approval → утверждение из консоли → fulfilled.
// Переход зафиксирован актором из сессии и временем часов.
func TestApprovalEndToEndApprove(t *testing.T) {
	server, fake := newStatefulApprovalServer(t)
	client := prepareApprovalClient(t, server)

	// После processing/start исполнение стоит на человеческой паузе: очередь
	// показывает заявку.
	queue := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
	if !strings.Contains(queue, "Заявка на утверждение") {
		t.Fatalf("очередь не показала заявку на паузе: %s", queue)
	}
	if !strings.Contains(queue, query.ApprovalPhaseAwaitingApproval) {
		t.Fatalf("очередь не показала фазу awaiting-approval: %s", queue)
	}

	resp := postApprovalDecision(t, client, "approve")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	request := loadFulfillmentRequest(t, fake)
	if request.Status() != domain.StatusFulfilled {
		t.Fatalf("статус заявки = %q, ожидался fulfilled", request.Status())
	}
	last := lastTransition(t, request)
	if last.To() != domain.StatusFulfilled {
		t.Fatalf("последний переход = %q, ожидался fulfilled", last.To())
	}
	if last.Actor().String() != consoleUser {
		t.Fatalf("актор перехода = %q, ожидался %q", last.Actor(), consoleUser)
	}
	if !last.At().Equal(e2eTime) {
		t.Fatalf("время перехода = %s, ожидалось %s", last.At(), e2eTime)
	}

	fake.mu.Lock()
	decisions := append([]ports.WorkflowDecision(nil), fake.decisions...)
	fake.mu.Unlock()
	if len(decisions) != 1 || !decisions[0].Approved || decisions[0].Actor != consoleUser {
		t.Fatalf("сигналы утверждения = %+v", decisions)
	}
}

// TestApprovalEndToEndReject проводит исход отказа: на паузе решение «отклонить»
// переводит заявку в cancelled, а переход фиксируется с актором и временем.
func TestApprovalEndToEndReject(t *testing.T) {
	server, fake := newStatefulApprovalServer(t)
	client := prepareApprovalClient(t, server)

	resp := postApprovalDecision(t, client, "reject")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("отклонение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	request := loadFulfillmentRequest(t, fake)
	if request.Status() != domain.StatusCancelled {
		t.Fatalf("статус заявки = %q, ожидался cancelled", request.Status())
	}
	last := lastTransition(t, request)
	if last.To() != domain.StatusCancelled {
		t.Fatalf("последний переход = %q, ожидался cancelled", last.To())
	}
	if last.Actor().String() != consoleUser {
		t.Fatalf("актор перехода = %q, ожидался %q", last.Actor(), consoleUser)
	}
	if !last.At().Equal(e2eTime) {
		t.Fatalf("время перехода = %s, ожидалось %s", last.At(), e2eTime)
	}
	if strings.TrimSpace(last.Reason()) == "" {
		t.Fatal("переход отмены зафиксирован без причины")
	}
}

// TestApprovalDoubleDecisionAppliesOnce проверяет инвариант «двойная отправка даёт
// одно решение»: повторное утверждение доставлено как сигнал, но агрегат получает
// ровно один переход в fulfilled — повтор идемпотентен.
func TestApprovalDoubleDecisionAppliesOnce(t *testing.T) {
	server, fake := newStatefulApprovalServer(t)
	client := prepareApprovalClient(t, server)

	resp := postApprovalDecision(t, client, "approve")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("первое утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()
	version := loadFulfillmentRequest(t, fake).Version()

	resp = postApprovalDecision(t, client, "approve")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("повторное утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	request := loadFulfillmentRequest(t, fake)
	if request.Status() != domain.StatusFulfilled {
		t.Fatalf("статус заявки = %q, ожидался fulfilled", request.Status())
	}
	if got := countTransitions(request, domain.StatusFulfilled); got != 1 {
		t.Fatalf("переходов в fulfilled = %d, ожидался один", got)
	}
	if request.Version() != version {
		t.Fatalf("версия заявки = %d, ожидалась неизменной %d", request.Version(), version)
	}

	fake.mu.Lock()
	delivered := len(fake.decisions)
	fake.mu.Unlock()
	if delivered != 2 {
		t.Fatalf("доставлено сигналов = %d, ожидалось 2", delivered)
	}
}

// TestApprovalRejectAfterApproveNotApplied проверяет инвариант двойного решения в
// паре утверждение → отказ: второе решение не применяется, заявка остаётся
// fulfilled, перехода в cancelled нет.
func TestApprovalRejectAfterApproveNotApplied(t *testing.T) {
	server, fake := newStatefulApprovalServer(t)
	client := prepareApprovalClient(t, server)

	resp := postApprovalDecision(t, client, "approve")
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("утверждение = %d, ожидался 303: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	resp = postApprovalDecision(t, client, "reject")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("отказ после утверждения = %d, ожидался 409: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	request := loadFulfillmentRequest(t, fake)
	if request.Status() != domain.StatusFulfilled {
		t.Fatalf("статус заявки = %q, ожидался fulfilled", request.Status())
	}
	if got := countTransitions(request, domain.StatusCancelled); got != 0 {
		t.Fatalf("переходов в cancelled = %d, ожидался ноль", got)
	}
}
