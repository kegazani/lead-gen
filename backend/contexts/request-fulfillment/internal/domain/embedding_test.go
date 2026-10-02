package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestOfferEmbeddingSetAndCleared проверяет, что вектор оффера фиксируется, а
// правка оффера его сбрасывает: вектор посчитан из прежнего текста.
func TestOfferEmbeddingSetAndCleared(t *testing.T) {
	request := newRequest(t)
	if err := request.SetOfferEmbedding([]float32{0.1, 0.2}); err != nil {
		t.Fatalf("SetOfferEmbedding: %v", err)
	}
	if len(request.Offer().Embedding()) != 2 {
		t.Fatalf("вектор оффера = %v", request.Offer().Embedding())
	}
	if _, err := request.ReviseOffer(input(1), "offer-2", "Новый текст оффера", ""); err != nil {
		t.Fatalf("ReviseOffer: %v", err)
	}
	if len(request.Offer().Embedding()) != 0 {
		t.Fatal("вектор не сброшен после правки оффера")
	}
}

// TestOfferEmbeddingRejectsEmpty проверяет отказ на пустом векторе.
func TestOfferEmbeddingRejectsEmpty(t *testing.T) {
	request := newRequest(t)
	if err := request.SetOfferEmbedding(nil); !errors.Is(err, domain.ErrEmptyEmbedding) {
		t.Fatalf("ошибка = %v, ожидалась ErrEmptyEmbedding", err)
	}
}

// TestOfferEmbeddingRoundTrip проверяет, что вектор оффера переживает сохранение и
// восстановление.
func TestOfferEmbeddingRoundTrip(t *testing.T) {
	request := newRequest(t)
	if err := request.SetOfferEmbedding([]float32{0.5}); err != nil {
		t.Fatalf("SetOfferEmbedding: %v", err)
	}
	restored, err := domain.RehydrateRequest(request.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateRequest: %v", err)
	}
	if len(restored.Offer().Embedding()) != 1 {
		t.Fatalf("вектор потерян при восстановлении: %v", restored.Offer().Embedding())
	}
}
