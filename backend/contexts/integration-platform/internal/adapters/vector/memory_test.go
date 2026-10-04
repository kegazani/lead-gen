package vector

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/integration-platform/contracttest"
	contract "lead-gen/contracts/integration-platform/vector"
)

// failingEmbedder — эмбеддер, всегда отвечающий сбоем: проверяет нормализацию.
type failingEmbedder struct{ err error }

// EmbedText реализует Embedder.
func (f failingEmbedder) EmbedText(context.Context, string) ([]float32, error) { return nil, f.err }

// TestUpsertAndSearchOrdersBySimilarity проверяет запись и поиск по близости:
// ближайший по составу слов документ идёт первым.
func TestUpsertAndSearchOrdersBySimilarity(t *testing.T) {
	ctx := context.Background()
	store := NewMemory(HashEmbedder{})

	for _, doc := range []contract.Document{
		{ID: "it-outsourcing", Text: "разработка программного обеспечения аутсорсинг команда"},
		{ID: "coffee-shop", Text: "кофейня обжарка кофе зерно"},
		{ID: "software-product", Text: "разработка программного обеспечения продукт"},
	} {
		if _, err := store.Upsert(ctx, doc); err != nil {
			t.Fatalf("Upsert %s: %v", doc.ID, err)
		}
	}

	matches, err := store.Search(ctx, "разработка программного обеспечения", 3)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 3 {
		t.Fatalf("найдено %d, ожидалось 3", len(matches))
	}
	if matches[0].ID == "coffee-shop" {
		t.Fatalf("кофейня оказалась ближайшей: %+v", matches)
	}
	for i := 1; i < len(matches); i++ {
		if matches[i-1].Score < matches[i].Score {
			t.Fatalf("список не упорядочен по убыванию: %+v", matches)
		}
	}
}

// TestSearchRespectsLimit проверяет ограничение числа кандидатов.
func TestSearchRespectsLimit(t *testing.T) {
	ctx := context.Background()
	store := NewMemory(HashEmbedder{})
	for _, id := range []string{"a", "b", "c"} {
		if _, err := store.Upsert(ctx, contract.Document{ID: id, Text: "разработка"}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	matches, err := store.Search(ctx, "разработка", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("найдено %d, ожидалось 2", len(matches))
	}
}

// TestEmptyTextRejected проверяет доменный отказ на пустом тексте.
func TestEmptyTextRejected(t *testing.T) {
	store := NewMemory(HashEmbedder{})
	if _, err := store.Embed(context.Background(), "   "); !errors.Is(err, contract.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestEmbedderFailureNormalized проверяет нормализацию ошибок эмбеддера до
// ошибки контракта.
func TestEmbedderFailureNormalized(t *testing.T) {
	store := NewMemory(failingEmbedder{err: errors.New("503 от сервиса")})
	if _, err := store.Embed(context.Background(), "текст"); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := store.Upsert(context.Background(), contract.Document{ID: "a", Text: "текст"}); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка Upsert = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := store.Search(context.Background(), "текст", 1); !errors.Is(err, contract.ErrUnavailable) {
		t.Fatalf("ошибка Search = %v, ожидалась ErrUnavailable", err)
	}
}

// TestEmptyDocumentIDRejected проверяет отказ индексации без идентификатора.
func TestEmptyDocumentIDRejected(t *testing.T) {
	store := NewMemory(HashEmbedder{})
	if _, err := store.Upsert(context.Background(), contract.Document{Text: "текст"}); !errors.Is(err, contract.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestVectorPortContract прогоняет общий contract suite порта vector против
// in-memory реализации с детерминированным эмбеддером. Тот же suite проходит и
// против PostgreSQL-адаптера (internal/adapters/vector/postgres).
func TestVectorPortContract(t *testing.T) {
	contracttest.RunVectorPortContract(t, func(t *testing.T) contract.Port {
		t.Helper()
		return NewMemory(HashEmbedder{})
	})
}
