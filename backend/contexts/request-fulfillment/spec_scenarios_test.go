package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestSpecScenarios покрывает каждый сценарий спецификации capability
// client-request. Имена подтестов повторяют сценарии spec.md.
func TestSpecScenarios(t *testing.T) {
	ctx := context.Background()

	slice := func(t *testing.T) *requestfulfillment.Slice {
		t.Helper()
		built, err := requestfulfillment.Build(requestfulfillment.Config{})
		if err != nil {
			t.Fatalf("сборка среза: %v", err)
		}
		return built
	}

	submit := func(t *testing.T, s *requestfulfillment.Slice, content string) {
		t.Helper()
		if _, err := s.SubmitRequest.Handle(ctx, command.SubmitRequest{
			RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
			Content: content, Target: "IT-компании", Actor: "actor-1",
		}); err != nil {
			t.Fatalf("SubmitRequest: %v", err)
		}
	}

	fields := func(tenant string) command.LifecycleFields {
		return command.LifecycleFields{RequestID: "req-1", TenantID: domain.TenantID(tenant), Actor: "actor-1"}
	}

	t.Run("Создание заявки", func(t *testing.T) {
		s := slice(t)
		result, err := s.SubmitRequest.Handle(ctx, command.SubmitRequest{
			RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
			Content: "Оффер", Actor: "actor-1",
		})
		if err != nil {
			t.Fatalf("SubmitRequest: %v", err)
		}
		if result.Status != domain.StatusSubmitted {
			t.Fatalf("статус = %q, ожидался submitted", result.Status)
		}
	})

	t.Run("Недопустимый статус", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		snapshot := request.Snapshot()
		snapshot.Status = domain.RequestStatus("bogus")
		if _, err := domain.RehydrateRequest(snapshot); !errors.Is(err, domain.ErrUnknownRequestStatus) {
			t.Fatalf("ошибка = %v, ожидалась ErrUnknownRequestStatus", err)
		}
	})

	t.Run("Пустой оффер", func(t *testing.T) {
		s := slice(t)
		_, err := s.SubmitRequest.Handle(ctx, command.SubmitRequest{
			RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
			Content: "   ", Actor: "actor-1",
		})
		if !errors.Is(err, domain.ErrOfferRequired) {
			t.Fatalf("ошибка = %v, ожидалась ErrOfferRequired", err)
		}
	})

	t.Run("Перенос между тенантами", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-2")}); !errors.Is(err, command.ErrTenantMismatch) {
			t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
		}
	})

	t.Run("Запуск без валидации", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: fields("tenant-1")}); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
		}
	})

	t.Run("Правка оффера после валидации", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
		result, err := s.ReviseOffer.Handle(ctx, command.ReviseOffer{
			RequestID: "req-1", TenantID: "tenant-1", OfferID: "offer-2",
			Content: "Новый оффер", Actor: "actor-1",
		})
		if err != nil {
			t.Fatalf("ReviseOffer: %v", err)
		}
		if result.OfferRevision != 2 {
			t.Fatalf("ревизия = %d, ожидалась 2", result.OfferRevision)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		history := request.OfferHistory()
		if len(history) != 1 || history[0].Content().Text() != "Оффер" {
			t.Fatalf("прежняя версия оффера изменена: %+v", history)
		}
	})

	t.Run("Фиксация аудитории", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
		if _, err := s.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		if _, err := s.RecordAudienceSnapshot.Handle(ctx, command.RecordAudienceSnapshot{
			RequestID: "req-1", TenantID: "tenant-1", AudienceSnapshotID: "snap-1",
		}); err != nil {
			t.Fatalf("RecordAudienceSnapshot: %v", err)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if request.AudienceSnapshotID() != "snap-1" {
			t.Fatalf("ссылка = %q, ожидалась snap-1", request.AudienceSnapshotID())
		}
	})

	t.Run("Аудит перехода", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		request, err := s.Repository.FindByID(ctx, "req-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		transitions := request.Transitions()
		last := transitions[len(transitions)-1]
		if last.Actor() != "actor-1" || last.At().IsZero() {
			t.Fatalf("переход без времени или актора: %+v", last)
		}
	})

	t.Run("Выход из терминального состояния", func(t *testing.T) {
		s := slice(t)
		submit(t, s, "Оффер")
		if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("ValidateRequest: %v", err)
		}
		if _, err := s.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		if _, err := s.FulfillRequest.Handle(ctx, command.FulfillRequest{LifecycleFields: fields("tenant-1")}); err != nil {
			t.Fatalf("FulfillRequest: %v", err)
		}
		if _, err := s.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: fields("tenant-1")}); !errors.Is(err, domain.ErrTerminalState) {
			t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
		}
	})
}
