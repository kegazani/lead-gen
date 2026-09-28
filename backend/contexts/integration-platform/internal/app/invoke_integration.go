package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/contexts/integration-platform/internal/domain"
	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/contexts/integration-platform/internal/ports/contracts"
	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/providerport"
)

// InvokeIntegrationCommand — команда вызова внешней интеграции.
type InvokeIntegrationCommand struct {
	// InvocationID — нейтральный идентификатор вызова. Обязателен.
	InvocationID domain.InvocationID
	// EventID — идентификатор входящего сообщения (конверт контракта). Обязателен.
	EventID string
	// OccurredAt — момент возникновения команды. Обязателен.
	OccurredAt time.Time
	// TenantID — тенант-владелец. Обязателен.
	TenantID domain.TenantID
	// CorrelationRef — ссылка на сквозной сценарий. Обязательна.
	CorrelationRef domain.CorrelationRef
	// CausationID — идентификатор сообщения-причины. Необязателен.
	CausationID string
	// IntegrationType — тип интеграции. Обязателен.
	IntegrationType domain.IntegrationType
	// Operation — имя операции (возможности). Обязательно.
	Operation domain.Operation
	// CommandKey — ключ команды инициатора. Обязателен.
	CommandKey domain.CommandKey
	// AdapterName — заранее выбранный адаптер. Необязателен: если не задан,
	// адаптер выбирается политикой adapter-selection.
	AdapterName *domain.AdapterName
	// RequiredCapabilities — обязательные возможности. Пустой список означает
	// единственную возможность, названную операцией.
	RequiredCapabilities []domain.CapabilityName
	// Payload — входной документ операции. Незрозрачен для контекста.
	Payload json.RawMessage
}

// InvokeIntegrationResult — результат вызова.
type InvokeIntegrationResult struct {
	InvocationID domain.InvocationID
	State        domain.InvocationState
	// Category — категория нормализованной ошибки. Пуста при успехе.
	Category domain.ErrorCategory
	// ExternalRef — привязка внешнего идентификатора. Задана при успехе.
	ExternalRef *domain.ExternalRef
	// Recovered равно true, если успех дал integration-recovered.
	Recovered bool
	// Replayed равно true, если результат возвращён из сохранённого вызова по
	// ключу идемпотентности без повторного обращения к поставщику.
	Replayed bool
	// Events — неподтверждённые события вызова.
	Events []domain.DomainEvent
}

// InvokeIntegrationHandler — use case вызова интеграции.
type InvokeIntegrationHandler struct {
	registrations ports.AdapterRegistrationRepository
	invocations   ports.IntegrationInvocationRepository
	provider      ports.ProviderPort
	clock         Clock
}

// NewInvokeIntegrationHandler собирает use case из портов и источника времени.
func NewInvokeIntegrationHandler(
	registrations ports.AdapterRegistrationRepository,
	invocations ports.IntegrationInvocationRepository,
	provider ports.ProviderPort,
	clock Clock,
) *InvokeIntegrationHandler {
	if clock == nil {
		clock = SystemClock{}
	}
	return &InvokeIntegrationHandler{
		registrations: registrations,
		invocations:   invocations,
		provider:      provider,
		clock:         clock,
	}
}

// Handle выполняет вызов: загрузка регистрации → Require обязательных
// возможностей → вызов через порт ProviderPort → нормализация исхода → запись
// вызова с привязкой внешнего id и событиями.
//
// Повтор команды с тем же ключом идемпотентности не обращается к поставщику
// повторно, а возвращает сохранённый результат.
func (h *InvokeIntegrationHandler) Handle(ctx context.Context, cmd InvokeIntegrationCommand) (InvokeIntegrationResult, error) {
	tenant, correlation, integrationType, operation, commandKey, invocationID, err := validateInvokeCommand(cmd)
	if err != nil {
		return InvokeIntegrationResult{}, err
	}

	key := domain.InvocationKey(tenant, integrationType, operation, commandKey)
	existing, err := h.invocations.FindByIdempotencyKey(ctx, key)
	switch {
	case err == nil && existing != nil:
		return resultOf(existing, true), replayError(existing)
	case err != nil && !errors.Is(err, ports.ErrInvocationNotFound):
		return InvokeIntegrationResult{}, fmt.Errorf("поиск вызова по ключу идемпотентности: %w", err)
	}

	required, err := requiredCapabilities(cmd, operation)
	if err != nil {
		return InvokeIntegrationResult{}, err
	}
	registration, err := h.resolveRegistration(ctx, cmd, required)
	if err != nil {
		return InvokeIntegrationResult{}, err
	}
	// Инвариант «возможности проверяются до вызова»: регистрация обязана объявить
	// каждую обязательную возможность, иначе вызов не начинается.
	for _, capability := range required {
		if err := registration.Require(capability); err != nil {
			return InvokeIntegrationResult{}, err
		}
	}

	prior, err := h.priorFailure(ctx, correlation)
	if err != nil {
		return InvokeIntegrationResult{}, err
	}

	startedAt := h.clock.Now()
	invocation, err := domain.BeginInvocation(domain.BeginInvocationParams{
		ID:              invocationID,
		TenantID:        tenant,
		CorrelationRef:  correlation,
		IntegrationType: integrationType,
		AdapterName:     registration.Name(),
		Operation:       operation,
		CommandKey:      commandKey,
		StartedAt:       startedAt,
		PriorFailure:    prior,
	})
	if err != nil {
		return InvokeIntegrationResult{}, err
	}

	request := providerport.Request{
		Envelope:        contracts.NewProviderEnvelope(providerEnvelope(cmd, tenant, correlation)),
		AdapterName:     registration.Name().String(),
		IntegrationType: providerport.IntegrationType(integrationType),
		Operation:       providerport.Operation(operation),
		CommandKey:      commandKey.String(),
		Payload:         cmd.Payload,
	}
	result, invokeErr := h.provider.Invoke(ctx, request)
	finishedAt := h.clock.Now()
	if err := recordOutcome(invocation, registration.Name(), integrationType, result, invokeErr, finishedAt); err != nil {
		return InvokeIntegrationResult{}, err
	}

	if err := h.invocations.Save(ctx, invocation); err != nil {
		return InvokeIntegrationResult{}, fmt.Errorf("сохранение вызова интеграции %s: %w", invocation.ID(), err)
	}

	outcome := resultOf(invocation, false)
	outcome.Events = invocation.PullDomainEvents()
	outcome.Recovered = containsRecovery(outcome.Events)
	return outcome, wrapInvokeError(invocation.ID(), invokeErr)
}

// validateInvokeCommand проверяет обязательные поля команды.
func validateInvokeCommand(cmd InvokeIntegrationCommand) (
	domain.TenantID, domain.CorrelationRef, domain.IntegrationType,
	domain.Operation, domain.CommandKey, domain.InvocationID, error,
) {
	tenant, err := domain.ParseTenantID(string(cmd.TenantID))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	correlation, err := domain.ParseCorrelationRef(string(cmd.CorrelationRef))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	integrationType, err := domain.ParseIntegrationType(string(cmd.IntegrationType))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	operation, err := domain.ParseOperation(string(cmd.Operation))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	commandKey, err := domain.ParseCommandKey(string(cmd.CommandKey))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	invocationID, err := domain.ParseInvocationID(string(cmd.InvocationID))
	if err != nil {
		return "", "", "", "", "", "", fmt.Errorf("%w: %v", ErrInvalidCommand, err)
	}
	if cmd.OccurredAt.IsZero() {
		return "", "", "", "", "", "", fmt.Errorf("%w: occurred_at", ErrInvalidCommand)
	}
	return tenant, correlation, integrationType, operation, commandKey, invocationID, nil
}

// requiredCapabilities определяет обязательные возможности: заданные явно или
// выведенные из имени операции.
func requiredCapabilities(cmd InvokeIntegrationCommand, operation domain.Operation) ([]domain.CapabilityName, error) {
	if len(cmd.RequiredCapabilities) > 0 {
		return cmd.RequiredCapabilities, nil
	}
	capability, err := domain.ParseCapabilityName(string(operation))
	if err != nil {
		return nil, err
	}
	return []domain.CapabilityName{capability}, nil
}

// resolveRegistration загружает названный адаптер или выбирает его политикой.
func (h *InvokeIntegrationHandler) resolveRegistration(
	ctx context.Context,
	cmd InvokeIntegrationCommand,
	required []domain.CapabilityName,
) (*domain.AdapterRegistration, error) {
	if cmd.AdapterName != nil {
		registration, err := h.registrations.FindByName(ctx, *cmd.AdapterName)
		if errors.Is(err, ports.ErrAdapterRegistrationNotFound) {
			return nil, fmt.Errorf(
				"%w: контракт %q: адаптер %q не зарегистрирован",
				domain.ErrAdapterNotFound, contracts.ContractProviderPort, *cmd.AdapterName,
			)
		}
		if err != nil {
			return nil, fmt.Errorf("загрузка регистрации адаптера %q: %w", *cmd.AdapterName, err)
		}
		return registration, nil
	}

	registrations, err := h.registrations.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("загрузка регистраций адаптеров: %w", err)
	}
	selection, err := domain.SelectAdapter(registrations, domain.AdapterSelectionRequest{
		Contract: contracts.ContractProviderPort,
		Required: required,
	})
	if err != nil {
		return nil, err
	}
	return selection.Adapter, nil
}

// priorFailure загружает зафиксированную временную ошибку в том же
// correlation-ref, чтобы успех дал integration-recovered.
func (h *InvokeIntegrationHandler) priorFailure(ctx context.Context, correlation domain.CorrelationRef) (*domain.PriorFailure, error) {
	previous, err := h.invocations.FindLatestByCorrelation(ctx, correlation)
	switch {
	case err == nil && previous != nil:
	case errors.Is(err, ports.ErrInvocationNotFound):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("поиск предыдущего вызова в correlation-ref %s: %w", correlation, err)
	}
	switch previous.State() {
	case domain.InvocationFailed, domain.InvocationTimedOut:
		return &domain.PriorFailure{Category: previous.Category()}, nil
	default:
		return nil, nil
	}
}

// providerEnvelope собирает конверт контракта provider-port из команды.
func providerEnvelope(cmd InvokeIntegrationCommand, tenant domain.TenantID, correlation domain.CorrelationRef) providerport.Envelope {
	return providerport.Envelope{
		EventID:       cmd.EventID,
		OccurredAt:    cmd.OccurredAt,
		TenantID:      tenant.String(),
		CorrelationID: correlation.String(),
		CausationID:   cmd.CausationID,
	}
}

// recordOutcome фиксирует исход вызова в агрегате, различая успех, тайм-аут,
// отмену и нормализованную ошибку.
func recordOutcome(
	invocation *domain.IntegrationInvocation,
	adapterName domain.AdapterName,
	integrationType domain.IntegrationType,
	result providerport.Result,
	invokeErr error,
	at time.Time,
) error {
	switch {
	case invokeErr == nil:
		var ref *domain.ExternalRef
		if externalID := strings.TrimSpace(result.ExternalID); externalID != "" {
			// Привязка внешнего идентификатора хранится отдельно от внутреннего.
			// Пустой внешний id в привязку не попадает.
			ref = &domain.ExternalRef{
				IntegrationType: integrationType,
				AdapterName:     adapterName,
				ExternalID:      domain.ExternalID(externalID),
			}
		}
		return invocation.Complete(ref, at)
	case errors.Is(invokeErr, context.DeadlineExceeded):
		return invocation.Timeout(at)
	case errors.Is(invokeErr, context.Canceled):
		return invocation.Cancel(at)
	default:
		return invocation.Fail(domain.AdapterError{Category: normalizeCategory(invokeErr), Reason: invokeErr.Error()}, at)
	}
}

// normalizeCategory сворачивает ошибку поставщика в доменную категорию. Ошибка
// контракта integration-error даёт свою категорию; любая другая — окончательный
// отказ, чтобы тип SDK не протекал в домен.
func normalizeCategory(err error) domain.ErrorCategory {
	var providerErr integrationerror.Error
	if errors.As(err, &providerErr) {
		if category, parseErr := contracts.ParseErrorCategory(providerErr.Category); parseErr == nil {
			return category
		}
	}
	return domain.ErrorPermanentFailure
}

// resultOf строит результат из сохранённого вызова.
func resultOf(invocation *domain.IntegrationInvocation, replayed bool) InvokeIntegrationResult {
	result := InvokeIntegrationResult{
		InvocationID: invocation.ID(),
		State:        invocation.State(),
		Category:     invocation.Category(),
		Replayed:     replayed,
	}
	if ref, ok := invocation.ExternalRef(); ok {
		result.ExternalRef = &ref
	}
	return result
}

// replayError восстанавливает ошибку для повторно возвращённого вызова.
func replayError(invocation *domain.IntegrationInvocation) error {
	switch invocation.State() {
	case domain.InvocationSucceeded:
		return nil
	case domain.InvocationCancelled:
		return fmt.Errorf("приложение: вызов %s отменён: %w", invocation.ID(), context.Canceled)
	case domain.InvocationTimedOut:
		return fmt.Errorf("приложение: вызов %s прерван по тайм-ауту: %w", invocation.ID(), context.DeadlineExceeded)
	default:
		return fmt.Errorf("%w: вызов %s, категория %q", ErrInvocationFailed, invocation.ID(), invocation.Category())
	}
}

// wrapInvokeError оборачивает ошибку поставщика для вызывающего. Диагностическая
// причина уже нормализована адаптером и секретов не несёт.
func wrapInvokeError(id domain.InvocationID, invokeErr error) error {
	if invokeErr == nil {
		return nil
	}
	return fmt.Errorf("приложение: вызов интеграции %s: %w", id, invokeErr)
}

// containsRecovery сообщает, есть ли среди событий integration-recovered.
func containsRecovery(events []domain.DomainEvent) bool {
	for _, event := range events {
		if event.Name() == domain.EventIntegrationRecovered {
			return true
		}
	}
	return false
}
