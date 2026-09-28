// Package contracttest содержит общий contract suite портов контекста
// integration-platform. Suite запускается против любой реализации через фабрику
// и потому не зависит от адаптера: in-memory репозитории и тестовый адаптер
// провайдера проверяются одним и тем же набором, а позднее — PostgreSQL и
// сетевой адаптер без изменений.
//
// Пакет импортирует только ports, domain и опубликованные контракты; он не
// импортирует adapters.
package contracttest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"

	"lead-gen/backend/contexts/integration-platform/internal/ports"
)

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

// Outcome — вид запрограммированного исхода вызова в harness тестового адаптера.
type Outcome string

const (
	// OutcomeSuccess — успешный ответ.
	OutcomeSuccess Outcome = "success"
	// OutcomeError — нормализованная ошибка заданной категории.
	OutcomeError Outcome = "error"
	// OutcomeTimeout — прерывание по истечении бюджета.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeCancel — отмена вызывающим.
	OutcomeCancel Outcome = "cancel"
)

// Scenario — запрограммированный исход вызова. Форма нейтральна: harness
// переводит её в собственные настройки тестового адаптера.
type Scenario struct {
	Outcome    Outcome
	Category   string
	ExternalID string
	Payload    []byte
	Reason     string
	Delay      time.Duration
}

// ProviderHarness — управляющая поверхность тестового адаптера провайдера.
// Реализация обязана дать порт, позволить задать исход, посчитать фактические
// исполнения и сообщить секретные фикстуры.
type ProviderHarness interface {
	// Port — проверяемая реализация providerport.Port.
	Port() ports.ProviderPort
	// Apply задаёт исход следующих вызовов.
	Apply(scenario Scenario)
	// Calls — число фактических исполнений (повтор ключа не считается).
	Calls() int
	// Secrets — секретные фикстуры, которые не должны протечь в ошибки.
	Secrets() []string
}

// ProviderFactory создаёт harness для одного прогона suite.
type ProviderFactory func(t *testing.T) ProviderHarness

// RunProviderContract проверяет, что реализация provider-port соблюдает контракт:
// успешный вызов, каждую категорию ошибки, тайм-аут, отмену, идемпотентность и
// запрет утечки секретов.
func RunProviderContract(t *testing.T, factory ProviderFactory) {
	t.Helper()

	t.Run("успешный вызов", func(t *testing.T) {
		harness := factory(t)
		harness.Apply(Scenario{Outcome: OutcomeSuccess, ExternalID: "ext-1", Payload: []byte(`{"ok":true}`)})

		result, err := harness.Port().Invoke(context.Background(), validRequest("cmd-1"))
		if err != nil {
			t.Fatalf("вызов: %v", err)
		}
		if result.ExternalID != "ext-1" {
			t.Fatalf("внешний id: %q", result.ExternalID)
		}
		if string(result.Payload) != `{"ok":true}` {
			t.Fatalf("payload: %s", result.Payload)
		}
	})

	t.Run("категории ошибок", func(t *testing.T) {
		for _, category := range integrationerror.AllCategories() {
			t.Run(string(category), func(t *testing.T) {
				harness := factory(t)
				harness.Apply(Scenario{Outcome: OutcomeError, Category: string(category), Reason: "причина отказа"})

				_, err := harness.Port().Invoke(context.Background(), validRequest("cmd-1"))
				var providerErr integrationerror.Error
				if !errors.As(err, &providerErr) {
					t.Fatalf("ожидалась integration-error, получено %v", err)
				}
				if providerErr.Category != category {
					t.Fatalf("категория: got %q, want %q", providerErr.Category, category)
				}
			})
		}
	})

	t.Run("тайм-аут", func(t *testing.T) {
		harness := factory(t)
		harness.Apply(Scenario{Outcome: OutcomeSuccess, Delay: 200 * time.Millisecond})

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		_, err := harness.Port().Invoke(ctx, validRequest("cmd-1"))
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ожидался тайм-аут, получено %v", err)
		}
	})

	t.Run("отмена", func(t *testing.T) {
		harness := factory(t)
		harness.Apply(Scenario{Outcome: OutcomeSuccess, Delay: 200 * time.Millisecond})

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := harness.Port().Invoke(ctx, validRequest("cmd-1"))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ожидалась отмена, получено %v", err)
		}
	})

	t.Run("идемпотентность", func(t *testing.T) {
		harness := factory(t)
		harness.Apply(Scenario{Outcome: OutcomeSuccess, ExternalID: "ext-1"})

		first, err := harness.Port().Invoke(context.Background(), validRequest("cmd-1"))
		if err != nil {
			t.Fatalf("первый вызов: %v", err)
		}
		second, err := harness.Port().Invoke(context.Background(), validRequest("cmd-1"))
		if err != nil {
			t.Fatalf("повторный вызов: %v", err)
		}
		if first.ExternalID != second.ExternalID || first.Operation != second.Operation || !bytes.Equal(first.Payload, second.Payload) {
			t.Fatalf("повтор вернул другой результат: %+v против %+v", second, first)
		}
		if calls := harness.Calls(); calls != 1 {
			t.Fatalf("повтор ключа не должен исполняться второй раз: исполнений %d", calls)
		}
	})

	t.Run("секреты не протекают", func(t *testing.T) {
		harness := factory(t)
		secrets := harness.Secrets()
		if len(secrets) == 0 {
			t.Fatal("harness обязан задать секретные фикстуры")
		}
		harness.Apply(Scenario{
			Outcome:  OutcomeError,
			Category: string(integrationerror.CategoryAuthorizationDenied),
			Reason:   "отказ авторизации: " + strings.Join(secrets, " "),
		})

		_, err := harness.Port().Invoke(context.Background(), validRequest("cmd-1"))
		if err == nil {
			t.Fatal("ожидалась ошибка")
		}
		message := err.Error()
		for _, secret := range secrets {
			if secret == "" {
				continue
			}
			if strings.Contains(message, secret) {
				t.Fatalf("секрет %q протёк в ошибку: %s", secret, message)
			}
		}
	})
}

// validRequest собирает корректный запрос provider-port с заданным ключом команды.
func validRequest(commandKey string) providerport.Request {
	return providerport.Request{
		Envelope: providerport.Envelope{
			EventID:       "evt-" + commandKey,
			OccurredAt:    baseTime,
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			Name:          providerport.ContractName,
			SchemaVersion: providerport.SchemaVersion,
		},
		AdapterName:     "firecrawl",
		IntegrationType: "web-research",
		Operation:       "search",
		CommandKey:      commandKey,
		Payload:         json.RawMessage(`{"query":"логисты"}`),
	}
}
