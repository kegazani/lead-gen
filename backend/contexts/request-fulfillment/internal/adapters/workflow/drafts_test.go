package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"testing"

	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/lead-management/emailgeneration"
)

// stubEmailGeneration — заглушка контракта email-generation: запоминает запросы и
// отдаёт заданное письмо либо ошибку по лиду.
type stubEmailGeneration struct {
	requests []emailgeneration.Request
	emails   map[string]emailgeneration.Email
	errs     map[string]error
}

// Generate реализует emailgeneration.Port.
func (s *stubEmailGeneration) Generate(_ context.Context, request emailgeneration.Request) (emailgeneration.Email, error) {
	s.requests = append(s.requests, request)
	if err, ok := s.errs[request.LeadID]; ok {
		return emailgeneration.Email{}, err
	}
	email, ok := s.emails[request.LeadID]
	if !ok {
		return emailgeneration.Email{Subject: "тема " + request.LeadID, Body: "тело " + request.LeadID}, nil
	}
	return email, nil
}

type failingDraftStore struct {
	inner  ports.EmailDraftStore
	failAt int
	calls  int
	err    error
}

func (s *failingDraftStore) SaveDrafts(ctx context.Context, drafts []ports.EmailDraft) error {
	s.calls++
	if s.calls == s.failAt {
		return s.err
	}
	return s.inner.SaveDrafts(ctx, drafts)
}

func (s *failingDraftStore) ListByRequest(ctx context.Context, requestID, tenantID string) ([]ports.EmailDraft, error) {
	return s.inner.ListByRequest(ctx, requestID, tenantID)
}

type checkingEmailGeneration struct {
	inner *stubEmailGeneration
	check func(context.Context, string) error
}

func (g *checkingEmailGeneration) Generate(ctx context.Context, request emailgeneration.Request) (emailgeneration.Email, error) {
	if g.check != nil {
		if err := g.check(ctx, request.LeadID); err != nil {
			return emailgeneration.Email{}, err
		}
	}
	return g.inner.Generate(ctx, request)
}

// TestContractDraftsSavesDraftsAndCounts проверяет, что шаг генерирует письмо под
// каждого найденного лида из оффера заявки, сохраняет черновики в хранилище и
// возвращает верный счётчик без текста писем.
func TestContractDraftsSavesDraftsAndCounts(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	result, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b"},
	})
	if err != nil {
		t.Fatalf("шаг генерации писем: %v", err)
	}
	if result.Drafted != 2 || result.Failed != 0 {
		t.Fatalf("результат = %+v, ожидалось 2 черновика без пропусков", result)
	}
	if len(result.LeadIDs) != 2 || result.LeadIDs[0] != "lead-a" || result.LeadIDs[1] != "lead-b" {
		t.Fatalf("лиды с черновиками = %v", result.LeadIDs)
	}

	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(stored) != 2 {
		t.Fatalf("сохранено черновиков = %d, ожидалось 2", len(stored))
	}
	if stored[0].LeadID != "lead-a" || stored[0].Subject != "тема lead-a" || stored[0].Body != "тело lead-a" {
		t.Fatalf("черновик = %+v", stored[0])
	}
	if len(port.requests) != 2 {
		t.Fatalf("вызовов контракта = %d, ожидалось 2", len(port.requests))
	}
	if port.requests[0].Criteria == "" || port.requests[0].RequestID != "req-1" || port.requests[0].TenantID != "tenant-1" {
		t.Fatalf("запрос контракта = %+v, ожидались оффер и идентификаторы заявки", port.requests[0])
	}
}

// TestContractDraftsSavesRecipientRef проверяет задачу 3.2: непрозрачная ссылка на
// получателя из ответа контракта email-generation (поле RecipientRef) попадает в
// сохранённый черновик; пустой ответ оставляет ссылку пустой.
func TestContractDraftsSavesRecipientRef(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{emails: map[string]emailgeneration.Email{
		"lead-a": {Subject: "тема a", Body: "тело a", RecipientRef: "contact-a"},
		"lead-b": {Subject: "тема b", Body: "тело b"},
	}}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	if _, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b"},
	}); err != nil {
		t.Fatalf("шаг генерации писем: %v", err)
	}

	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(stored) != 2 || stored[0].RecipientRef != "contact-a" || stored[1].RecipientRef != "" {
		t.Fatalf("черновики = %+v, ожидались contact-a и пустая ссылка", stored)
	}
}

// TestContractDraftsPartialFailureKeepsStepAlive проверяет выбранное поведение:
// ошибка генерации по одному лиду не роняет шаг — остальные письма сохраняются, а
// исход виден счётчиком Failed.
func TestContractDraftsPartialFailureKeepsStepAlive(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{
		errs: map[string]error{"lead-b": fmt.Errorf("%w: пустой лид", emailgeneration.ErrRejected)},
	}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	result, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b"},
	})
	if err != nil {
		t.Fatalf("ошибка по лиду не должна ронять шаг: %v", err)
	}
	if result.Drafted != 1 || result.Failed != 1 {
		t.Fatalf("результат = %+v, ожидался 1 черновик и 1 пропуск", result)
	}
	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(stored) != 1 || stored[0].LeadID != "lead-a" {
		t.Fatalf("сохранённые черновики = %+v, ожидался только lead-a", stored)
	}
}

func TestContractDraftsSavesBeforeNextGenerationAndKeepsDraftOnGenerationFailure(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{
		errs: map[string]error{"lead-b": fmt.Errorf("provider failed")},
	}
	generator := &checkingEmailGeneration{
		inner: port,
		check: func(ctx context.Context, leadID string) error {
			if leadID != "lead-b" {
				return nil
			}
			stored, err := store.ListByRequest(ctx, "req-1", "tenant-1")
			if err != nil {
				return err
			}
			if len(stored) != 1 || stored[0].LeadID != "lead-a" {
				return fmt.Errorf("перед генерацией lead-b не сохранён lead-a: %+v", stored)
			}
			return nil
		},
	}
	drafter := ContractDrafts{Requests: repo, Contract: generator, Drafts: store}

	result, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b"},
	})
	if err != nil {
		t.Fatalf("шаг генерации писем: %v", err)
	}
	if result.Drafted != 1 || result.Failed != 1 || len(port.requests) != 2 {
		t.Fatalf("результат = %+v, вызовов генерации = %d", result, len(port.requests))
	}
	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение сохранённого черновика: %v", err)
	}
	if len(stored) != 1 || stored[0].LeadID != "lead-a" {
		t.Fatalf("черновик после отказа генерации = %+v", stored)
	}

	port.errs = nil
	if _, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b"},
	}); err != nil {
		t.Fatalf("повторная генерация: %v", err)
	}
	stored, err = store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков после повтора: %v", err)
	}
	if len(stored) != 2 || stored[0].LeadID != "lead-a" || stored[1].LeadID != "lead-b" {
		t.Fatalf("повтор удалил или продублировал ранее сохранённый черновик: %+v", stored)
	}
}

func TestContractDraftsSaveErrorStopsCycleAndPreservesEarlierDraft(t *testing.T) {
	_, repo := newValidatedRequest(t)
	inner := persistence.NewDraftStore()
	saveErr := errors.New("save rejected")
	store := &failingDraftStore{inner: inner, failAt: 2, err: saveErr}
	port := &stubEmailGeneration{}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	_, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b", "lead-c"},
	})
	if !errors.Is(err, saveErr) {
		t.Fatalf("ошибка шага = %v, ожидалась ошибка записи %v", err, saveErr)
	}
	if len(port.requests) != 2 || store.calls != 2 {
		t.Fatalf("генераций = %d, сохранений = %d; цикл должен остановиться после ошибки записи", len(port.requests), store.calls)
	}
	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение предыдущего черновика: %v", err)
	}
	if len(stored) != 1 || stored[0].LeadID != "lead-a" {
		t.Fatalf("ранее сохранённый черновик потерян: %+v", stored)
	}
}

func TestContractDraftsCancellationStopsCycleWithoutCountingFailure(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{errs: map[string]error{"lead-b": context.Canceled}}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	result, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-b", "lead-c"},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка шага = %v, ожидалась отмена контекста", err)
	}
	if result.Drafted != 1 || result.Failed != 0 || len(port.requests) != 2 {
		t.Fatalf("результат отмены = %+v, вызовов генерации = %d", result, len(port.requests))
	}
	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновика после отмены: %v", err)
	}
	if len(stored) != 1 || stored[0].LeadID != "lead-a" {
		t.Fatalf("сохранённый до отмены черновик недоступен: %+v", stored)
	}
}

// TestContractDraftsSkipsDuplicatesAndEmpty проверяет, что повторный и пустой
// идентификаторы лида не порождают лишних вызовов контракта.
func TestContractDraftsSkipsDuplicatesAndEmpty(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	port := &stubEmailGeneration{}
	drafter := ContractDrafts{Requests: repo, Contract: port, Drafts: store}

	result, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a", "lead-a", "  "},
	})
	if err != nil {
		t.Fatalf("шаг генерации писем: %v", err)
	}
	if result.Drafted != 1 {
		t.Fatalf("черновиков = %d, ожидался 1", result.Drafted)
	}
	if len(port.requests) != 1 {
		t.Fatalf("вызовов контракта = %d, ожидался 1", len(port.requests))
	}
}

// TestContractDraftsMissingRequestFails проверяет, что отсутствие заявки — честная
// ошибка шага, а не выдуманный набор черновиков.
func TestContractDraftsMissingRequestFails(t *testing.T) {
	_, repo := newValidatedRequest(t)
	drafter := ContractDrafts{Requests: repo, Contract: &stubEmailGeneration{}, Drafts: persistence.NewDraftStore()}

	_, err := drafter.Draft(context.Background(), DraftInput{
		RequestID: "req-missing", TenantID: "tenant-1", LeadIDs: []string{"lead-a"},
	})
	if err == nil {
		t.Fatal("ожидалась ошибка отсутствующей заявки")
	}
	if !errors.Is(err, ports.ErrRequestNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrRequestNotFound", err)
	}
}

// TestDraftEmailsNilGeneratorSkips проверяет, что без подключённого контракта шаг
// штатно пропускается: activity не выдумывает письма и не роняет workflow.
func TestDraftEmailsNilGeneratorSkips(t *testing.T) {
	activities := &Activities{}
	result, err := activities.DraftEmails(context.Background(), draftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a"},
	})
	if err != nil {
		t.Fatalf("шаг пропущен без ошибки, получено: %v", err)
	}
	if result.Drafted != 0 || result.Failed != 0 {
		t.Fatalf("результат = %+v, ожидался пустой", result)
	}
}

// TestDraftEmailsReturnsCountersOnly проверяет, что activity отдаёт наружу только
// счётчики и идентификаторы лидов, а тексты писем остаются в хранилище.
func TestDraftEmailsReturnsCountersOnly(t *testing.T) {
	_, repo := newValidatedRequest(t)
	store := persistence.NewDraftStore()
	activities := &Activities{
		Drafts: ContractDrafts{Requests: repo, Contract: &stubEmailGeneration{}, Drafts: store},
	}

	result, err := activities.DraftEmails(context.Background(), draftInput{
		RequestID: "req-1", TenantID: "tenant-1", LeadIDs: []string{"lead-a"},
	})
	if err != nil {
		t.Fatalf("шаг генерации писем: %v", err)
	}
	if result.Drafted != 1 || len(result.LeadIDs) != 1 || result.LeadIDs[0] != "lead-a" {
		t.Fatalf("результат = %+v, ожидался 1 черновик по lead-a", result)
	}
	stored, err := store.ListByRequest(context.Background(), "req-1", "tenant-1")
	if err != nil {
		t.Fatalf("чтение черновиков: %v", err)
	}
	if len(stored) != 1 || stored[0].Body == "" {
		t.Fatalf("черновик не сохранён в хранилище: %+v", stored)
	}
}
