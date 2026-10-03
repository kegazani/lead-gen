package workflowadapter

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newEnv собирает тестовую среду workflow: регистрирует workflow и activities под
// их рабочими именами. activities создаются без обработчиков — в тестах они
// замещаются моками, реальные адаптеры здесь не исполняются.
func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(Fulfillment, workflow.RegisterOptions{Name: WorkflowName})

	activities := &Activities{}
	env.RegisterActivityWithOptions(activities.HandleStartProcessing, activity.RegisterOptions{Name: ActivityStartProcessing})
	env.RegisterActivityWithOptions(activities.HandleFulfillRequest, activity.RegisterOptions{Name: ActivityFulfillRequest})
	env.RegisterActivityWithOptions(activities.HandleCancelRequest, activity.RegisterOptions{Name: ActivityCancelRequest})
	env.RegisterActivityWithOptions(activities.SearchLeads, activity.RegisterOptions{Name: ActivitySearchLeads})
	env.RegisterActivityWithOptions(activities.DraftEmails, activity.RegisterOptions{Name: ActivityDraftEmails})
	return env
}

// TestFulfillmentHappyPath проверяет путь validated → processing → fulfilled с
// человеческой паузой утверждения результата.
func TestFulfillmentHappyPath(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 5, LeadIDs: []string{"lead-1", "lead-2", "lead-3", "lead-4", "lead-5"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-1", TenantID: "tenant-1", CorrelationID: "corr-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseFulfilled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseFulfilled)
	}
	if status.Found != 5 {
		t.Fatalf("найдено = %d, ожидалось 5", status.Found)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentTopsUpUntilTarget проверяет ветвление «достаточно лидов /
// добор»: пока уникальных лидов меньше цели, шаг поиска повторяется в пределах
// предела, а итог считает уникальные лиды.
func TestFulfillmentTopsUpUntilTarget(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil).Once()
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-2"}}, nil).Once()
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-3"}}, nil).Once()
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-3", TenantID: "tenant-1", LeadTarget: maxTopUpRounds})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Found != maxTopUpRounds {
		t.Fatalf("найдено = %d, ожидалось %d (уникальные лиды добора)", status.Found, maxTopUpRounds)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentCountsUniqueLeads проверяет сценарий «Лид повторяется в раундах
// добора»: один и тот же лид из разных раундов учитывается один раз, а не
// суммируется по раундам.
func TestFulfillmentCountsUniqueLeads(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil).Once()
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-b"}}, nil).Once()
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-b"}}, nil).Once()
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	// Цель недостижима сырыми счётчиками (2+1+1=4), но уникальных лидов всего два:
	// цикл доходит до предела добора и останавливается на двух уникальных.
	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-unique", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Found != 2 {
		t.Fatalf("найдено = %d, ожидалось 2 уникальных лида", status.Found)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentCancelsOnApprovalDenied проверяет отказ на паузе утверждения:
// заявка отменяется вместо перевода в fulfilled.
func TestFulfillmentCancelsOnApprovalDenied(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 5}, nil)
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: false, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-4", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseCancelled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseCancelled)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentReproducesOldGraph проверяет совместимость версий: если новые
// ветки графа (добор, черновики, гейт утверждения) зафиксированы как
// DefaultVersion (прежняя структура), workflow воспроизводится без новых шагов,
// не требуя сигнала.
func TestFulfillmentReproducesOldGraph(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion(changeTopUpLoop, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnGetVersion(changeEmailDrafts, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnGetVersion(changeApprovalGate, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 5}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-5", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseFulfilled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseFulfilled)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
	env.AssertExpectations(t)
}

// TestFulfillmentDraftsEmailsBeforeApproval проверяет, что шаг генерации писем
// идёт после поиска и до паузы утверждения: идентификаторы найденных лидов
// доходят до activity, а счётчик черновиков попадает в наблюдаемое состояние.
func TestFulfillmentDraftsEmailsBeforeApproval(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-draft", TenantID: "tenant-1", LeadTarget: 2})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseFulfilled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseFulfilled)
	}
	if status.Drafted != 2 {
		t.Fatalf("черновиков = %d, ожидалось 2", status.Drafted)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentSkipsDraftStepWithoutLeads проверяет, что без идентификаторов
// лидов шаг генерации писем не запускается: прежние заглушки поиска не выдают
// ссылок, и workflow не выдумывает письма.
func TestFulfillmentSkipsDraftStepWithoutLeads(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 5}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-nodraft", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Drafted != 0 {
		t.Fatalf("черновиков = %d, ожидался 0", status.Drafted)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
}

// TestFulfillmentStatusQuery проверяет, что состояние workflow видно через
// query: после завершения обработчик возвращает финальную фазу.
func TestFulfillmentStatusQuery(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 5}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-6", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	value, err := env.QueryWorkflow(QueryStatus)
	if err != nil {
		t.Fatalf("запрос состояния: %v", err)
	}
	var queried Status
	if err := value.Get(&queried); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if queried.Phase != PhaseFulfilled {
		t.Fatalf("фаза = %q, ожидалась %q", queried.Phase, PhaseFulfilled)
	}
	if queried.RequestID != "req-6" {
		t.Fatalf("request_id = %q, ожидался req-6", queried.RequestID)
	}
}

// TestFulfillmentCancelledPhaseVisibleInQuery проверяет, что после отмены фаза
// видна через query: повтор отклонения не должен оставлять в query прежнюю фазу
// awaiting-approval, пока исполнение уже завершено (defect: состояние по значению).
func TestFulfillmentCancelledPhaseVisibleInQuery(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: false, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-cancel-query", TenantID: "tenant-1", LeadTarget: 1})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	value, err := env.QueryWorkflow(QueryStatus)
	if err != nil {
		t.Fatalf("запрос состояния: %v", err)
	}
	var queried Status
	if err := value.Get(&queried); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if queried.Phase != PhaseCancelled {
		t.Fatalf("фаза = %q, ожидалась %q", queried.Phase, PhaseCancelled)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentApprovalTimeoutCancels проверяет сценарий «Срок утверждения
// истёк»: решение не пришло за срок — заявка переходит в cancelled с причиной
// срока, а не ожидает вечно.
func TestFulfillmentApprovalTimeoutCancels(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == approvalExpiredReason
	})).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-timeout", TenantID: "tenant-1", LeadTarget: 1,
		Policy: ExecutionPolicy{ApprovalTTL: time.Minute},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseCancelled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseCancelled)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

// TestFulfillmentNoLeadsWithoutApprovalIsNotFulfilled проверяет запрет
// молчаливого успеха: при нуле лидов без одобрения оператора заявка не
// завершается fulfilled, а отменяется по сроку.
func TestFulfillmentNoLeadsWithoutApprovalIsNotFulfilled(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{}, nil)
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-noleads", TenantID: "tenant-1", LeadTarget: 3,
		Policy: ExecutionPolicy{ApprovalTTL: time.Minute},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase == PhaseFulfilled {
		t.Fatal("нуль лидов завершён успехом без одобрения оператора")
	}
	if !status.NoLeads {
		t.Fatal("исход «0 лидов» не наблюдаем")
	}
	if status.Phase != PhaseCancelled {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseCancelled)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentNoLeadsApprovedByOperator проверяет сценарий «Оператор явно
// одобряет пустой результат»: только после одобрения заявка становится fulfilled.
func TestFulfillmentNoLeadsApprovedByOperator(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-empty-ok", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseFulfilled || !status.NoLeads {
		t.Fatalf("исход = %+v, ожидался fulfilled с признаком no-leads", status)
	}
}

// TestExecutionPolicyDefaults проверяет осмысленные значения по умолчанию и
// согласование предела исполнения со сроком паузы.
func TestExecutionPolicyDefaults(t *testing.T) {
	policy := ExecutionPolicy{}.withDefaults()
	if policy.ApprovalTTL != defaultApprovalTTL {
		t.Fatalf("срок паузы = %v, ожидался %v", policy.ApprovalTTL, defaultApprovalTTL)
	}
	if policy.ExecutionTimeout < policy.ApprovalTTL {
		t.Fatalf("предел исполнения %v меньше срока паузы %v", policy.ExecutionTimeout, policy.ApprovalTTL)
	}
	if policy.LongStartToClose <= policy.TransitionStartToClose {
		t.Fatalf("таймаут длинных activities %v не больше коротких %v", policy.LongStartToClose, policy.TransitionStartToClose)
	}
	if policy.LongHeartbeat <= 0 {
		t.Fatal("HeartbeatTimeout длинных activities не задан")
	}
	if policy.LongMaxAttempts >= policy.TransitionMaxAttempts {
		t.Fatalf("повторы длинных шагов %d не ограничены относительно коротких %d", policy.LongMaxAttempts, policy.TransitionMaxAttempts)
	}

	// Пользовательский срок паузы растягивает предел исполнения.
	stretched := ExecutionPolicy{ApprovalTTL: 60 * 24 * time.Hour}.withDefaults()
	if stretched.ExecutionTimeout < stretched.ApprovalTTL {
		t.Fatalf("предел исполнения %v меньше пользовательского срока паузы %v", stretched.ExecutionTimeout, stretched.ApprovalTTL)
	}
}

// TestStartHeartbeatOutsideActivityIsSafe проверяет, что heartbeat-хелпер не
// паникует при вызове вне activity-контекста (так activities зовутся из
// юнит-тестов) и что остановка безопасна при повторном вызове.
func TestStartHeartbeatOutsideActivityIsSafe(t *testing.T) {
	stop := startHeartbeat(context.Background(), time.Nanosecond)
	stop()
	stop()
	safeRecordHeartbeat(context.Background())
}
