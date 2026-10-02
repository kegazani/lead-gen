package billingresult

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var testInstant = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func validCharge() Result {
	return Result{
		Envelope: Envelope{
			EventID:       "evt-1",
			OccurredAt:    testInstant,
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          ContractName,
			SchemaVersion: SchemaVersion,
		},
		ResultID:       "res-1",
		Kind:           KindCharge,
		SubscriptionID: "sub-1",
		SourceRef:      "invoice-1",
		AmountMinor:    500000,
		Currency:       "RUB",
	}
}

func TestResultValidateCharge(t *testing.T) {
	if err := validCharge().Validate(); err != nil {
		t.Fatalf("валидное начисление отвергнуто: %v", err)
	}
}

func TestResultValidateSuccessFeeRequiresCalculationVersion(t *testing.T) {
	fee := validCharge()
	fee.Kind = KindSuccessFee
	fee.SourceRef = "result-1"
	err := fee.Validate()
	if err == nil || !strings.Contains(err.Error(), "calculation_version") {
		t.Fatalf("вознаграждение без версии правила: ошибка = %v", err)
	}
	fee.CalculationVersion = "v1"
	if err := fee.Validate(); err != nil {
		t.Fatalf("вознаграждение с версией правила: %v", err)
	}
	if !fee.IsSuccessFee() {
		t.Fatal("результат должен опознаваться как вознаграждение")
	}
}

func TestKindIsValid(t *testing.T) {
	for _, kind := range []Kind{KindCharge, KindSuccessFee} {
		if !kind.IsValid() {
			t.Errorf("%q должен быть валиден", kind)
		}
	}
	if Kind("unknown").IsValid() {
		t.Fatal("неизвестный вид результата не должен быть валиден")
	}
}

// recordingSubscriber запоминает полученные результаты.
type recordingSubscriber struct {
	received []Result
}

func (s *recordingSubscriber) Receive(_ context.Context, result Result) error {
	s.received = append(s.received, result)
	return nil
}

func TestInProcPublisherForwardsValidatedResult(t *testing.T) {
	subscriber := &recordingSubscriber{}
	publisher := InProc(subscriber)
	if err := publisher.Publish(context.Background(), validCharge()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(subscriber.received) != 1 {
		t.Fatalf("получатель получил %d результатов", len(subscriber.received))
	}
	invalid := validCharge()
	invalid.Currency = "R"
	if err := publisher.Publish(context.Background(), invalid); err == nil {
		t.Fatal("невалидный результат не должен публиковаться")
	}
}

func TestInProcWithoutSubscriberIsUnavailable(t *testing.T) {
	publisher := InProc(nil)
	if err := publisher.Publish(context.Background(), validCharge()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
