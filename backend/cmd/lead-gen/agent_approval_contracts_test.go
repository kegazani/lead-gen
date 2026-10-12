package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/campaigncommand"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

// fakeCampaignCommand — тестовый двойник контракта campaign-command: фиксирует
// поданную форму и отдаёт заготовленный исход.
type fakeCampaignCommand struct {
	result campaigncommand.Result
	err    error
	got    campaigncommand.Submission
}

func (f *fakeCampaignCommand) SubmitForApproval(_ context.Context, in campaigncommand.Submission) (campaigncommand.Result, error) {
	f.got = in
	return f.result, f.err
}

// fakeDraftDecision — тестовый двойник read-контракта draftdecision.
type fakeDraftDecision struct {
	action approvedaction.ApprovedAction
	err    error
	got    draftdecision.Lookup
}

func (f *fakeDraftDecision) ApprovedAction(_ context.Context, in draftdecision.Lookup) (approvedaction.ApprovedAction, error) {
	f.got = in
	return f.action, f.err
}

// TestCampaignCommandSubmitterMapsSubmission проверяет перевод формы подачи:
// непрозрачные ссылки и версия переносятся без искажения, успех владельца — не
// ошибка.
func TestCampaignCommandSubmitterMapsSubmission(t *testing.T) {
	port := &fakeCampaignCommand{result: campaigncommand.Result{CampaignID: "camp-1", Repeated: true}}
	submitter := newCampaignCommandSubmitter(port)
	err := submitter.SubmitForApproval(context.Background(), agentorchestration.ApprovalSubmission{
		TenantID: "tenant-1", RunID: "run-1", MissionID: "mission-1", CompanyID: "company-1",
		DraftRef: "company-1@v2", DraftVersion: 2, MaterialRef: "company-1@v2",
	})
	if err != nil {
		t.Fatalf("SubmitForApproval: %v", err)
	}
	got := port.got
	if got.TenantID != "tenant-1" || got.RunID != "run-1" || got.MissionID != "mission-1" ||
		got.CompanyID != "company-1" || got.DraftRef != "company-1@v2" || got.DraftVersion != 2 ||
		got.MaterialRef != "company-1@v2" {
		t.Fatalf("подача владельцу = %+v", got)
	}
}

// TestCampaignCommandSubmitterMapsErrors проверяет исходы: доменный отказ
// отличается от недоступности; сбой транспорта не выдаётся за приём.
func TestCampaignCommandSubmitterMapsErrors(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"отказ владельца", campaigncommand.ErrRejected, agentorchestration.ErrApprovalSubmitRejected},
		{"недоступность", campaigncommand.ErrUnavailable, agentorchestration.ErrApprovalSubmitUnavailable},
		{"сбой транспорта", errors.New("транспорт"), agentorchestration.ErrApprovalSubmitUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			submitter := newCampaignCommandSubmitter(&fakeCampaignCommand{err: tc.in})
			err := submitter.SubmitForApproval(context.Background(), agentorchestration.ApprovalSubmission{
				TenantID: "t", RunID: "r", CompanyID: "c", DraftRef: "d@v1", DraftVersion: 1, MaterialRef: "d@v1",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// TestCampaignCommandSubmitterWithoutPort проверяет честную недоступность без порта.
func TestCampaignCommandSubmitterWithoutPort(t *testing.T) {
	err := newCampaignCommandSubmitter(nil).SubmitForApproval(context.Background(), agentorchestration.ApprovalSubmission{})
	if !errors.Is(err, agentorchestration.ErrApprovalSubmitUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrApprovalSubmitUnavailable", err)
	}
}

// TestApprovedActionSourceReturnsApproved проверяет перевод запроса и успешное
// чтение действия.
func TestApprovedActionSourceReturnsApproved(t *testing.T) {
	source := &fakeDraftDecision{action: approvedaction.ApprovedAction{ApprovedActionID: "act-1"}}
	adapter := newApprovedActionSource(source)
	got, err := adapter.ApprovedAction(context.Background(), agentorchestration.ApprovedActionLookup{
		TenantID: "tenant-1", DraftRef: "d@v2", DraftVersion: 2,
	})
	if err != nil {
		t.Fatalf("ApprovedAction: %v", err)
	}
	if source.got.TenantID != "tenant-1" || source.got.DraftRef != "d@v2" || source.got.DraftVersion != 2 {
		t.Fatalf("запрос владельцу = %+v", source.got)
	}
	if got.ApprovedActionID != "act-1" {
		t.Fatalf("действие = %+v", got)
	}
}

// TestApprovedActionSourceMapsOutcomes проверяет исходы решения: «не принято»,
// «отклонено», «не найдено» и «недоступно» не смешиваются; положительное решение
// не выдумывается. «Не найдено» — это «ещё не санкционировано» (блок send
// откажет в отправке), а не недоступность источника.
func TestApprovedActionSourceMapsOutcomes(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want error
	}{
		{"решение не принято", draftdecision.ErrPending, agentorchestration.ErrApprovedActionPending},
		{"отклонено", draftdecision.ErrRejected, agentorchestration.ErrApprovedActionRejected},
		{"не найдено", draftdecision.ErrNotFound, agentorchestration.ErrApprovedActionPending},
		{"недоступно", draftdecision.ErrUnavailable, agentorchestration.ErrApprovedActionUnavailable},
		{"сбой транспорта", errors.New("транспорт"), agentorchestration.ErrApprovedActionUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter := newApprovedActionSource(&fakeDraftDecision{err: tc.in})
			_, err := adapter.ApprovedAction(context.Background(), agentorchestration.ApprovedActionLookup{
				TenantID: "t", DraftRef: "d@v1", DraftVersion: 1,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("ошибка = %v, ожидалась %v", err, tc.want)
			}
		})
	}
}

// TestApprovedActionSourceWithoutContract проверяет честную недоступность без порта.
func TestApprovedActionSourceWithoutContract(t *testing.T) {
	_, err := newApprovedActionSource(nil).ApprovedAction(context.Background(), agentorchestration.ApprovedActionLookup{})
	if !errors.Is(err, agentorchestration.ErrApprovedActionUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrApprovedActionUnavailable", err)
	}
}

// TestAssembleAgentApprovalContractsNetwork проверяет сетевую ветку: при заданных
// адресах контракты читаются сетевыми клиентами, и оба вызова доходят до владельца
// по проводу.
func TestAssembleAgentApprovalContractsNetwork(t *testing.T) {
	commandServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/campaign-command" {
			http.Error(w, "неверный путь", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"campaign_id":"camp-1","draft_ref":"d@v2","draft_version":2,"repeated":false}`)
	}))
	defer commandServer.Close()

	decisionServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/approved-action" {
			http.Error(w, "неверный путь", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"outcome":"approved","approved_action":{"approved_action_id":"act-1"}}`)
	}))
	defer decisionServer.Close()

	opts := buildOptions{
		logger:             observ.NewLogger(),
		campaignCommandURL: commandServer.URL,
		draftDecisionURL:   decisionServer.URL,
	}
	assembleAgentApprovalContracts(nil, &opts, nil, opts.logger)
	if opts.approvalSubmitter == nil {
		t.Fatal("порт подачи на утверждение не связан по сети")
	}
	if opts.approvedActionSource == nil {
		t.Fatal("источник утверждённого действия не связан по сети")
	}
	if err := opts.approvalSubmitter.SubmitForApproval(context.Background(), agentorchestration.ApprovalSubmission{
		TenantID: "t", RunID: "r", CompanyID: "c", DraftRef: "d@v2", DraftVersion: 2, MaterialRef: "d@v2",
	}); err != nil {
		t.Fatalf("подача по сети: %v", err)
	}
	action, err := opts.approvedActionSource.ApprovedAction(context.Background(), agentorchestration.ApprovedActionLookup{
		TenantID: "t", DraftRef: "d@v2", DraftVersion: 2,
	})
	if err != nil {
		t.Fatalf("чтение решения по сети: %v", err)
	}
	if action.ApprovedActionID != "act-1" {
		t.Fatalf("действие = %+v", action)
	}
}

// TestAssembleAgentApprovalContractsCoLocated проверяет co-location: при выбранном
// владельце оба порта делят один ленивый мост, а владелец
// outreach-orchestration собирается при первом обращении без цикла.
func TestAssembleAgentApprovalContractsCoLocated(t *testing.T) {
	opts := buildOptions{apiAddr: ":0", logger: observ.NewLogger(), metrics: observ.NewMetrics()}
	buildOnce := supportServiceBuildOnce(t, opts)

	assembleAgentApprovalContracts([]selectedService{{ID: outreachorchestration.ServiceID}}, &opts, buildOnce, opts.logger)

	resolver, ok := opts.approvalSubmitter.(*agentApprovalCoLocated)
	if !ok {
		t.Fatalf("порт подачи = %T, ожидался ленивый co-located мост", opts.approvalSubmitter)
	}
	if _, ok := opts.approvedActionSource.(*agentApprovalCoLocated); !ok {
		t.Fatalf("источник решения = %T, ожидался ленивый co-located мост", opts.approvedActionSource)
	}
	if opts.approvedActionSource.(*agentApprovalCoLocated) != resolver {
		t.Fatal("оба контракта владельца должны делить один ленивый мост")
	}
	if err := resolver.resolve(); err != nil {
		t.Fatalf("ленивое разрешение владельца: %v", err)
	}
	if resolver.submit == nil || resolver.decision == nil {
		t.Fatalf("контракты владельца не связаны in-proc: submit=%v decision=%v", resolver.submit, resolver.decision)
	}
}

// TestAssembleAgentApprovalContractsUnavailable проверяет честную недоступность
// без адресов и без выбранного владельца: порты не подменяются заглушкой.
func TestAssembleAgentApprovalContractsUnavailable(t *testing.T) {
	opts := buildOptions{logger: observ.NewLogger()}
	assembleAgentApprovalContracts(nil, &opts, nil, opts.logger)
	if opts.approvalSubmitter != nil {
		t.Fatal("порт подачи не должен быть связан без адреса и владельца")
	}
	if opts.approvedActionSource != nil {
		t.Fatal("источник решения не должен быть связан без адреса и владельца")
	}
}

// TestAssembleAgentApprovalContractsWiresAgent проверяет интеграцию сборки: при
// выбранном владельце outreach-orchestration сборка сервиса агента с
// подключёнными ApprovalSubmitter и ApprovedActionSource завершается успешно.
func TestAssembleAgentApprovalContractsWiresAgent(t *testing.T) {
	opts := buildOptions{apiAddr: ":0", logger: observ.NewLogger(), metrics: observ.NewMetrics()}
	buildOnce := supportServiceBuildOnce(t, opts)

	assembleAgentApprovalContracts([]selectedService{{ID: outreachorchestration.ServiceID}}, &opts, buildOnce, opts.logger)
	if opts.approvalSubmitter == nil || opts.approvedActionSource == nil {
		t.Fatalf("контракты утверждения не подключены: submit=%v source=%v", opts.approvalSubmitter, opts.approvedActionSource)
	}

	broker := amqp.NewInMemoryBroker(3)
	stub := newPublicIngestStub(":0", observ.NewLogger())
	service, err := buildService(agentorchestration.ServiceID, "in-process", opts, broker, stub)
	if err != nil {
		t.Fatalf("сборка сервиса агента с контрактами утверждения: %v", err)
	}
	if service.ID() != agentorchestration.ServiceID {
		t.Fatalf("ID() = %q", service.ID())
	}
}
