package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
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
	if status.Status != "submitted" || status.OfferContent != "Предлагаем автоматизацию найма" {
		t.Fatalf("Status результат = %+v", status)
	}
}

// TestContractTenantIsolation проверяет изоляцию: заявка чужого тенанта
// неотличима от отсутствующей.
func TestContractTenantIsolation(t *testing.T) {
	port := newContract(t)
	if _, err := port.Submit(context.Background(), requestintent.SubmitIntent{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
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
