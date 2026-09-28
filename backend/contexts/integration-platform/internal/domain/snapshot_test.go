package domain

import (
	"errors"
	"testing"
	"time"
)

func invocationSnapshot(state InvocationState) IntegrationInvocationSnapshot {
	return IntegrationInvocationSnapshot{
		ID:              "invocation-1",
		TenantID:        "tenant-1",
		CorrelationRef:  "corr-1",
		IntegrationType: "web-research",
		AdapterName:     "firecrawl",
		Operation:       "search",
		CommandKey:      "cmd-1",
		State:           state,
		StartedAt:       testAt,
	}
}

// Снимок завершённого вызова переживает round-trip через Rehydrate.
func TestRehydrateInvocation_RoundTrip(t *testing.T) {
	invocation := beginInvocation(t, nil)
	ref := ExternalRef{IntegrationType: "web-research", AdapterName: "firecrawl", ExternalID: "ext-1"}
	if err := invocation.Complete(&ref, testAt.Add(time.Second)); err != nil {
		t.Fatalf("завершение: %v", err)
	}
	restored, err := RehydrateIntegrationInvocation(invocation.Snapshot())
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if restored.State() != InvocationSucceeded {
		t.Fatalf("состояние: %q", restored.State())
	}
	got, ok := restored.ExternalRef()
	if !ok || got.ExternalID != "ext-1" {
		t.Fatalf("привязка: %+v", got)
	}
	if restored.IdempotencyKey() != invocation.IdempotencyKey() {
		t.Fatalf("ключ: %q", restored.IdempotencyKey())
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление не эмитит событий")
	}
}

// Противоречивые снимки отвергаются доменной ошибкой.
func TestRehydrateInvocation_RejectsContradictorySnapshots(t *testing.T) {
	failedWithoutCategory := invocationSnapshot(InvocationFailed)
	failedWithoutCategory.FinishedAt = testAt

	succeededWithCategory := invocationSnapshot(InvocationSucceeded)
	succeededWithCategory.FinishedAt = testAt
	succeededWithCategory.Category = ErrorConflict
	succeededWithCategory.ExternalRef = &ExternalRef{IntegrationType: "web-research", AdapterName: "firecrawl", ExternalID: "ext-1"}

	foreignRef := invocationSnapshot(InvocationSucceeded)
	foreignRef.FinishedAt = testAt
	foreignRef.ExternalRef = &ExternalRef{IntegrationType: "web-research", AdapterName: "other", ExternalID: "ext-1"}

	pendingWithOutcome := invocationSnapshot(InvocationPending)
	pendingWithOutcome.Category = ErrorConflict

	unknownState := invocationSnapshot(InvocationState("nope"))

	cases := map[string]IntegrationInvocationSnapshot{
		"ошибка без категории":     failedWithoutCategory,
		"успех с категорией":       succeededWithCategory,
		"привязка чужого адаптера": foreignRef,
		"начатый с исходом":        pendingWithOutcome,
		"неизвестное состояние":    unknownState,
	}
	for name, snapshot := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := RehydrateIntegrationInvocation(snapshot); !errors.Is(err, ErrInvalidSnapshot) {
				t.Fatalf("получено %v, ожидалась ErrInvalidSnapshot", err)
			}
		})
	}
}

// Успех без привязки внешнего идентификатора допустим: поставщик вправе не
// возвращать собственный id.
func TestRehydrateInvocation_SuccessWithoutExternalRef(t *testing.T) {
	snapshot := invocationSnapshot(InvocationSucceeded)
	snapshot.FinishedAt = testAt
	restored, err := RehydrateIntegrationInvocation(snapshot)
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if _, ok := restored.ExternalRef(); ok {
		t.Fatal("привязки быть не должно")
	}
	if restored.State() != InvocationSucceeded {
		t.Fatalf("состояние: %q", restored.State())
	}
}

// Восстановленные состояния не путаются: тайм-аут требует временной категории.
func TestRehydrateInvocation_Timeout(t *testing.T) {
	timedOut := invocationSnapshot(InvocationTimedOut)
	timedOut.FinishedAt = testAt
	timedOut.Category = ErrorTemporarilyUnavailable
	restored, err := RehydrateIntegrationInvocation(timedOut)
	if err != nil {
		t.Fatalf("восстановление тайм-аута: %v", err)
	}
	if restored.State() != InvocationTimedOut {
		t.Fatalf("состояние: %q", restored.State())
	}

	bad := invocationSnapshot(InvocationTimedOut)
	bad.FinishedAt = testAt
	bad.Category = ErrorPermanentFailure
	if _, err := RehydrateIntegrationInvocation(bad); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("тайм-аут с постоянной категорией: %v", err)
	}
}
