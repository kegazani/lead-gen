package workflowadapter

import (
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
	return env
}

// TestFulfillmentHappyPath проверяет путь validated → processing → fulfilled с
// человеческой паузой утверждения результата.
func TestFulfillmentHappyPath(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 5}, nil)
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
// добор»: пока лидов меньше цели, шаг поиска повторяется в пределах предела.
func TestFulfillmentTopsUpUntilTarget(t *testing.T) {
	env := newEnv(t)
	env.OnActivity(ActivityStartProcessing, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(ActivitySearchLeads, mock.Anything, mock.Anything).Return(searchResult{Found: 1}, nil).Times(maxTopUpRounds)
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
		t.Fatalf("найдено = %d, ожидалось %d (добор до цели)", status.Found, maxTopUpRounds)
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

// TestFulfillmentReproducesOldGraph проверяет совместимость версий: если
// версия гейта утверждения зафиксирована как DefaultVersion (прежняя структура
// графа), workflow воспроизводится без паузы утверждения, не требуя сигнала.
func TestFulfillmentReproducesOldGraph(t *testing.T) {
	env := newEnv(t)
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
	env.AssertExpectations(t)
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
