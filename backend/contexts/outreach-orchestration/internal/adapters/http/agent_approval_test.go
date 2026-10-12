package httpapi

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/campaigncommand"
	campaigncommandhttp "lead-gen/contracts/outreach-orchestration/campaigncommand/httpport"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
	draftdecisionhttp "lead-gen/contracts/outreach-orchestration/draftdecision/httpport"
)

const approvalDraftRef = domain.DraftID("company-1@v1")

func approvalSubmission() campaigncommand.Submission {
	return campaigncommand.Submission{
		TenantID:     testTenant.String(),
		RunID:        "run-1",
		MissionID:    "mission-1",
		CompanyID:    "company-1",
		DraftRef:     string(approvalDraftRef),
		DraftVersion: 1,
		MaterialRef:  string(approvalDraftRef),
		RecipientRef: "contact-1",
	}
}

func approvalLookup() draftdecision.Lookup {
	return draftdecision.Lookup{TenantID: testTenant.String(), DraftRef: string(approvalDraftRef), DraftVersion: 1}
}

// approveAgentDraft фиксирует положительное решение оператора по текущей версии
// черновика прямо в агрегате: HTTP-тест проверяет чтение решения, а не путь
// утверждения (он покрыт отдельно).
func approveAgentDraft(t *testing.T, fixture *testFixture) {
	t.Helper()
	campaign, err := fixture.repo.FindByID(context.Background(), domain.DraftCampaignID(testTenant, approvalDraftRef))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
		DecisionID:   "decision-1",
		DraftID:      approvalDraftRef,
		DraftVersion: 1,
		Actor:        testOperator,
		DecidedAt:    testNow(),
	}); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}
	campaign.PullDomainEvents()
	if err := fixture.repo.Save(context.Background(), campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}
}

// TestAgentApprovalFlowOverHTTP проводит черновик агента через межсервисные
// контракты: подача (campaign-command) → ожидание решения → утверждение → чтение
// утверждённого действия (draftdecision). Используются настоящие сетевые клиенты
// контрактов поверх реального HTTP-адаптера.
func TestAgentApprovalFlowOverHTTP(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()

	submitClient, err := campaigncommandhttp.New(fixture.server.URL)
	if err != nil {
		t.Fatalf("клиент campaign-command: %v", err)
	}
	readClient, err := draftdecisionhttp.New(fixture.server.URL)
	if err != nil {
		t.Fatalf("клиент draftdecision: %v", err)
	}

	// Подача: черновик встаёт в очередь, повтор идемпотентен.
	result, err := submitClient.SubmitForApproval(ctx, approvalSubmission())
	if err != nil {
		t.Fatalf("SubmitForApproval: %v", err)
	}
	if result.Repeated {
		t.Fatal("первая подача помечена повтором")
	}
	if want := string(domain.DraftCampaignID(testTenant, approvalDraftRef)); result.CampaignID != want {
		t.Fatalf("кампания = %q, ожидалась %q", result.CampaignID, want)
	}
	repeated, err := submitClient.SubmitForApproval(ctx, approvalSubmission())
	if err != nil {
		t.Fatalf("повторная SubmitForApproval: %v", err)
	}
	if !repeated.Repeated {
		t.Fatal("повтор не распознан как идемпотентный")
	}

	// До решения действие не выдаётся.
	if _, err := readClient.ApprovedAction(ctx, approvalLookup()); !errors.Is(err, draftdecision.ErrPending) {
		t.Fatalf("до решения ожидалась ErrPending, получено %v", err)
	}

	approveAgentDraft(t, fixture)

	// После решения чтение отдаёт действие, совпадающее с решением.
	action, err := readClient.ApprovedAction(ctx, approvalLookup())
	if err != nil {
		t.Fatalf("ApprovedAction: %v", err)
	}
	if err := action.Validate(); err != nil {
		t.Fatalf("действие невалидно: %v", err)
	}
	if action.DraftID != string(approvalDraftRef) || action.DraftVersion != 1 {
		t.Fatalf("действие не совпало с решением: %q v%d", action.DraftID, action.DraftVersion)
	}
	if action.DecisionID != "decision-1" {
		t.Fatalf("решение = %q", action.DecisionID)
	}

	// Неизвестный черновик честно не найден.
	if _, err := readClient.ApprovedAction(ctx, draftdecision.Lookup{
		TenantID: testTenant.String(), DraftRef: "other@v1", DraftVersion: 1,
	}); !errors.Is(err, draftdecision.ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}

func TestAgentApprovalRejectionOverHTTP(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()

	submitClient, _ := campaigncommandhttp.New(fixture.server.URL)
	readClient, _ := draftdecisionhttp.New(fixture.server.URL)

	if _, err := submitClient.SubmitForApproval(ctx, approvalSubmission()); err != nil {
		t.Fatalf("SubmitForApproval: %v", err)
	}

	campaign, _ := fixture.repo.FindByID(ctx, domain.DraftCampaignID(testTenant, approvalDraftRef))
	if _, _, err := campaign.RejectDraft(domain.DecisionParams{
		DecisionID:   "decision-1",
		DraftID:      approvalDraftRef,
		DraftVersion: 1,
		Actor:        testOperator,
		Reason:       "нет оффера",
		DecidedAt:    testNow(),
	}); err != nil {
		t.Fatalf("RejectDraft: %v", err)
	}
	campaign.PullDomainEvents()
	if err := fixture.repo.Save(ctx, campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if _, err := readClient.ApprovedAction(ctx, approvalLookup()); !errors.Is(err, draftdecision.ErrRejected) {
		t.Fatalf("ожидалась ErrRejected, получено %v", err)
	}
}

// TestAgentApprovalSurfaceUnavailableWithoutHandlers проверяет честную недоступность:
// без подключённых сценариев оба контракта отвечают недоступностью, а не 200 с
// выдуманным результатом.
func TestAgentApprovalSurfaceUnavailableWithoutHandlers(t *testing.T) {
	server := NewServer(Dependencies{})
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	submitClient, _ := campaigncommandhttp.New(httpServer.URL)
	readClient, _ := draftdecisionhttp.New(httpServer.URL)

	if _, err := submitClient.SubmitForApproval(context.Background(), approvalSubmission()); !errors.Is(err, campaigncommand.ErrUnavailable) {
		t.Fatalf("подача без сценария: ожидалась ErrUnavailable, получено %v", err)
	}
	if _, err := readClient.ApprovedAction(context.Background(), approvalLookup()); !errors.Is(err, draftdecision.ErrUnavailable) {
		t.Fatalf("чтение без сценария: ожидалась ErrUnavailable, получено %v", err)
	}
}

// Гарантия, что конструкторы сценариев остаются совместимы с интерфейсами сервера.
var (
	_ SubmitApprovalUseCase = (*command.SubmitForApprovalHandler)(nil)
	_ ApprovedActionUseCase = (*query.ApprovedActionHandler)(nil)
)
