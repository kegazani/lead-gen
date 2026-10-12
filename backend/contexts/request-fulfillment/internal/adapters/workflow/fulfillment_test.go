package workflowadapter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// newEnv собирает тестовую среду workflow: регистрирует workflow и activities под
// их рабочими именами. activities создаются без обработчиков — в тестах они
// замещаются моками, реальные адаптеры здесь не исполняются.
func newEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	return newEnvWithActivities(t, &Activities{})
}

// newEnvWithActivities собирает тестовую среду workflow с заданными activities.
// Обычные тесты передают пустой набор и замещают шаги моками; тесты передачи —
// реальный HandoffDrafts со стабами контрактов, чтобы проверить состав команды.
func newEnvWithActivities(t *testing.T, activities *Activities) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.RegisterWorkflowWithOptions(Fulfillment, workflow.RegisterOptions{Name: WorkflowName})

	env.RegisterActivityWithOptions(activities.HandleStartProcessing, activity.RegisterOptions{Name: ActivityStartProcessing})
	env.RegisterActivityWithOptions(activities.HandleFulfillRequest, activity.RegisterOptions{Name: ActivityFulfillRequest})
	env.RegisterActivityWithOptions(activities.HandleCancelRequest, activity.RegisterOptions{Name: ActivityCancelRequest})
	env.RegisterActivityWithOptions(activities.HandleFailRequest, activity.RegisterOptions{Name: ActivityFailRequest})
	env.RegisterActivityWithOptions(activities.SearchLeads, activity.RegisterOptions{Name: ActivitySearchLeads})
	env.RegisterActivityWithOptions(activities.DraftEmails, activity.RegisterOptions{Name: ActivityDraftEmails})
	env.RegisterActivityWithOptions(activities.HandoffDrafts, activity.RegisterOptions{Name: ActivityHandoffDrafts})
	return env
}

// assertWorkflowFailed проверяет, что workflow завершился сбоем (Temporal
// close-статус Failed), а не штатно (Completed), и что фаза failed наблюдаема
// через query. Именно close-статус Failed разрешает переиспользование WorkflowID
// при повторном запуске (WorkflowIDReusePolicy = ALLOW_DUPLICATE_FAILED_ONLY).
func assertWorkflowFailed(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow завершился штатно (Completed); ожидался close-статус Failed")
	}
	value, err := env.QueryWorkflow(QueryStatus)
	if err != nil {
		t.Fatalf("запрос состояния: %v", err)
	}
	var status Status
	if err := value.Get(&status); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if status.Phase != PhaseFailed {
		t.Fatalf("фаза = %q, ожидалась %q", status.Phase, PhaseFailed)
	}
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
// идёт после поиска и до паузы утверждения при недоборе: идентификаторы найденных
// лидов доходят до activity, а счётчик черновиков попадает в наблюдаемое
// состояние. Цель 3 при двух уникальных лидах даёт недобор 1 — письма нужны,
// чтобы добрать недостающее.
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

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-draft", TenantID: "tenant-1", LeadTarget: 3})
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
	if status.Shortfall == nil || *status.Shortfall != 1 {
		t.Fatalf("недобор = %v, ожидался 1", status.Shortfall)
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

// TestFulfillmentNoDraftsWhenTargetReached проверяет сценарий «цель достигнута —
// письма не генерируются»: при недоборе равном нулю шаг черновиков не запускается
// автоматически, а наблюдаемый недобор честно равен нулю (не nil). Передача
// черновиков тогда тоже не запускается: без черновиков передавать нечего.
func TestFulfillmentNoDraftsWhenTargetReached(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-no-shortfall", TenantID: "tenant-1", LeadTarget: 2})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Shortfall == nil || *status.Shortfall != 0 {
		t.Fatalf("недобор = %v, при достигнутой цели ожидался 0", status.Shortfall)
	}
	if status.Drafted != 0 {
		t.Fatalf("черновиков = %d, при достигнутой цели ожидался 0", status.Drafted)
	}
	if status.Handed != 0 {
		t.Fatalf("передано = %d, при достигнутой цели ожидался 0", status.Handed)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityHandoffDrafts)
	env.AssertExpectations(t)
}

// TestFulfillmentNoDraftsWithoutLeads проверяет сценарий «0 лидов → письма не
// генерируются»: недобор наблюдается, но под кого генерировать — нет, поэтому
// письма не выдумываются.
func TestFulfillmentNoDraftsWithoutLeads(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-zero-leads", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Shortfall == nil || *status.Shortfall != 3 {
		t.Fatalf("недобор = %v, без лидов при цели 3 ожидался 3", status.Shortfall)
	}
	if status.Drafted != 0 {
		t.Fatalf("черновиков = %d, без лидов ожидался 0", status.Drafted)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
}

// TestFulfillmentShortfallVisibleInQuery проверяет наблюдаемость недобора через
// query status: число недостающих компаний видно наряду с найденными и
// черновиками.
func TestFulfillmentShortfallVisibleInQuery(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-shortfall-query", TenantID: "tenant-1", LeadTarget: 3})
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
	if queried.Shortfall == nil || *queried.Shortfall != 2 {
		t.Fatalf("недобор в query = %v, ожидался 2", queried.Shortfall)
	}
	if queried.Target != 3 {
		t.Fatalf("цель в query = %d, ожидалась 3", queried.Target)
	}
}

// TestFulfillmentShortfallNotCalculatedWithoutTarget проверяет сценарий «цель не
// задана»: недобор не рассчитывается и не показывается выдуманным нулём, письма
// автоматически не генерируются.
func TestFulfillmentShortfallNotCalculatedWithoutTarget(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-no-target", TenantID: "tenant-1"})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Shortfall != nil {
		t.Fatalf("недобор = %d, при отсутствии цели ожидался nil", *status.Shortfall)
	}
	if status.Target != 0 {
		t.Fatalf("цель = %d, при отсутствии цели ожидался 0", status.Target)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityDraftEmails)
}

// TestFulfillmentReproducesDraftsWithoutShortfallGate проверяет совместимость
// версий: исполнение, начатое до появления гейта по недобору (`shortfall-gate` ==
// DefaultVersion), воспроизводит прежнюю структуру — черновики генерируются под
// найденных лидов даже при достигнутой цели.
func TestFulfillmentReproducesDraftsWithoutShortfallGate(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion(changeShortfallGate, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnGetVersion(changeEmailDrafts, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.Version(1) })
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 2, LeadIDs: []string{"lead-a", "lead-b"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-old-graph", TenantID: "tenant-1", LeadTarget: 2})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Drafted != 2 {
		t.Fatalf("черновиков = %d, прежнее исполнение без гейта ожидало 2", status.Drafted)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentDraftStepMissingContractContinues проверяет штатный пропуск при
// неподключённом контракте `email-generation`: шаг черновиков вызывается, но
// возвращает пустой результат, и исполнение продолжает путь с нулём черновиков.
func TestFulfillmentDraftStepMissingContractContinues(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-no-contract", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseFulfilled || status.Drafted != 0 {
		t.Fatalf("исход = %+v, ожидался fulfilled с нулём черновиков", status)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentHandsOffDraftsOnShortfall проверяет сценарий «недобор — письма
// переданы на утверждение»: после генерации черновиков workflow вызывает шаг
// передачи, а черновики заявки с разрешённым получателем и материалом попадают в
// одну команду передачи с ожидаемым составом. Шаг передачи исполняется реально
// (со стабами контрактов материала и передачи), шаги поиска и генерации замещены.
func TestFulfillmentHandsOffDraftsOnShortfall(t *testing.T) {
	ctx := context.Background()
	store := persistence.NewDraftStore()
	if err := store.SaveDrafts(ctx, []ports.EmailDraft{
		{RequestID: "req-ho", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема", Body: "тело", RecipientRef: "contact-a"},
	}); err != nil {
		t.Fatalf("сохранение черновика: %v", err)
	}
	content := &stubContentRequest{}
	handoff := &stubOutreachHandoff{result: outreachhandoff.Result{CampaignID: "camp-req-ho", Accepted: 1}}

	env := newEnvWithActivities(t, &Activities{
		Handoff: ContractHandoff{Drafts: store, Content: content, Outreach: handoff},
	})
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-ho", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Handed != 1 {
		t.Fatalf("передано = %d, ожидалось 1", status.Handed)
	}
	if len(handoff.commands) != 1 {
		t.Fatalf("команд передачи = %d, ожидалась 1", len(handoff.commands))
	}
	command := handoff.commands[0]
	if command.RequestID != "req-ho" || command.TenantID != "tenant-1" {
		t.Fatalf("команда = %+v, ожидалась заявка req-ho тенанта tenant-1", command)
	}
	if len(command.Items) != 1 {
		t.Fatalf("состав передачи = %+v, ожидалось одно письмо", command.Items)
	}
	item := command.Items[0]
	if item.LeadID != "lead-a" || item.RecipientRef != "contact-a" || item.MaterialRef != emailMaterialAssetID("req-ho", "lead-a")+"@v1" {
		t.Fatalf("письмо передачи = %+v, ожидались лид, получатель и материал", item)
	}
	env.AssertExpectations(t)
}

// TestFulfillmentReproducesGraphWithoutHandoff проверяет совместимость версий:
// исполнение, начатое до появления звена передачи (`outreach-handoff` ==
// DefaultVersion), воспроизводит прежний граф — черновики генерируются, но
// передача не запускается.
func TestFulfillmentReproducesGraphWithoutHandoff(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion(changeOutreachHandoff, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-old-handoff", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Drafted != 1 {
		t.Fatalf("черновиков = %d, ожидался 1", status.Drafted)
	}
	if status.Handed != 0 {
		t.Fatalf("передано = %d, прежнее исполнение без передачи ожидало 0", status.Handed)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityHandoffDrafts)
	env.AssertExpectations(t)
}

// TestFulfillmentStepFailureFailsRequest проверяет saga-компенсацию: сбой шага
// поиска переводит заявку в failed с кодом search_failed, и workflow завершается
// штатно (без ошибки), а не падает.
func TestFulfillmentStepFailureFailsRequest(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{}, errors.New("activity: поиск лидов: сеть недоступна")).Once()
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeSearch
	})).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-fail", TenantID: "tenant-1", LeadTarget: 3})
	assertWorkflowFailed(t, env)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

func TestFulfillmentKeepsTerminalIntentPendingAfterBoundedCompensation(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{}, errors.New("исходная ошибка поиска")).Once()
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeSearch && in.Reason == failReason(failCodeSearch)
	})).Return(errors.New("хранилище недоступно")).Times(2)

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-pending", TenantID: "tenant-1", AttemptID: "attempt-7", Generation: 7, CommandID: "cmd-7",
		Policy: ExecutionPolicy{LongMaxAttempts: 1, CompensationMaxAttempts: 2, CompensationScheduleToClose: time.Minute},
	})
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("workflow не должен сообщать об успехе при неподтверждённой компенсации")
	}
	value, err := env.QueryWorkflow(QueryStatus)
	if err != nil {
		t.Fatalf("query состояния: %v", err)
	}
	var status Status
	if err := value.Get(&status); err != nil {
		t.Fatalf("разбор состояния: %v", err)
	}
	if status.Phase != PhasePending {
		t.Fatalf("фаза = %q, ожидалось pending", status.Phase)
	}
	env.AssertExpectations(t)
}

func TestTerminalIntentChecksumCoversIdentityAndOriginalReason(t *testing.T) {
	intent := TerminalIntent{
		Version: 1, WorkflowID: "workflow-1", RunID: "run-1", RequestID: "request-1", TenantID: "tenant-1",
		AttemptID: "attempt-1", Generation: 1, CommandID: "command-1",
		ReasonCode: failCodeSearch, OriginalReason: failReason(failCodeSearch),
	}
	checksum := terminalIntentChecksum(intent)
	if len(checksum) != 64 || checksum != terminalIntentChecksum(intent) {
		t.Fatalf("checksum неканоничен или недетерминирован: %q", checksum)
	}
	intent.OriginalReason += " изменено"
	if checksum == terminalIntentChecksum(intent) {
		t.Fatal("checksum не отражает изменение исходной причины")
	}
	intent.OriginalReason = failReason(failCodeSearch)
	intent.Version++
	if checksum == terminalIntentChecksum(intent) {
		t.Fatal("checksum не отражает изменение версии marker")
	}
}

// TestFulfillmentHandoffFailureFailsRequest проверяет компенсацию сбоя шага
// передачи: код handoff_failed, заявка в failed, workflow завершается штатно.
func TestFulfillmentHandoffFailureFailsRequest(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityHandoffDrafts, mock.Anything, mock.Anything).
		Return(handoffOutcome{}, errors.New("activity: передача писем: недоступна")).Once()
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeHandoff
	})).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-fail-ho", TenantID: "tenant-1", LeadTarget: 3})
	assertWorkflowFailed(t, env)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

// TestFulfillmentDraftFailureFailsRequest проверяет компенсацию сбоя шага
// генерации черновиков: код draft_failed, заявка в failed, workflow завершается
// штатно (без ошибки), а завершение не вызывается.
func TestFulfillmentDraftFailureFailsRequest(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{}, errors.New("activity: генерация черновиков писем: LLM недоступен"))
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeDraft
	})).Return(nil)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-fail-draft", TenantID: "tenant-1", LeadTarget: 3})
	assertWorkflowFailed(t, env)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

// TestFulfillmentTransitionRejectedFailsRequest проверяет, что доменный отказ
// перехода завершения (ошибка activity FulfillRequest) переводит заявку в failed
// с кодом transition_rejected, а не оставляет её в processing.
func TestFulfillmentTransitionRejectedFailsRequest(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-a"}}, nil)
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).
		Return(errors.New("activity FulfillRequest: переход заявки: недопустимый переход"))
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeTransitionRejected
	})).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalApprovalDecision, ApprovalDecision{Approved: true, Actor: "operator"})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-fail-transition", TenantID: "tenant-1", LeadTarget: 1})
	assertWorkflowFailed(t, env)
	env.AssertExpectations(t)
}

// TestFulfillmentExecutionTimeoutFailsRequest проверяет реальный путь
// workflow_timed_out: когда исполнение приближается к внутреннему дедлайну,
// workflow не ждёт терминального kill Temporal, а переводит заявку в failed с
// кодом workflow_timed_out и завершается сбоем (close-статус Failed).
func TestFulfillmentExecutionTimeoutFailsRequest(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityFailRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == failCodeWorkflowTimedOut
	})).Return(nil)

	// Предел исполнения 2ч30м, срок паузы 2ч, запас 1ч: внутренний дедлайн (1ч30м)
	// наступает раньше истечения паузы — workflow должен перейти в failed по
	// таймауту, а не в cancelled по истечении срока паузы.
	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-timeout", TenantID: "tenant-1", LeadTarget: 1,
		Policy: ExecutionPolicy{ApprovalTTL: 2 * time.Hour, ExecutionTimeout: 2*time.Hour + 30*time.Minute},
	})
	assertWorkflowFailed(t, env)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityCancelRequest)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

// TestFulfillmentReproducesOldGraphWithoutFailureCompensation проверяет
// совместимость версий для компенсации сбоя: прежнее исполнение
// (changeFailureCompensation == DefaultVersion) при сбое шага возвращает исходную
// ошибку, роняя workflow, а не переводит заявку в failed.
func TestFulfillmentReproducesOldGraphWithoutFailureCompensation(t *testing.T) {
	env := newEnv(t)
	env.OnGetVersion(changeFailureCompensation, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnGetVersion(changeTerminalIntent, workflow.DefaultVersion, workflow.Version(1)).
		Return(func(string, workflow.Version, workflow.Version) workflow.Version { return workflow.DefaultVersion })
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{}, errors.New("activity: поиск лидов: сеть недоступна")).Once()

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-old-fail", TenantID: "tenant-1", LeadTarget: 3})
	if err := env.GetWorkflowError(); err == nil {
		t.Fatal("прежнее исполнение должно упасть исходной ошибкой шага, а не завершиться штатно")
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFailRequest)
	env.AssertExpectations(t)
}

// TestFulfillmentApprovalExpiryNotReclassifiedAsTimeout проверяет различие
// исходов после паузы утверждения: когда срок утверждения истекает раньше
// дедлайна workflow, заявка переходит в cancelled (срок утверждения истёк), а не
// переклассифицируется в failed (дедлайн workflow истёк).
func TestFulfillmentApprovalExpiryNotReclassifiedAsTimeout(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: 1, LeadIDs: []string{"lead-1"}}, nil)
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == approvalExpiredReason
	})).Return(nil)

	// Срок утверждения 1ч, предел исполнения 3ч (дедлайн = 3ч − 1ч = 2ч > 1ч):
	// срок утверждения истекает первым → cancelled, не failed.
	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-approval-expiry", TenantID: "tenant-1", LeadTarget: 1,
		Policy: ExecutionPolicy{ApprovalTTL: time.Hour, ExecutionTimeout: 3 * time.Hour},
	})
	if err := env.GetWorkflowError(); err != nil {
		t.Fatalf("workflow завершился ошибкой: %v", err)
	}
	var status Status
	if err := env.GetWorkflowResult(&status); err != nil {
		t.Fatalf("чтение результата: %v", err)
	}
	if status.Phase != PhaseCancelled {
		t.Fatalf("фаза = %q, ожидалась %q (срок утверждения истёк)", status.Phase, PhaseCancelled)
	}
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFailRequest)
	env.AssertNotCalled(t, "ExecuteActivity", ActivityFulfillRequest)
	env.AssertExpectations(t)
}

// TestDraftsAllowed проверяет правило гейта «недобор → письма»: при включённом
// гейте нужен наблюдаемый недобор, при выключенном (прежнее исполнение) генерация
// разрешена безусловно.
func TestDraftsAllowed(t *testing.T) {
	zero, positive := 0, 2
	tests := []struct {
		name      string
		gate      workflow.Version
		shortfall *int
		want      bool
	}{
		{"прежнее исполнение без гейта", workflow.DefaultVersion, nil, true},
		{"гейт: цель не задана", 1, nil, false},
		{"гейт: цель достигнута", 1, &zero, false},
		{"гейт: недобор", 1, &positive, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := draftsAllowed(tc.gate, tc.shortfall); got != tc.want {
				t.Fatalf("draftsAllowed(%v, %v) = %v, ожидалось %v", tc.gate, tc.shortfall, got, tc.want)
			}
		})
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

	// Пользовательский срок паузы растягивает предел исполнения с запасом на
	// компенсацию: запас всегда гарантирован и не съедает срок утверждения.
	stretched := ExecutionPolicy{ApprovalTTL: 60 * 24 * time.Hour}.withDefaults()
	if stretched.ExecutionTimeout < stretched.ApprovalTTL {
		t.Fatalf("предел исполнения %v меньше пользовательского срока паузы %v", stretched.ExecutionTimeout, stretched.ApprovalTTL)
	}
	if stretched.ExecutionTimeout < stretched.ApprovalTTL+workflowTimeoutReserve {
		t.Fatalf("предел исполнения %v не покрывает срок паузы %v и запас %v",
			stretched.ExecutionTimeout, stretched.ApprovalTTL, workflowTimeoutReserve)
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

// newHandoffEnv собирает тестовую среду с реальным шагом передачи: письма заявки
// разрешаются в материал и передаются в outreach стабами контрактов, поэтому
// workflow получает наблюдаемое число переданных писем (Expected) для ожидания
// доставки. Возвращает среду и число переданных писем.
func newHandoffEnv(t *testing.T, drafts []ports.EmailDraft) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	env := newEnvWithActivities(t, &Activities{
		Handoff: ContractHandoff{
			Drafts:   handoffStoreWith(t, drafts...),
			Content:  &stubContentRequest{},
			Outreach: &stubOutreachHandoff{result: outreachhandoff.Result{CampaignID: "camp-req-del", Accepted: len(drafts)}},
		},
	})
	leadIDs := make([]string, 0, len(drafts))
	for _, d := range drafts {
		leadIDs = append(leadIDs, d.LeadID)
	}
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).
		Return(searchResult{Found: len(drafts), LeadIDs: leadIDs}, nil)
	env.OnActivity(ActivityDraftEmails, mock.Anything, mock.Anything).
		Return(draftResult{Drafted: len(drafts), LeadIDs: leadIDs}, nil)
	return env
}

// TestFulfillmentDeliveryConcludesRequest проверяет capability
// request-delivery-conclusion: заявка с переданными письмами завершается по
// полному покрытию доставкой, не дожидаясь сигнала утверждения консоли заявок.
func TestFulfillmentDeliveryConcludesRequest(t *testing.T) {
	env := newHandoffEnv(t, []ports.EmailDraft{
		{RequestID: "req-del", TenantID: "tenant-1", LeadID: "lead-a", Subject: "т", Body: "т", RecipientRef: "contact-a"},
	})
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDeliveryOutcome, DeliveryOutcome{MessageID: "msg-1", Delivered: true})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-del", TenantID: "tenant-1", LeadTarget: 3})
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
	env.AssertExpectations(t)
}

// TestFulfillmentDeliveryDuplicateIsIdempotent проверяет идемпотентность
// завершения по доставке: повтор события (at-least-once) не даёт второго перехода.
func TestFulfillmentDeliveryDuplicateIsIdempotent(t *testing.T) {
	env := newHandoffEnv(t, []ports.EmailDraft{
		{RequestID: "req-del-dup", TenantID: "tenant-1", LeadID: "lead-a", Subject: "т", Body: "т", RecipientRef: "contact-a"},
	})
	// Ровно один переход в fulfilled: повтор того же message_id не считается.
	env.OnActivity(ActivityFulfillRequest, mock.Anything, mock.Anything).Return(nil).Once()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDeliveryOutcome, DeliveryOutcome{MessageID: "msg-1", Delivered: true})
		env.SignalWorkflow(SignalDeliveryOutcome, DeliveryOutcome{MessageID: "msg-1", Delivered: true})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-del-dup", TenantID: "tenant-1", LeadTarget: 3})
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
	env.AssertExpectations(t)
}

// TestFulfillmentDeliveryFailureCancels проверяет, что недоставка письма не
// завершает заявку успехом: терминальное покрытие с отказом ведёт к cancelled.
func TestFulfillmentDeliveryFailureCancels(t *testing.T) {
	env := newHandoffEnv(t, []ports.EmailDraft{
		{RequestID: "req-del-fail", TenantID: "tenant-1", LeadID: "lead-a", Subject: "т", Body: "т", RecipientRef: "contact-a"},
	})
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == deliveryFailedReason
	})).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDeliveryOutcome, DeliveryOutcome{MessageID: "msg-1", Delivered: false})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{RequestID: "req-del-fail", TenantID: "tenant-1", LeadTarget: 3})
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

// TestFulfillmentDeliveryIncompleteTimesOut проверяет ограниченность ожидания:
// неполное покрытие доставкой по истечении срока даёт cancelled, а не вечное
// ожидание.
func TestFulfillmentDeliveryIncompleteTimesOut(t *testing.T) {
	env := newHandoffEnv(t, []ports.EmailDraft{
		{RequestID: "req-del-slow", TenantID: "tenant-1", LeadID: "lead-a", Subject: "т", Body: "т", RecipientRef: "contact-a"},
		{RequestID: "req-del-slow", TenantID: "tenant-1", LeadID: "lead-b", Subject: "т", Body: "т", RecipientRef: "contact-b"},
	})
	// Неполное покрытие по истечении срока — недоставка, а не успех.
	env.OnActivity(ActivityCancelRequest, mock.Anything, mock.MatchedBy(func(in transitionInput) bool {
		return in.ReasonCode == deliveryFailedReason
	})).Return(nil)

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalDeliveryOutcome, DeliveryOutcome{MessageID: "msg-1", Delivered: true})
	}, time.Second)

	env.ExecuteWorkflow(Fulfillment, Params{
		RequestID: "req-del-slow", TenantID: "tenant-1", LeadTarget: 3,
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
