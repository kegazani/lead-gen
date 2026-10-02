package domain

import (
	"errors"
	"testing"
)

func newTestSuccessFee(t *testing.T, confirmed bool) *SuccessFee {
	t.Helper()
	base, err := NewMoney(200000, "rub")
	if err != nil {
		t.Fatalf("база расчёта: %v", err)
	}
	calculation, err := NewCalculationVersion("v1")
	if err != nil {
		t.Fatalf("версия правила: %v", err)
	}
	fee, err := NewSuccessFee(NewSuccessFeeParams{
		ID:             "fee-1",
		TenantID:       "tenant-1",
		SubscriptionID: "sub-1",
		ResultRef:      "result-1",
		DealID:         "deal-1",
		Base:           base,
		RateBps:        500,
		Calculation:    calculation,
		Confirmed:      confirmed,
		At:             testNow,
		EventID:        "evt-1",
	})
	if err != nil {
		t.Fatalf("NewSuccessFee: %v", err)
	}
	return fee
}

func TestSuccessFeeCalculatedFromConfirmedResult(t *testing.T) {
	fee := newTestSuccessFee(t, true)
	if fee.Amount().AmountMinor() != 10000 {
		t.Fatalf("вознаграждение = %d, ожидалось 10000", fee.Amount().AmountMinor())
	}
	if fee.Status() != FeeCalculated {
		t.Fatalf("статус = %s", fee.Status())
	}
	events := fee.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventSuccessFeeCalculated {
		t.Fatalf("событие расчёта = %+v", events)
	}
}

func TestSuccessFeeRequiresConfirmedResult(t *testing.T) {
	base, _ := NewMoney(200000, "rub")
	_, err := NewSuccessFee(NewSuccessFeeParams{
		ID:             "fee-1",
		TenantID:       "tenant-1",
		SubscriptionID: "sub-1",
		ResultRef:      "result-1",
		DealID:         "deal-1",
		Base:           base,
		RateBps:        500,
		Calculation:    "v1",
		Confirmed:      false,
		At:             testNow,
		EventID:        "evt-1",
	})
	if !errors.Is(err, ErrUnconfirmedResult) {
		t.Fatalf("неподтверждённый результат: ошибка = %v", err)
	}

	_, err = NewSuccessFee(NewSuccessFeeParams{
		ID:             "fee-1",
		TenantID:       "tenant-1",
		SubscriptionID: "sub-1",
		ResultRef:      "result-1",
		DealID:         "deal-1",
		Base:           base,
		RateBps:        500,
		Calculation:    "",
		Confirmed:      true,
		At:             testNow,
		EventID:        "evt-1",
	})
	if !errors.Is(err, ErrEmptyCalculationVersion) {
		t.Fatalf("пустая версия правила: ошибка = %v", err)
	}
}

func TestSuccessFeeDisputeAndResolve(t *testing.T) {
	fee := newTestSuccessFee(t, true)
	if err := fee.Dispute(testNow); err != nil {
		t.Fatalf("Dispute: %v", err)
	}
	if fee.Status() != FeeDisputed {
		t.Fatalf("статус = %s", fee.Status())
	}
	if err := fee.Dispute(testNow); !errors.Is(err, ErrInvalidFeeTransition) {
		t.Fatalf("повторный спор: ошибка = %v", err)
	}
	if err := fee.Resolve(testNow); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if fee.Status() != FeeCalculated {
		t.Fatalf("статус после снятия спора = %s", fee.Status())
	}
}

func TestSuccessFeeSnapshotRoundTrip(t *testing.T) {
	fee := newTestSuccessFee(t, true)
	if err := fee.Dispute(testNow); err != nil {
		t.Fatalf("Dispute: %v", err)
	}
	snapshot := fee.Snapshot()
	restored, err := RehydrateSuccessFee(snapshot)
	if err != nil {
		t.Fatalf("RehydrateSuccessFee: %v", err)
	}
	if restored.Status() != FeeDisputed || restored.Amount().AmountMinor() != fee.Amount().AmountMinor() {
		t.Fatalf("восстановление не совпало: %+v", restored)
	}

	tampered := snapshot
	tampered.Amount, _ = NewMoney(1, "rub")
	if _, err := RehydrateSuccessFee(tampered); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("рассогласованная сумма: ошибка = %v", err)
	}
}

func TestSuccessFeeStatuses(t *testing.T) {
	if len(FeeStatuses()) != 3 {
		t.Fatalf("статусов = %d", len(FeeStatuses()))
	}
	if FeeStatus("unknown").isValid() {
		t.Fatal("неизвестный статус не должен быть валиден")
	}
}
