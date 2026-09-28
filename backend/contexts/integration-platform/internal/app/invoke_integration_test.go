package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"
)

// fixedClock — детерминированные часы прикладного слоя.
type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

type invokeFixture struct {
	registrations *registrationRepo
	invocations   *invocationRepo
	provider      *providerStub
	handler       *InvokeIntegrationHandler
}

// newInvokeFixture собирает сценарий с зарегистрированным адаптером firecrawl,
// объявляющим возможности search и crawl.
func newInvokeFixture(t *testing.T) *invokeFixture {
	t.Helper()
	registrations := newRegistrationRepo()
	invocations := newInvocationRepo()
	provider := newProviderStub()

	register := NewRegisterAdapterHandler(registrations)
	if _, err := register.Handle(context.Background(), registerCommand(
		"adapter-1", "firecrawl", domain.CapabilitySearch, domain.CapabilityCrawl,
	)); err != nil {
		t.Fatalf("регистрация адаптера: %v", err)
	}

	return &invokeFixture{
		registrations: registrations,
		invocations:   invocations,
		provider:      provider,
		handler:       NewInvokeIntegrationHandler(registrations, invocations, provider, fixedClock{at: testTime}),
	}
}

func invokeCommand(invocationID, commandKey string) InvokeIntegrationCommand {
	return InvokeIntegrationCommand{
		InvocationID:    domain.InvocationID(invocationID),
		EventID:         "evt-" + invocationID,
		OccurredAt:      testTime,
		TenantID:        "tenant-1",
		CorrelationRef:  "corr-1",
		IntegrationType: "web-research",
		Operation:       "search",
		CommandKey:      domain.CommandKey(commandKey),
		Payload:         json.RawMessage(`{"query":"логисты"}`),
	}
}

// Успешный вызов сохраняет результат и привязку внешнего идентификатора.
func TestInvokeIntegration_Success(t *testing.T) {
	fixture := newInvokeFixture(t)
	fixture.provider.setResult(providerport.Result{
		Operation:  "search",
		ExternalID: "ext-9",
		Payload:    json.RawMessage(`{"id":"ext-9"}`),
	})

	result, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1"))
	if err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if result.State != domain.InvocationSucceeded {
		t.Fatalf("состояние: %q", result.State)
	}
	if result.ExternalRef == nil || result.ExternalRef.ExternalID != "ext-9" {
		t.Fatalf("привязка: %+v", result.ExternalRef)
	}

	loaded, err := fixture.invocations.FindByID(context.Background(), result.InvocationID)
	if err != nil {
		t.Fatalf("запрос результата по InvocationID: %v", err)
	}
	if loaded.State() != domain.InvocationSucceeded {
		t.Fatalf("сохранённое состояние: %q", loaded.State())
	}
}

// Каждая категория ошибки нормализуется и фиксируется в вызове.
func TestInvokeIntegration_EachCategory(t *testing.T) {
	for _, category := range domain.AllErrorCategories() {
		t.Run(string(category), func(t *testing.T) {
			fixture := newInvokeFixture(t)
			fixture.provider.setError(integrationerror.Error{
				Category:     integrationerror.Category(category),
				AdapterName:  "firecrawl",
				ContractName: providerport.ContractName,
				Reason:       "причина отказа",
			})

			result, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1"))
			if err == nil {
				t.Fatal("ожидалась ошибка")
			}
			if result.State != domain.InvocationFailed {
				t.Fatalf("состояние: %q", result.State)
			}
			if result.Category != category {
				t.Fatalf("категория: got %q, want %q", result.Category, category)
			}
			loaded, loadErr := fixture.invocations.FindByID(context.Background(), result.InvocationID)
			if loadErr != nil {
				t.Fatalf("загрузка: %v", loadErr)
			}
			if loaded.State() != domain.InvocationFailed || loaded.Category() != category {
				t.Fatalf("сохранённый вызов: %q/%q", loaded.State(), loaded.Category())
			}
		})
	}
}

// Тайм-аут и отмена различаются в состоянии вызова.
func TestInvokeIntegration_TimeoutAndCancel(t *testing.T) {
	t.Run("тайм-аут", func(t *testing.T) {
		fixture := newInvokeFixture(t)
		fixture.provider.setError(context.DeadlineExceeded)
		result, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ошибка: %v", err)
		}
		if result.State != domain.InvocationTimedOut {
			t.Fatalf("состояние: %q", result.State)
		}
		if result.Category != domain.ErrorTemporarilyUnavailable {
			t.Fatalf("категория тайм-аута: %q", result.Category)
		}
	})

	t.Run("отмена", func(t *testing.T) {
		fixture := newInvokeFixture(t)
		fixture.provider.setError(context.Canceled)
		result, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ошибка: %v", err)
		}
		if result.State != domain.InvocationCancelled {
			t.Fatalf("состояние: %q", result.State)
		}
	})
}

// Повтор ключа идемпотентности не обращается к поставщику повторно, а другой
// ключ создаёт новый вызов.
func TestInvokeIntegration_Idempotency(t *testing.T) {
	fixture := newInvokeFixture(t)
	fixture.provider.setResult(providerport.Result{Operation: "search", ExternalID: "ext-1"})

	first, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1"))
	if err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	// Тот же ключ (tenant, тип, операция, ключ команды), но другой идентификатор
	// вызова: повтор возвращает прежний результат без обращения к поставщику.
	replay, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-2", "cmd-1"))
	if err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if !replay.Replayed {
		t.Fatal("повтор должен быть помечен как воспроизведение")
	}
	if replay.InvocationID != first.InvocationID {
		t.Fatalf("повтор вернул другой вызов: %q против %q", replay.InvocationID, first.InvocationID)
	}
	if calls := fixture.provider.Calls(); calls != 1 {
		t.Fatalf("поставщик вызван %d раз вместо 1", calls)
	}

	fresh, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-3", "cmd-2"))
	if err != nil {
		t.Fatalf("новый ключ: %v", err)
	}
	if fresh.Replayed || fresh.InvocationID != "inv-3" {
		t.Fatalf("новый ключ должен создать новый вызов: %+v", fresh)
	}
	if calls := fixture.provider.Calls(); calls != 2 {
		t.Fatalf("поставщик вызван %d раз вместо 2", calls)
	}
}

// Успех после временной ошибки в том же correlation-ref эмитит
// integration-recovered; после постоянной ошибки — нет.
func TestInvokeIntegration_Recovery(t *testing.T) {
	fixture := newInvokeFixture(t)
	fixture.provider.setError(integrationerror.Error{
		Category:     integrationerror.CategoryTemporarilyUnavailable,
		AdapterName:  "firecrawl",
		ContractName: providerport.ContractName,
	})
	if _, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1")); err == nil {
		t.Fatal("ожидалась временная ошибка")
	}

	fixture.provider.setResult(providerport.Result{Operation: "search", ExternalID: "ext-1"})
	result, err := fixture.handler.Handle(context.Background(), invokeCommand("inv-2", "cmd-2"))
	if err != nil {
		t.Fatalf("восстановление: %v", err)
	}
	if !result.Recovered {
		t.Fatal("успех после временной ошибки должен дать integration-recovered")
	}

	// Постоянная ошибка восстановления не даёт.
	other := newInvokeFixture(t)
	other.provider.setError(integrationerror.Error{
		Category:     integrationerror.CategoryPermanentFailure,
		AdapterName:  "firecrawl",
		ContractName: providerport.ContractName,
	})
	if _, err := other.handler.Handle(context.Background(), invokeCommand("inv-1", "cmd-1")); err == nil {
		t.Fatal("ожидалась постоянная ошибка")
	}
	other.provider.setResult(providerport.Result{Operation: "search", ExternalID: "ext-1"})
	result, err = other.handler.Handle(context.Background(), invokeCommand("inv-2", "cmd-2"))
	if err != nil {
		t.Fatalf("вызов: %v", err)
	}
	if result.Recovered {
		t.Fatal("после постоянной ошибки восстановления нет")
	}
}

// Необъявленная обязательная возможность отклоняет вызов до обращения к
// поставщику.
func TestInvokeIntegration_RejectsUndeclaredCapability(t *testing.T) {
	fixture := newInvokeFixture(t)
	name := domain.AdapterName("firecrawl")
	command := invokeCommand("inv-1", "cmd-1")
	command.AdapterName = &name
	command.Operation = "send"

	_, err := fixture.handler.Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrCapabilityNotDeclared) {
		t.Fatalf("ожидалась ошибка о необъявленной возможности: %v", err)
	}
	if calls := fixture.provider.Calls(); calls != 0 {
		t.Fatalf("вызов не должен доходить до поставщика: %d", calls)
	}
}

// Незарегистрированный адаптер — диагностика без секретов до вызова.
func TestInvokeIntegration_RejectsUnknownAdapter(t *testing.T) {
	fixture := newInvokeFixture(t)
	name := domain.AdapterName("missing")
	command := invokeCommand("inv-1", "cmd-1")
	command.AdapterName = &name

	_, err := fixture.handler.Handle(context.Background(), command)
	if !errors.Is(err, domain.ErrAdapterNotFound) {
		t.Fatalf("ожидалась ошибка о неизвестном адаптере: %v", err)
	}
}
