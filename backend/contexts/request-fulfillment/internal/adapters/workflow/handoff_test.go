package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"go.temporal.io/sdk/temporal"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/content-production/contentrequest"
	"lead-gen/contracts/outreach-orchestration/outreachhandoff"
)

// stubContentRequest — заглушка контракта content-request: запоминает запросы и
// отдаёт заданный материал либо ошибку. Пустые поля результата заполняются из
// запроса, чтобы ссылка на материал была детерминированной.
type stubContentRequest struct {
	requests []contentrequest.Intent
	result   contentrequest.Result
	err      error
}

// RequestContent реализует contentrequest.Port.
func (s *stubContentRequest) RequestContent(_ context.Context, in contentrequest.Intent) (contentrequest.Result, error) {
	s.requests = append(s.requests, in)
	if s.err != nil {
		return contentrequest.Result{}, s.err
	}
	result := s.result
	if result.AssetID == "" {
		result.AssetID = in.AssetID
	}
	if result.VersionID == "" {
		result.VersionID = "v1"
	}
	return result, nil
}

// stubOutreachHandoff — заглушка контракта outreach-handoff: запоминает команды и
// отдаёт заданный результат либо ошибку.
type stubOutreachHandoff struct {
	commands      []outreachhandoff.Command
	cancellations []outreachhandoff.RequestCancellation
	result        outreachhandoff.Result
	err           error
}

// Submit реализует outreachhandoff.Port.
func (s *stubOutreachHandoff) Submit(_ context.Context, command outreachhandoff.Command) (outreachhandoff.Result, error) {
	s.commands = append(s.commands, command)
	if s.err != nil {
		return outreachhandoff.Result{}, s.err
	}
	return s.result, nil
}

func (s *stubOutreachHandoff) CancelRequest(_ context.Context, cancellation outreachhandoff.RequestCancellation) error {
	s.cancellations = append(s.cancellations, cancellation)
	return s.err
}

// handoffStoreWith собирает хранилище черновиков с заданными черновиками.
func handoffStoreWith(t *testing.T, drafts ...ports.EmailDraft) ports.EmailDraftStore {
	t.Helper()
	store := persistence.NewDraftStore()
	if err := store.SaveDrafts(context.Background(), drafts); err != nil {
		t.Fatalf("сохранение черновиков: %v", err)
	}
	return store
}

// TestContractHandoffSubmitsExpectedItems проверяет задачу 3.3: для каждого
// черновика с непустой ссылкой на получателя производится материал через
// content-request, а в одну команду передачи попадают лид, получатель и ссылка на
// материал. Тело материала — тема и тело письма, разделённые пустой строкой;
// язык — из черновика либо по умолчанию.
func TestContractHandoffSubmitsExpectedItems(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема a", Body: "тело a", Language: "en", RecipientRef: "contact-a"},
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "тема b", Body: "тело b", RecipientRef: "contact-b"},
	)
	content := &stubContentRequest{}
	handoff := &stubOutreachHandoff{result: outreachhandoff.Result{CampaignID: "camp-1", Accepted: 2}}

	out, err := ContractHandoff{Drafts: store, Content: content, Outreach: handoff}.Handoff(context.Background(), HandoffInput{
		RequestID: "req-1", TenantID: "tenant-1", Actor: "workflow",
	})
	if err != nil {
		t.Fatalf("шаг передачи: %v", err)
	}
	if out.Handed != 2 || out.Skipped != 0 || out.Repeated {
		t.Fatalf("исход = %+v, ожидалось 2 переданных письма без пропусков", out)
	}
	if out.CampaignID != "camp-1" || out.Expected != 2 {
		t.Fatalf("кампания/ожидаемое покрытие = %q/%d, ожидалось camp-1/2", out.CampaignID, out.Expected)
	}

	if len(content.requests) != 2 {
		t.Fatalf("вызовов материала = %d, ожидалось 2", len(content.requests))
	}
	first := content.requests[0]
	if first.RequestID != "req-1-lead-a" || first.TenantID != "tenant-1" {
		t.Fatalf("запрос материала = %+v, ожидались идентификаторы заявки и лида", first)
	}
	if first.AssetID != emailMaterialAssetID("req-1", "lead-a") {
		t.Fatalf("asset_id = %q, ожидался детерминированный", first.AssetID)
	}
	if first.Format != handoffMaterialFormat || first.TemplateID != handoffMaterialTemplate {
		t.Fatalf("формат/шаблон = %q/%q, ожидались %q/%q", first.Format, first.TemplateID, handoffMaterialFormat, handoffMaterialTemplate)
	}
	if first.Language != "en" {
		t.Fatalf("язык = %q, ожидался язык черновика en", first.Language)
	}
	if first.Body != "тема a\n\nтело a" {
		t.Fatalf("тело материала = %q, ожидалась тема и тело через пустую строку", first.Body)
	}
	if content.requests[1].Language != handoffDefaultLanguage {
		t.Fatalf("язык по умолчанию = %q, ожидался %q", content.requests[1].Language, handoffDefaultLanguage)
	}

	if len(handoff.commands) != 1 {
		t.Fatalf("команд передачи = %d, ожидалась 1", len(handoff.commands))
	}
	command := handoff.commands[0]
	if command.RequestID != "req-1" || command.TenantID != "tenant-1" {
		t.Fatalf("команда = %+v, ожидалась заявка req-1 тенанта tenant-1", command)
	}
	// Передача — машинная межсервисная команда: актор пуст, чтобы приёмник не
	// проверял право оператора (актор workflow не член тенанта). Актор материала —
	// непустой (content-request требует происхождение).
	if command.Actor != "" {
		t.Fatalf("передача должна идти без актора, получено %q", command.Actor)
	}
	if first.Actor != "workflow" {
		t.Fatalf("материал должен нести непустого актора, получено %q", first.Actor)
	}
	wantMaterialA := emailMaterialAssetID("req-1", "lead-a") + "@v1"
	wantMaterialB := emailMaterialAssetID("req-1", "lead-b") + "@v1"
	if len(command.Items) != 2 || command.Items[0].LeadID != "lead-a" || command.Items[0].RecipientRef != "contact-a" || command.Items[0].MaterialRef != wantMaterialA {
		t.Fatalf("состав передачи = %+v, ожидались лид, получатель и материал", command.Items)
	}
	if command.Items[1].MaterialRef != wantMaterialB {
		t.Fatalf("материал второго письма = %q, ожидался %q", command.Items[1].MaterialRef, wantMaterialB)
	}
}

// TestContractHandoffSkipsDraftWithoutRecipient проверяет сценарий «письмо без
// получателя не передаётся»: черновик без пригодного адреса честно пропускается,
// материал для него не производится, а пропуск наблюдаем.
func TestContractHandoffSkipsDraftWithoutRecipient(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема a", Body: "тело a", RecipientRef: "contact-a"},
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-b", Subject: "тема b", Body: "тело b"},
	)
	content := &stubContentRequest{}
	handoff := &stubOutreachHandoff{result: outreachhandoff.Result{Accepted: 1}}

	out, err := ContractHandoff{Drafts: store, Content: content, Outreach: handoff}.Handoff(context.Background(), HandoffInput{
		RequestID: "req-1", TenantID: "tenant-1", Actor: "workflow",
	})
	if err != nil {
		t.Fatalf("шаг передачи: %v", err)
	}
	if out.Handed != 1 || out.Skipped != 1 {
		t.Fatalf("исход = %+v, ожидалось 1 переданное и 1 пропущенное", out)
	}
	if len(content.requests) != 1 || content.requests[0].RequestID != "req-1-lead-a" {
		t.Fatalf("запросы материала = %+v, ожидался только пригодный лид", content.requests)
	}
	if len(handoff.commands) != 1 || len(handoff.commands[0].Items) != 1 {
		t.Fatalf("команды = %+v, ожидалось одно письмо в составе", handoff.commands)
	}
	if handoff.commands[0].Items[0].LeadID != "lead-a" {
		t.Fatalf("состав = %+v, непригодный черновик не должен попадать в передачу", handoff.commands[0].Items)
	}
}

// TestContractHandoffNoSuitableDraftsSkipsSubmit проверяет сценарий «нет писем с
// разрешимым получателем»: команда не отправляется вовсе, а пропуск наблюдаем.
func TestContractHandoffNoSuitableDraftsSkipsSubmit(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема", Body: "тело"},
	)
	content := &stubContentRequest{}
	handoff := &stubOutreachHandoff{}

	out, err := ContractHandoff{Drafts: store, Content: content, Outreach: handoff}.Handoff(context.Background(), HandoffInput{
		RequestID: "req-1", TenantID: "tenant-1", Actor: "workflow",
	})
	if err != nil {
		t.Fatalf("шаг передачи: %v", err)
	}
	if out.Handed != 0 || out.Skipped != 1 {
		t.Fatalf("исход = %+v, ожидался один пропуск", out)
	}
	if len(handoff.commands) != 0 {
		t.Fatalf("команд передачи = %d, при отсутствии пригодных писем ожидалось 0", len(handoff.commands))
	}
	if len(content.requests) != 0 {
		t.Fatalf("вызовов материала = %d, ожидалось 0", len(content.requests))
	}
}

// TestContractHandoffNilPortsSkips проверяет, что неподключённый контракт
// материала или передачи — штатный пропуск без падения: шаг не выдумывает
// материал и не выдаёт пропуск за успех.
func TestContractHandoffNilPortsSkips(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема", Body: "тело", RecipientRef: "contact-a"},
	)
	cases := map[string]ContractHandoff{
		"нет обоих контрактов": {Drafts: store},
		"нет материала":        {Drafts: store, Outreach: &stubOutreachHandoff{}},
		"нет передачи":         {Drafts: store, Content: &stubContentRequest{}},
	}
	for name, handoff := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := handoff.Handoff(context.Background(), HandoffInput{RequestID: "req-1", TenantID: "tenant-1"})
			if err != nil {
				t.Fatalf("пропуск без ошибки, получено: %v", err)
			}
			if out != (HandoffOutcome{}) {
				t.Fatalf("исход = %+v, ожидался пустой при пропуске", out)
			}
		})
	}
}

// TestHandoffDraftsNilSenderSkips проверяет, что без собранного источника
// (Activities.Handoff == nil) шаг передачи штатно пропускается, не роняя workflow.
func TestHandoffDraftsNilSenderSkips(t *testing.T) {
	result, err := (&Activities{}).HandoffDrafts(context.Background(), handoffInput{
		RequestID: "req-1", TenantID: "tenant-1", Actor: "workflow",
	})
	if err != nil {
		t.Fatalf("шаг пропущен без ошибки, получено: %v", err)
	}
	if result != (handoffOutcome{}) {
		t.Fatalf("исход = %+v, ожидался пустой", result)
	}
}

// TestHandoffDraftsMaterialUnavailableIsRetryable проверяет сценарий
// «недоступность контракта не маскируется»: сбой владельца материала возвращается
// ошибкой (retryable), а не пропуском или выдуманным успехом.
func TestHandoffDraftsMaterialUnavailableIsRetryable(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема", Body: "тело", RecipientRef: "contact-a"},
	)
	handoff := &stubOutreachHandoff{}
	activities := &Activities{Handoff: ContractHandoff{
		Drafts:   store,
		Content:  &stubContentRequest{err: fmt.Errorf("%w: сеть", contentrequest.ErrUnavailable)},
		Outreach: handoff,
	}}

	_, err := activities.HandoffDrafts(context.Background(), handoffInput{RequestID: "req-1", TenantID: "tenant-1"})
	if err == nil {
		t.Fatal("ожидалась ошибка недоступности материала")
	}
	if !errors.Is(err, contentrequest.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.NonRetryable() {
		t.Fatal("недоступность материала должна ретраиться")
	}
	if len(handoff.commands) != 0 {
		t.Fatal("при недоступности материала команда передачи не должна отправляться")
	}
}

// TestHandoffDraftsRejectedIsNonRetryable проверяет, что доменный отказ владельца
// материала или приёмника помечается неретраибельным и не подменяется успехом.
func TestHandoffDraftsRejectedIsNonRetryable(t *testing.T) {
	store := handoffStoreWith(t,
		ports.EmailDraft{RequestID: "req-1", TenantID: "tenant-1", LeadID: "lead-a", Subject: "тема", Body: "тело", RecipientRef: "contact-a"},
	)
	cases := map[string]*Activities{
		"отказ материала": {Handoff: ContractHandoff{
			Drafts:   store,
			Content:  &stubContentRequest{err: fmt.Errorf("%w: пустое тело", contentrequest.ErrRejected)},
			Outreach: &stubOutreachHandoff{},
		}},
		"отказ передачи": {Handoff: ContractHandoff{
			Drafts:   store,
			Content:  &stubContentRequest{},
			Outreach: &stubOutreachHandoff{err: fmt.Errorf("%w: пустой состав", outreachhandoff.ErrRejected)},
		}},
	}
	for name, activities := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := activities.HandoffDrafts(context.Background(), handoffInput{RequestID: "req-1", TenantID: "tenant-1"})
			if err == nil {
				t.Fatal("ожидалась ошибка отказа")
			}
			var appErr *temporal.ApplicationError
			if !errors.As(err, &appErr) {
				t.Fatalf("ошибка = %v, ожидалась ApplicationError", err)
			}
			if !appErr.NonRetryable() {
				t.Fatal("доменный отказ не должен ретраиться")
			}
		})
	}
}
