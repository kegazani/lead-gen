package formsubmission

import (
	"context"
	"errors"
	"testing"
	"time"
)

// sinkStub — минимальная реализация контрактного интерфейса.
type sinkStub struct{}

func (sinkStub) Submit(_ context.Context, submission Submission) error {
	if submission.Form == "unknown" {
		return ErrNotFound
	}
	return nil
}

func TestContractIdentity(t *testing.T) {
	if ContractName != "form-submission" {
		t.Errorf("ContractName: получено %q", ContractName)
	}
	if SchemaVersion == "" {
		t.Error("пустая SchemaVersion")
	}
}

func TestSinkContract(t *testing.T) {
	var sink Sink = sinkStub{}
	submission := Submission{
		Form:       "demo",
		Name:       "Иван",
		Contact:    "ivan@example.com",
		Message:    "Здравствуйте",
		Consent:    true,
		ReceivedAt: time.Now().UTC(),
	}
	if err := sink.Submit(context.Background(), submission); err != nil {
		t.Fatalf("Submit: неожиданная ошибка: %v", err)
	}
	if err := sink.Submit(context.Background(), Submission{Form: "unknown"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}
