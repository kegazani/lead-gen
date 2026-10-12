package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// newContract собирает опубликованный контракт request-intent поверх среза заявки.
func newContract(t *testing.T) requestintent.Port {
	t.Helper()
	slice, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	return requestfulfillment.NewRequestIntent(slice)
}

// TestContractSubmitAndStatus проверяет команду подачи и чтение состояния через
// контракт: заявка создаётся в статусе submitted и видна своему тенанту.
func TestContractSubmitAndStatus(t *testing.T) {
	port := newContract(t)
	result, err := port.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Title:   "Автоматизация найма",
		Content: "Предлагаем автоматизацию найма", Target: "IT-компании",
		Actor: "client", CorrelationID: "corr-1",
	})
	if err != nil {
		t.Fatalf("Submit: %v", err)
	}
	if result.RequestID != "req-1" || result.Status != "submitted" {
		t.Fatalf("Submit результат = %+v", result)
	}

	status, err := port.Status(context.Background(), requestintent.StatusQuery{RequestID: "req-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != "submitted" || status.Title != "Автоматизация найма" || status.OfferContent != "Предлагаем автоматизацию найма" {
		t.Fatalf("Status результат = %+v", status)
	}
}

// TestContractTenantIsolation проверяет изоляцию: заявка чужого тенанта
// неотличима от отсутствующей.
func TestContractTenantIsolation(t *testing.T) {
	port := newContract(t)
	if _, err := port.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Title:   "Изоляция тенанта",
		Content: "Оффер", Actor: "client",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	_, err := port.Status(context.Background(), requestintent.StatusQuery{RequestID: "req-1", TenantID: "tenant-2"})
	if !errors.Is(err, requestintent.ErrNotFound) {
		t.Fatalf("чужой тенант: ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestContractEmptyOfferRejected проверяет, что заявка без оффера не создаётся, а
// отказ владельца доходит как ErrRejected.
func TestContractEmptyOfferRejected(t *testing.T) {
	port := newContract(t)
	_, err := port.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "   ", Actor: "client",
	})
	if !errors.Is(err, requestintent.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestContractUnknownRequest проверяет, что отсутствующая заявка даёт ErrNotFound.
func TestContractUnknownRequest(t *testing.T) {
	port := newContract(t)
	_, err := port.Status(context.Background(), requestintent.StatusQuery{RequestID: "ghost", TenantID: "tenant-1"})
	if !errors.Is(err, requestintent.ErrNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestContractList проверяет список заявок клиента: возвращаются заявки тенанта с
// текущими статусами, чужие не попадают.
func TestContractList(t *testing.T) {
	port := newContract(t)
	for _, in := range []requestintent.SubmitIntent{
		{RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1", Title: "Заявка 1", Content: "Оффер 1", Actor: "client"},
		{RequestID: "req-2", OfferID: "offer-2", TenantID: "tenant-1", Title: "Заявка 2", Content: "Оффер 2", Actor: "client"},
		{RequestID: "req-3", OfferID: "offer-3", TenantID: "tenant-2", Title: "Заявка 3", Content: "Оффер 3", Actor: "client"},
	} {
		if _, err := port.Submit(context.Background(), in); err != nil {
			t.Fatalf("Submit %s: %v", in.RequestID, err)
		}
	}

	entries, err := port.List(context.Background(), requestintent.ListQuery{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("List вернул %d записей, ожидалось 2: %+v", len(entries), entries)
	}
	if entries[0].RequestID != "req-1" || entries[0].Title != "Заявка 1" || entries[0].Status != "submitted" {
		t.Fatalf("запись[0] = %+v", entries[0])
	}
	if entries[1].RequestID != "req-2" || entries[1].Title != "Заявка 2" || entries[1].Status != "submitted" {
		t.Fatalf("запись[1] = %+v", entries[1])
	}
}

// TestContractListNilSliceUnavailable проверяет, что без среза List честно
// сообщает о недоступности владельца.
func TestContractListNilSliceUnavailable(t *testing.T) {
	port := requestfulfillment.NewRequestIntent(nil)
	if _, err := port.List(context.Background(), requestintent.ListQuery{TenantID: "tenant-1"}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("List ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestContractStatusCarriesFailureReason проверяет требование 4.2: чтение статуса
// упавшей заявки несёт статус failed, фазу и нормализованный код с текстом
// причины — потребитель не видит «в работе».
func TestContractStatusCarriesFailureReason(t *testing.T) {
	slice, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	port := requestfulfillment.NewRequestIntent(slice)
	ctx := context.Background()

	if _, err := port.Submit(ctx, requestintent.SubmitIntent{
		RequestID: "req-f", OfferID: "offer-f", TenantID: "tenant-1",
		Title:   "Заявка со сбоем",
		Content: "Оффер", Actor: "client",
	}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	fields := command.LifecycleFields{RequestID: "req-f", TenantID: "tenant-1", Actor: "admin"}
	if _, err := slice.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := slice.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields}); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
	if _, err := slice.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: fields}); err != nil {
		t.Fatalf("StartProcessing: %v", err)
	}
	if _, err := slice.FailRequest.Handle(ctx, command.FailRequest{
		LifecycleFields: fields, ReasonCode: "search_failed", Reason: "поиск упал",
	}); err != nil {
		t.Fatalf("FailRequest: %v", err)
	}

	status, err := port.Status(ctx, requestintent.StatusQuery{RequestID: "req-f", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if status.Status != string(domain.StatusFailed) {
		t.Fatalf("статус = %q, ожидался failed", status.Status)
	}
	if status.Phase != "failed" {
		t.Fatalf("фаза = %q, ожидалась failed", status.Phase)
	}
	if status.ErrorReason != "search_failed" || status.ErrorMessage != "поиск упал" {
		t.Fatalf("причина = %q/%q, ожидалась search_failed/поиск упал", status.ErrorReason, status.ErrorMessage)
	}
}

// TestContractNilSliceUnavailable проверяет, что без среза контракт честно
// сообщает о недоступности владельца.
func TestContractNilSliceUnavailable(t *testing.T) {
	port := requestfulfillment.NewRequestIntent(nil)
	if _, err := port.Submit(context.Background(), requestintent.SubmitIntent{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Submit ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := port.Status(context.Background(), requestintent.StatusQuery{}); !errors.Is(err, requestintent.ErrUnavailable) {
		t.Fatalf("Status ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
