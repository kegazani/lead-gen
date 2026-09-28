package domain

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func beginInvocation(t *testing.T, prior *PriorFailure) *IntegrationInvocation {
	t.Helper()
	invocation, err := BeginInvocation(BeginInvocationParams{
		ID:              "invocation-1",
		TenantID:        "tenant-1",
		CorrelationRef:  "corr-1",
		IntegrationType: "web-research",
		AdapterName:     "firecrawl",
		Operation:       "search",
		CommandKey:      "cmd-1",
		StartedAt:       testAt,
		PriorFailure:    prior,
	})
	if err != nil {
		t.Fatalf("начало вызова: %v", err)
	}
	return invocation
}

// Успешный вызов хранит привязку внешнего идентификатора отдельно от внутреннего.
func TestInvocation_Complete(t *testing.T) {
	invocation := beginInvocation(t, nil)
	ref := ExternalRef{IntegrationType: "web-research", AdapterName: "firecrawl", ExternalID: "ext-42"}
	if err := invocation.Complete(&ref, testAt.Add(time.Second)); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	if invocation.State() != InvocationSucceeded {
		t.Fatalf("состояние: %q", invocation.State())
	}
	got, ok := invocation.ExternalRef()
	if !ok || got.ExternalID != "ext-42" {
		t.Fatalf("привязка: %+v, ok=%v", got, ok)
	}
	if invocation.Category() != "" {
		t.Fatalf("успех не несёт категории: %q", invocation.Category())
	}
	if len(invocation.DomainEvents()) != 0 {
		t.Fatal("обычный успех не эмитит событий")
	}
}

// Повторный переход завершённого вызова отвергается.
func TestInvocation_RejectsSecondTransition(t *testing.T) {
	invocation := beginInvocation(t, nil)
	ref := ExternalRef{IntegrationType: "web-research", AdapterName: "firecrawl", ExternalID: "ext-1"}
	if err := invocation.Complete(&ref, testAt); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	if err := invocation.Cancel(testAt); !errors.Is(err, ErrInvalidInvocationTransition) {
		t.Fatalf("повторный переход: %v", err)
	}
}

// Привязка чужого адаптера или типа интеграции отвергается.
func TestInvocation_CompleteRejectsForeignRef(t *testing.T) {
	invocation := beginInvocation(t, nil)
	ref := ExternalRef{IntegrationType: "web-research", AdapterName: "other", ExternalID: "ext-1"}
	if err := invocation.Complete(&ref, testAt); !errors.Is(err, ErrInvalidExternalRef) {
		t.Fatalf("чужая привязка: %v", err)
	}
}

// Нормализация отображает каждую категорию и помечает временные.
func TestNormalizeError_EachCategory(t *testing.T) {
	for _, category := range AllErrorCategories() {
		outcome, err := NormalizeError(AdapterError{Category: category, Reason: "причина"})
		if err != nil {
			t.Fatalf("категория %q: %v", category, err)
		}
		if outcome.Category != category {
			t.Fatalf("категория: got %q, want %q", outcome.Category, category)
		}
		if outcome.Diagnostic != "причина" {
			t.Fatalf("диагностика: %q", outcome.Diagnostic)
		}
		wantTemporary := category == ErrorTemporarilyUnavailable || category == ErrorRateLimited
		if outcome.Temporary != wantTemporary {
			t.Fatalf("временность %q: got %v, want %v", category, outcome.Temporary, wantTemporary)
		}
	}

	if _, err := NormalizeError(AdapterError{Category: "nope"}); !errors.Is(err, ErrUnknownErrorCategory) {
		t.Fatalf("неизвестная категория: %v", err)
	}
}

// Диагностическая причина не попадает в событие отказы.
func TestInvocation_FailDoesNotLeakReasonIntoEvent(t *testing.T) {
	invocation := beginInvocation(t, nil)
	secret := "sk-live-AAA"
	err := invocation.Fail(AdapterError{Category: ErrorAuthorizationDenied, Reason: "ключ " + secret + " отвергнут"}, testAt)
	if err != nil {
		t.Fatalf("ошибка: %v", err)
	}
	if invocation.State() != InvocationFailed || invocation.Category() != ErrorAuthorizationDenied {
		t.Fatalf("состояние/категория: %q/%q", invocation.State(), invocation.Category())
	}
	events := invocation.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("число событий: %d", len(events))
	}
	if _, ok := events[0].(IntegrationFailed); !ok {
		t.Fatalf("тип события: %T", events[0])
	}
	if rendered := fmt.Sprintf("%+v", events[0]); strings.Contains(rendered, secret) {
		t.Fatalf("диагностическая причина протекла в событие: %s", rendered)
	}
}

// Тайм-аут — нормализованная ошибка с временной категорией.
func TestInvocation_Timeout(t *testing.T) {
	invocation := beginInvocation(t, nil)
	if err := invocation.Timeout(testAt); err != nil {
		t.Fatalf("тайм-аут: %v", err)
	}
	if invocation.State() != InvocationTimedOut || invocation.Category() != ErrorTemporarilyUnavailable {
		t.Fatalf("состояние/категория: %q/%q", invocation.State(), invocation.Category())
	}
	events := invocation.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventIntegrationFailed {
		t.Fatalf("события тайм-аута: %v", events)
	}
}

// Отмена не эмитит событий.
func TestInvocation_Cancel(t *testing.T) {
	invocation := beginInvocation(t, nil)
	if err := invocation.Cancel(testAt); err != nil {
		t.Fatalf("отмена: %v", err)
	}
	if invocation.State() != InvocationCancelled {
		t.Fatalf("состояние: %q", invocation.State())
	}
	if len(invocation.DomainEvents()) != 0 {
		t.Fatal("отмена не эмитит событий")
	}
}

// Успех после временной ошибки эмитит integration-recovered; после постоянной —
// нет.
func TestInvocation_RecoversAfterTemporaryFailure(t *testing.T) {
	recovered := beginInvocation(t, &PriorFailure{Category: ErrorTemporarilyUnavailable})
	ref := ExternalRef{IntegrationType: "web-research", AdapterName: "firecrawl", ExternalID: "ext-1"}
	if err := recovered.Complete(&ref, testAt); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	events := recovered.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventIntegrationRecovered {
		t.Fatalf("ожидалось integration-recovered: %v", events)
	}

	notRecovered := beginInvocation(t, &PriorFailure{Category: ErrorPermanentFailure})
	if err := notRecovered.Complete(&ref, testAt); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	if len(notRecovered.DomainEvents()) != 0 {
		t.Fatal("после постоянной ошибки восстановления нет")
	}
}

// Начало вызова с неизвестной категорией предыдущей ошибки отвергается.
func TestBeginInvocation_RejectsUnknownPriorCategory(t *testing.T) {
	_, err := BeginInvocation(BeginInvocationParams{
		ID:              "invocation-1",
		TenantID:        "tenant-1",
		CorrelationRef:  "corr-1",
		IntegrationType: "web-research",
		AdapterName:     "firecrawl",
		Operation:       "search",
		CommandKey:      "cmd-1",
		StartedAt:       testAt,
		PriorFailure:    &PriorFailure{Category: "nope"},
	})
	if !errors.Is(err, ErrUnknownErrorCategory) {
		t.Fatalf("получено %v", err)
	}
}

// Ключ идемпотентности — (tenant_id, тип интеграции, операция, ключ команды).
func TestInvocation_IdempotencyKey(t *testing.T) {
	invocation := beginInvocation(t, nil)
	if got, want := invocation.IdempotencyKey(), "tenant-1|web-research|search|cmd-1"; got != want {
		t.Fatalf("ключ: got %q, want %q", got, want)
	}
	other := InvocationKey("tenant-1", "web-research", "search", "cmd-2")
	if other == invocation.IdempotencyKey() {
		t.Fatal("другой ключ команды даёт другой ключ идемпотентности")
	}
}
