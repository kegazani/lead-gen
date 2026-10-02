package requestfulfillment_test

import (
	"context"
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/integration-platform/vector"
)

// fakeVector — фейковый порт векторного хранилища для сценариев эмбеддинга оффера.
type fakeVector struct{ err error }

// Embed реализует vector.Port.
func (f fakeVector) Embed(_ context.Context, text string) (vector.Vector, error) {
	if f.err != nil {
		return nil, f.err
	}
	return vector.Vector{float32(len(text)), 1}, nil
}

// Upsert реализует vector.Port.
func (f fakeVector) Upsert(_ context.Context, document vector.Document) (vector.Vector, error) {
	return f.Embed(context.Background(), document.Text)
}

// Search реализует vector.Port.
func (f fakeVector) Search(context.Context, string, int) ([]vector.Match, error) { return nil, nil }

// TestSpecScenarioOfferEmbedding — сценарий «Эмбеддинг оффера»: после валидации
// оффер получает векторное представление.
func TestSpecScenarioOfferEmbedding(t *testing.T) {
	ctx := context.Background()
	built, err := requestfulfillment.Build(requestfulfillment.Config{Vector: fakeVector{}})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	submitAndValidate(t, built)

	result, err := built.EmbedOffer.Handle(ctx, command.EmbedOffer{RequestID: "req-1", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("EmbedOffer: %v", err)
	}
	if result.Dimensions == 0 {
		t.Fatal("эмбеддинг пуст")
	}

	request, err := built.Repository.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if len(request.Offer().Embedding()) == 0 {
		t.Fatal("заявка не сохранила вектор оффера")
	}
}

// TestOfferEmbeddingClearedAfterRevision проверяет, что правка оффера сбрасывает
// устаревший вектор.
func TestOfferEmbeddingClearedAfterRevision(t *testing.T) {
	ctx := context.Background()
	built, err := requestfulfillment.Build(requestfulfillment.Config{Vector: fakeVector{}})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	submitAndValidate(t, built)
	if _, err := built.EmbedOffer.Handle(ctx, command.EmbedOffer{RequestID: "req-1", TenantID: "tenant-1"}); err != nil {
		t.Fatalf("EmbedOffer: %v", err)
	}
	if _, err := built.ReviseOffer.Handle(ctx, command.ReviseOffer{
		RequestID: "req-1", TenantID: "tenant-1", OfferID: "offer-2",
		Content: "Новый оффер", Actor: "actor-1",
	}); err != nil {
		t.Fatalf("ReviseOffer: %v", err)
	}
	request, err := built.Repository.FindByID(ctx, "req-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if len(request.Offer().Embedding()) != 0 {
		t.Fatal("вектор не сброшен после правки оффера")
	}
}

// TestOfferEmbeddingUnavailable — без векторного хранилища эмбеддинг не выдаётся за
// успех, а заявка не меняется.
func TestOfferEmbeddingUnavailable(t *testing.T) {
	ctx := context.Background()
	built, err := requestfulfillment.Build(requestfulfillment.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	submitAndValidate(t, built)

	handler := command.NewEmbedOfferHandler(built.UnitOfWork, nil)
	if _, err := handler.Handle(ctx, command.EmbedOffer{RequestID: "req-1", TenantID: "tenant-1"}); !errors.Is(err, ports.ErrVectorUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVectorUnavailable", err)
	}
}

// submitAndValidate подаёт заявку и доводит её до статуса validated.
func submitAndValidate(t *testing.T, s *requestfulfillment.Slice) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.SubmitRequest.Handle(ctx, command.SubmitRequest{
		RequestID: "req-1", OfferID: "offer-1", TenantID: "tenant-1",
		Content: "Предлагаем автоматизацию найма", Actor: "client",
	}); err != nil {
		t.Fatalf("SubmitRequest: %v", err)
	}
	fields := command.LifecycleFields{RequestID: "req-1", TenantID: domain.TenantID("tenant-1"), Actor: "admin"}
	if _, err := s.StartValidation.Handle(ctx, command.StartValidation{LifecycleFields: fields}); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := s.ValidateRequest.Handle(ctx, command.ValidateRequest{LifecycleFields: fields}); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
}
