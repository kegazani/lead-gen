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
