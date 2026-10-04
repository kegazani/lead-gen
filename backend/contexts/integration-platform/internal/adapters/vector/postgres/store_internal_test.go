package postgres

import (
	"context"
	"errors"
	"testing"

	contract "lead-gen/contracts/integration-platform/vector"
)

// failingEmbedder — эмбеддер, всегда отвечающий сбоем: проверяет нормализацию
// отказа транспорта до contract.ErrUnavailable.
type failingEmbedder struct{ err error }

// EmbedText реализует vector.Embedder.
func (f failingEmbedder) EmbedText(context.Context, string) ([]float32, error) { return nil, f.err }

// TestEmbedNormalizesEmbedderFailure проверяет, что сбой эмбеддера становится
// ErrUnavailable, а не доменным отказом.
func TestEmbedNormalizesEmbedderFailure(t *testing.T) {
	store := &Store{embedder: failingEmbedder{err: errors.New("503 от провайдера")}}
	if _, err := store.Embed(context.Background(), "текст"); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := store.Upsert(context.Background(), contract.Document{ID: "doc-1", Text: "текст"}); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка Upsert = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := store.Search(context.Background(), "текст", 1); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка Search = %v, ожидалась ErrUnavailable", err)
	}
}

// TestEmptyTextRejected проверяет доменный отказ на пустом тексте.
func TestEmptyTextRejected(t *testing.T) {
	store := &Store{embedder: failingEmbedder{err: errors.New("не вызывается")}}
	if _, err := store.Embed(context.Background(), "   "); !errors.Is(err, contract.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestEmptyDocumentIDRejected проверяет отказ индексации без идентификатора.
func TestEmptyDocumentIDRejected(t *testing.T) {
	store := &Store{embedder: failingEmbedder{err: errors.New("не вызывается")}}
	if _, err := store.Upsert(context.Background(), contract.Document{Text: "текст"}); !errors.Is(err, contract.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestCancelledContextPropagates проверяет, что отмена контекста возвращается как
// context.Canceled, а не маскируется под недоступность хранилища.
func TestCancelledContextPropagates(t *testing.T) {
	store := &Store{embedder: failingEmbedder{err: errors.New("не вызывается")}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Embed(ctx, "текст"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ожидалась context.Canceled", err)
	}
}
