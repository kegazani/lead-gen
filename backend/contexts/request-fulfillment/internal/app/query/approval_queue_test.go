package query_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// approvalEpoch — детерминированное время переходов теста.
var approvalEpoch = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// transientError — сбой хранилища или читателя, не относящийся к известным
// исходам порта: очередь обязана пробросить его, а не выдать за «исполнение
// недоступно» или за пустую очередь.
var transientError = errors.New("хранилище: непредвиденный сбой")

// processingRequest создаёт заявку и доводит её до доменного статуса processing —
// единственного, из которого очередь утверждения берёт строки.
func processingRequest(t *testing.T, id, tenant, content, target string) *domain.Request {
	t.Helper()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID:       domain.RequestID(id),
		OfferID:  domain.OfferID("offer-" + id),
		TenantID: domain.TenantID(tenant),
		Content:  content,
		Target:   target,
		Actor:    "actor-1",
		At:       approvalEpoch,
		EventID:  domain.EventID("evt-create-" + id),
	})
	if err != nil {
		t.Fatalf("создание заявки %s: %v", id, err)
	}
	steps := []func(domain.TransitionInput) ([]domain.DomainEvent, error){
		request.StartValidation,
		request.Validate,
		request.StartProcessing,
	}
	for i, step := range steps {
		in := domain.TransitionInput{
			Actor:   "actor-1",
			At:      approvalEpoch.Add(time.Duration(i+1) * time.Minute),
			EventID: domain.EventID(fmt.Sprintf("evt-%s-%d", id, i)),
		}
		if _, err := step(in); err != nil {
			t.Fatalf("переход заявки %s (шаг %d): %v", id, i, err)
		}
	}
	return request
}

// stubRequestRepository — репозиторий, отдающий заранее заданный набор заявок
// (либо ошибку) и запоминающий запрошенный статус.
type stubRequestRepository struct {
	requests []*domain.Request
	listErr  error
	status   domain.RequestStatus
}

// Save реализует ports.RequestRepository; чтению очереди не нужен.
func (s *stubRequestRepository) Save(context.Context, *domain.Request, int) error { return nil }

// FindByID реализует ports.RequestRepository; чтению очереди не нужен.
func (s *stubRequestRepository) FindByID(context.Context, domain.RequestID) (*domain.Request, error) {
	return nil, ports.ErrRequestNotFound
}

// ListByStatus реализует ports.RequestRepository.
func (s *stubRequestRepository) ListByStatus(_ context.Context, status domain.RequestStatus) ([]*domain.Request, error) {
	s.status = status
	return s.requests, s.listErr
}

// stubStatusReader — читатель фазы исполнения: отсутствие записи отвечает
// ErrWorkflowNotFound, общая ошибка — на весь читатель, errs — на конкретную заявку.
type stubStatusReader struct {
	statuses map[string]ports.WorkflowStatus
	errs     map[string]error
	err      error
	seen     []string
}

// WorkflowStatus реализует ports.WorkflowStatusReader.
func (s *stubStatusReader) WorkflowStatus(_ context.Context, requestID string) (ports.WorkflowStatus, error) {
	s.seen = append(s.seen, requestID)
	if err, ok := s.errs[requestID]; ok {
		return ports.WorkflowStatus{}, err
	}
	if s.err != nil {
		return ports.WorkflowStatus{}, s.err
	}
	status, ok := s.statuses[requestID]
	if !ok {
		return ports.WorkflowStatus{}, ports.ErrWorkflowNotFound
	}
	return status, nil
}

// TestApprovalQueueNilReaderUnavailable проверяет, что без читателя фазы очередь
// отвечает отдельным исходом «исполнение недоступно», а не пустой очередью.
func TestApprovalQueueNilReaderUnavailable(t *testing.T) {
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{}, nil)

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if !errors.Is(err, query.ErrApprovalRuntimeUnavailable) {
		t.Fatalf("ошибка = %v, ожидался ErrApprovalRuntimeUnavailable", err)
	}
	if items != nil {
		t.Fatalf("строки = %v, ожидалось nil", items)
	}
}

// TestApprovalQueueTenantIsolation проверяет, что очередь фильтрует заявки по
// тенанту и не обращается к исполнению чужой заявки.
func TestApprovalQueueTenantIsolation(t *testing.T) {
	requests := []*domain.Request{
		processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании"),
		processingRequest(t, "req-b", "tenant-2", "Оффер B", "SaaS-компании"),
	}
	reader := &stubStatusReader{statuses: map[string]ports.WorkflowStatus{
		"req-a": {Phase: query.ApprovalPhaseAwaitingApproval, Found: 3},
		"req-b": {Phase: query.ApprovalPhaseAwaitingApproval, Found: 9},
	}}
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{requests: requests}, reader)

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("очередь: %v", err)
	}
	if len(items) != 1 || items[0].RequestID != "req-a" {
		t.Fatalf("строки = %+v, ожидалась одна заявка req-a", items)
	}
	if len(reader.seen) != 1 || reader.seen[0] != "req-a" {
		t.Fatalf("читатель фазы вызван для %v, ожидалась только req-a", reader.seen)
	}
}

// TestApprovalQueueSkipsNonAwaitingPhase проверяет, что заявка не в фазе
// awaiting-approval в очередь не входит.
func TestApprovalQueueSkipsNonAwaitingPhase(t *testing.T) {
	requests := []*domain.Request{processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании")}
	reader := &stubStatusReader{statuses: map[string]ports.WorkflowStatus{
		"req-a": {Phase: "searching", Execution: "RUNNING", Found: 4},
	}}
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{requests: requests}, reader)

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("очередь: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("строки = %+v, ожидалась пустая очередь", items)
	}
}

// TestApprovalQueueSkipsWorkflowNotFound проверяет, что отсутствие исполнения —
// пропуск, а не ошибка очереди.
func TestApprovalQueueSkipsWorkflowNotFound(t *testing.T) {
	requests := []*domain.Request{processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании")}
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{requests: requests}, &stubStatusReader{})

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("очередь: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("строки = %+v, ожидалась пустая очередь", items)
	}
}

// TestApprovalQueueRuntimeUnavailable проверяет, что недоступность рантайма
// чтения фазы даёт исход «исполнение недоступно», а не пропуск заявки.
func TestApprovalQueueRuntimeUnavailable(t *testing.T) {
	requests := []*domain.Request{processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании")}
	reader := &stubStatusReader{errs: map[string]error{"req-a": ports.ErrWorkflowUnavailable}}
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{requests: requests}, reader)

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if !errors.Is(err, query.ErrApprovalRuntimeUnavailable) {
		t.Fatalf("ошибка = %v, ожидался ErrApprovalRuntimeUnavailable", err)
	}
	if !strings.Contains(err.Error(), ports.ErrWorkflowUnavailable.Error()) {
		t.Fatalf("ошибка %v не сохранила причину ErrWorkflowUnavailable", err)
	}
	if items != nil {
		t.Fatalf("строки = %v, ожидалось nil", items)
	}
}

// TestApprovalQueuePropagatesReaderError проверяет, что прочая ошибка читателя
// пробрасывается как есть и не выдаётся за «исполнение недоступно».
func TestApprovalQueuePropagatesReaderError(t *testing.T) {
	requests := []*domain.Request{processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании")}
	reader := &stubStatusReader{errs: map[string]error{"req-a": transientError}}
	handler := query.NewApprovalQueueHandler(&stubRequestRepository{requests: requests}, reader)

	_, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if !errors.Is(err, transientError) {
		t.Fatalf("ошибка = %v, ожидался проброс %v", err, transientError)
	}
	if errors.Is(err, query.ErrApprovalRuntimeUnavailable) {
		t.Fatalf("прочая ошибка выдана за недоступность исполнения: %v", err)
	}
}

// TestApprovalQueueRepositoryError проверяет, что сбой хранилища пробрасывается.
func TestApprovalQueueRepositoryError(t *testing.T) {
	repository := &stubRequestRepository{listErr: transientError}
	handler := query.NewApprovalQueueHandler(repository, &stubStatusReader{})

	_, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if !errors.Is(err, transientError) {
		t.Fatalf("ошибка = %v, ожидался проброс %v", err, transientError)
	}
	if repository.status != domain.StatusProcessing {
		t.Fatalf("запрошен статус %q, ожидался processing", repository.status)
	}
}

// TestApprovalQueueItemFields проверяет состав строки очереди: идентификаторы,
// оффер, версия, фаза, состояние исполнения и найденное число.
func TestApprovalQueueItemFields(t *testing.T) {
	requests := []*domain.Request{processingRequest(t, "req-a", "tenant-1", "Оффер A", "IT-компании")}
	reader := &stubStatusReader{statuses: map[string]ports.WorkflowStatus{
		"req-a": {
			RequestID: "req-a", WorkflowID: "fulfillment-req-a", RunID: "run-1",
			Phase: query.ApprovalPhaseAwaitingApproval, Execution: "RUNNING", Found: 7,
		},
	}}
	repository := &stubRequestRepository{requests: requests}
	handler := query.NewApprovalQueueHandler(repository, reader)

	items, err := handler.Handle(context.Background(), query.ApprovalQueue{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("очередь: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("строк = %d, ожидалась 1", len(items))
	}
	item := items[0]
	if item.RequestID != "req-a" || item.TenantID != "tenant-1" {
		t.Fatalf("идентификаторы строки = %q/%q", item.RequestID, item.TenantID)
	}
	if item.Offer.OfferID != "offer-req-a" || item.Offer.Content != "Оффер A" || item.Offer.Target != "IT-компании" || item.Offer.Revision != 1 {
		t.Fatalf("оффер строки = %+v", item.Offer)
	}
	if item.Version != 4 {
		t.Fatalf("версия строки = %d, ожидалась 4 (1 + три перехода)", item.Version)
	}
	if item.Phase != query.ApprovalPhaseAwaitingApproval || item.Execution != "RUNNING" || item.Found != 7 {
		t.Fatalf("состояние строки = %q/%q/%d", item.Phase, item.Execution, item.Found)
	}
	if repository.status != domain.StatusProcessing {
		t.Fatalf("запрошен статус %q, ожидался processing", repository.status)
	}
}
