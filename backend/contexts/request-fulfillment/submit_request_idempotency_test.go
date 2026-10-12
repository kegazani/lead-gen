package requestfulfillment_test

import (
	"context"
	"testing"
	"time"

	outboxmemory "lead-gen/backend/contexts/request-fulfillment/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

type submitTestClock struct{ at time.Time }

func (c submitTestClock) Now() time.Time { return c.at }

type submitTestIDs struct{ calls int }

func (g *submitTestIDs) NewEventID() domain.EventID {
	g.calls++
	return domain.EventID("event-" + string(rune('0'+g.calls)))
}

func TestSubmitRequestSameIntentRetryIsIdempotentAndPurposeMismatchConflicts(t *testing.T) {
	repo := persistence.New()
	uow := outboxmemory.NewUnitOfWorkWith(repo, outboxmemory.NewTransactionalJournal())
	ids := &submitTestIDs{}
	handler := command.NewSubmitRequestHandler(uow, submitTestClock{at: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}, ids)
	sender := &domain.SenderCompanySnapshot{CompanyName: "ООО Отправитель", Website: "https://sender.example", Description: "CRM", Services: []domain.SenderServiceSnapshot{{Name: "Внедрение", Short: "Кратко", Full: "Полностью"}}}
	input := command.SubmitRequest{RequestID: "req-idempotent", OfferID: "offer-idempotent", TenantID: "tenant-1", Title: "Идемпотентная заявка", Content: "Оффер", Target: "B2B", ExecutionPurpose: domain.ExecutionPurposeEditorial, SenderCompany: sender, Actor: "user-1"}
	first, err := handler.Handle(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := handler.Handle(context.Background(), input)
	if err != nil {
		t.Fatalf("повтор идентичной команды: %v", err)
	}
	if first.RequestID != second.RequestID || first.Version != second.Version || ids.calls != 1 {
		t.Fatalf("первый/повтор = %+v / %+v, event IDs = %d", first, second, ids.calls)
	}
	input.ExecutionPurpose = domain.ExecutionPurposeDelivery
	if _, err := handler.Handle(context.Background(), input); err != domain.ErrRequestSubmissionConflict {
		t.Fatalf("purpose mismatch = %v, ожидался ErrRequestSubmissionConflict", err)
	}
	input.ExecutionPurpose = domain.ExecutionPurposeEditorial
	input.Title = "Другое имя"
	if _, err := handler.Handle(context.Background(), input); err != domain.ErrRequestSubmissionConflict {
		t.Fatalf("title mismatch = %v, ожидался ErrRequestSubmissionConflict", err)
	}
	stored, err := repo.FindByID(context.Background(), domain.RequestID(input.RequestID))
	if err != nil {
		t.Fatal(err)
	}
	if stored.ExecutionPurpose() != domain.ExecutionPurposeEditorial || stored.SenderCompanySnapshot().CompanyName != "ООО Отправитель" {
		t.Fatalf("повтор изменил исходную заявку: purpose=%q sender=%+v", stored.ExecutionPurpose(), stored.SenderCompanySnapshot())
	}
}
