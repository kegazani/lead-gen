package feedback

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/contracts/lead-management/recommendationapply"
)

// fakePort — тестовый двойник контракта recommendation-apply (только в тестах).
type fakePort struct {
	calls   []recommendationapply.Application
	receipt recommendationapply.Receipt
	err     error
}

func (f *fakePort) ApplyRecommendation(_ context.Context, application recommendationapply.Application) (recommendationapply.Receipt, error) {
	f.calls = append(f.calls, application)
	if f.err != nil {
		return recommendationapply.Receipt{}, f.err
	}
	return f.receipt, nil
}

func recommendationChange() ports.RecommendationChange {
	return ports.RecommendationChange{
		TenantID: "t1", ScopeKind: "industry", ScopeKey: "saas", Reason: "no_signal", Basis: "наблюдений 3",
		Change: ports.SearchChange{Kind: "new-search", Criteria: "saas-агентства", Targets: []string{"разработка на заказ"}, Limit: 20},
	}
}

// TestApplyRecommendationMapsChange проверяет перевод структурного изменения
// внутреннего порта в контракт и ссылки на задание обратно.
func TestApplyRecommendationMapsChange(t *testing.T) {
	port := &fakePort{receipt: recommendationapply.Receipt{JobID: "reco-42"}}
	adapter := New(port)

	receipt, err := adapter.ApplyRecommendation(context.Background(), recommendationChange())
	if err != nil {
		t.Fatalf("ApplyRecommendation: %v", err)
	}
	if receipt.Reference != "reco-42" {
		t.Fatalf("ссылка = %q", receipt.Reference)
	}
	if len(port.calls) != 1 {
		t.Fatalf("вызовов контракта %d, ожидался 1", len(port.calls))
	}
	call := port.calls[0]
	if call.TenantID != "t1" || call.ScopeKind != "industry" || call.ScopeKey != "saas" {
		t.Fatalf("запрос контракта = %+v", call)
	}
	if call.Change.Kind != recommendationapply.KindNewSearch || call.Change.Criteria != "saas-агентства" || call.Change.Limit != 20 {
		t.Fatalf("структурное изменение = %+v", call.Change)
	}
	if call.ChangeID == "" {
		t.Fatal("ключ идемпотентности не выведен")
	}
}

// TestChangeIDStable проверяет, что ключ идемпотентности детерминирован для той
// же рекомендации и различается для разных изменений.
func TestChangeIDStable(t *testing.T) {
	port := &fakePort{}
	adapter := New(port)

	if _, err := adapter.ApplyRecommendation(context.Background(), recommendationChange()); err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	if _, err := adapter.ApplyRecommendation(context.Background(), recommendationChange()); err != nil {
		t.Fatalf("повтор применения: %v", err)
	}
	if port.calls[0].ChangeID != port.calls[1].ChangeID {
		t.Fatalf("идентичный запрос дал разные ключи: %q и %q", port.calls[0].ChangeID, port.calls[1].ChangeID)
	}

	other := recommendationChange()
	other.Change.Criteria = "другая ниша"
	if _, err := adapter.ApplyRecommendation(context.Background(), other); err != nil {
		t.Fatalf("третье применение: %v", err)
	}
	if port.calls[0].ChangeID == port.calls[2].ChangeID {
		t.Fatal("разные изменения дали один ключ идемпотентности")
	}
}

// TestApplyRecommendationPropagatesError проверяет, что сбой владельца не
// выдаётся за успешное применение.
func TestApplyRecommendationPropagatesError(t *testing.T) {
	port := &fakePort{err: recommendationapply.ErrUnavailable}
	adapter := New(port)

	if _, err := adapter.ApplyRecommendation(context.Background(), recommendationChange()); !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestApplyRecommendationWithoutPort проверяет честную недоступность без
// подключённого контракта.
func TestApplyRecommendationWithoutPort(t *testing.T) {
	adapter := New(nil)
	if _, err := adapter.ApplyRecommendation(context.Background(), recommendationChange()); !errors.Is(err, recommendationapply.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
