// Package fake — тестовый адаптер контракта provider-port. Он даёт
// детерминированные ответы и эмулирует каждую категорию integration-error,
// тайм-аут и отмену, а также соблюдает ключ идемпотентности.
//
// Адаптер не ходит в сеть и не импортирует SDK: это инструмент contract suite и
// тестов, а не production-реализация. Он намеренно хранит секреты во внутренней
// конфигурации и обязан не проецировать их в ошибки — так проверяется запрет
// утечки секретов.
package fake

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"
)

// OutcomeKind — вид запрограммированного исхода вызова.
type OutcomeKind string

const (
	// OutcomeSuccess — успешный ответ.
	OutcomeSuccess OutcomeKind = "success"
	// OutcomeError — нормализованная ошибка заданной категории.
	OutcomeError OutcomeKind = "error"
	// OutcomeTimeout — прерывание по истечении бюджета.
	OutcomeTimeout OutcomeKind = "timeout"
	// OutcomeCancel — отмена вызывающим.
	OutcomeCancel OutcomeKind = "cancel"
)

// Scenario — запрограммированный исход следующего вызова.
type Scenario struct {
	// Kind — вид исхода. Нулевое значение трактуется как OutcomeSuccess.
	Kind OutcomeKind
	// Category — категория ошибки для OutcomeError.
	Category integrationerror.Category
	// ExternalID — внешний идентификатор поставщика для успеха.
	ExternalID string
	// Payload — выходной документ операции.
	Payload json.RawMessage
	// Reason — сырая диагностическая причина. Может содержать секрет: адаптер
	// обязан вычистить секреты перед возвратом ошибки.
	Reason string
	// Delay — задержка перед ответом. Даёт вызвать тайм-аут или отмену.
	Delay time.Duration
}

// Config — конфигурация адаптера.
type Config struct {
	// Scenario — исход по умолчанию. Нулевое значение — успех.
	Scenario Scenario
	// Secrets — секреты и адреса, которые адаптер не должен проецировать в
	// ошибки и диагностику.
	Secrets []string
}

// Provider — детерминированный тестовый адаптер provider-port.
type Provider struct {
	mu       sync.Mutex
	secrets  []string
	scenario Scenario
	calls    int
	applied  map[string]providerport.Result
}

var _ providerport.Port = (*Provider)(nil)

// New создаёт адаптер с пустым журналом применённых ключей.
func New(cfg Config) *Provider {
	return &Provider{
		secrets:  append([]string(nil), cfg.Secrets...),
		scenario: cfg.Scenario,
		applied:  make(map[string]providerport.Result),
	}
}

// Port возвращает адаптер как интерфейс контракта.
func (p *Provider) Port() providerport.Port { return p }

// Configure задаёт исход следующих вызовов.
func (p *Provider) Configure(scenario Scenario) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.scenario = scenario
}

// Calls возвращает число фактических исполнений. Повтор ключа идемпотентности
// исполнением не считается.
func (p *Provider) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// Secrets возвращает секретные фикстуры адаптера.
func (p *Provider) Secrets() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.secrets...)
}

// Invoke реализует providerport.Port.
//
// Порядок шагов: проверка контракта, дедупликация по ключу идемпотентности,
// задержка с учётом отмены, затем запрограммированный исход. Повтор уже
// применённого ключа не исполняется повторно и возвращает сохранённый результат.
func (p *Provider) Invoke(ctx context.Context, request providerport.Request) (providerport.Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := request.Validate(); err != nil {
		return providerport.Result{}, err
	}

	key := request.IdempotencyKey()
	p.mu.Lock()
	if result, ok := p.applied[key]; ok {
		p.mu.Unlock()
		return result, nil
	}
	p.calls++
	scenario := p.scenario
	p.mu.Unlock()

	if scenario.Delay > 0 {
		timer := time.NewTimer(scenario.Delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return providerport.Result{}, ctx.Err()
		case <-timer.C:
		}
	}
	if err := ctx.Err(); err != nil {
		return providerport.Result{}, err
	}

	switch scenario.Kind {
	case OutcomeTimeout:
		return providerport.Result{}, context.DeadlineExceeded
	case OutcomeCancel:
		return providerport.Result{}, context.Canceled
	case OutcomeError:
		return providerport.Result{}, integrationerror.Error{
			Category:     p.category(scenario.Category),
			AdapterName:  request.AdapterName,
			ContractName: providerport.ContractName,
			Reason:       p.sanitize(scenario.Reason),
		}
	default:
		result := providerport.Result{
			Operation:  request.Operation,
			ExternalID: scenario.ExternalID,
			Payload:    scenario.Payload,
		}
		p.mu.Lock()
		p.applied[key] = result
		p.mu.Unlock()
		return result, nil
	}
}

func (p *Provider) category(category integrationerror.Category) integrationerror.Category {
	if integrationerror.IsValidCategory(category) {
		return category
	}
	return integrationerror.CategoryPermanentFailure
}

// sanitize вычищает секреты из диагностической причины.
func (p *Provider) sanitize(reason string) string {
	for _, secret := range p.secrets {
		if secret == "" {
			continue
		}
		reason = strings.ReplaceAll(reason, secret, "[redacted]")
	}
	return reason
}
