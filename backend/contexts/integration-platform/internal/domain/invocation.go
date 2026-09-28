package domain

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ErrorCategory — категория нормализованной ошибки интеграции. Набор закрыт
// доменом и совпадает с набором контракта integration-error; политика
// error-normalization работает только с категориями, а не с типами SDK.
type ErrorCategory string

const (
	// ErrorTemporarilyUnavailable — внешняя сторона временно недоступна.
	ErrorTemporarilyUnavailable ErrorCategory = "temporarily-unavailable"
	// ErrorRateLimited — превышен лимит частоты обращений.
	ErrorRateLimited ErrorCategory = "rate-limited"
	// ErrorAuthorizationDenied — отказ авторизации.
	ErrorAuthorizationDenied ErrorCategory = "authorization-denied"
	// ErrorInvalidRequest — запрос отвергнут как некорректный.
	ErrorInvalidRequest ErrorCategory = "invalid-request"
	// ErrorConflict — конфликт состояния (например, повтор с иным телом).
	ErrorConflict ErrorCategory = "conflict"
	// ErrorPermanentFailure — окончательный отказ, повтор бессмыслен.
	ErrorPermanentFailure ErrorCategory = "permanent-failure"
)

// AllErrorCategories возвращает закрытый набор категорий в порядке объявления.
// Возвращённый срез — копия: набор нельзя испортить.
func AllErrorCategories() []ErrorCategory {
	return []ErrorCategory{
		ErrorTemporarilyUnavailable,
		ErrorRateLimited,
		ErrorAuthorizationDenied,
		ErrorInvalidRequest,
		ErrorConflict,
		ErrorPermanentFailure,
	}
}

var knownErrorCategories = func() map[ErrorCategory]struct{} {
	set := make(map[ErrorCategory]struct{}, len(AllErrorCategories()))
	for _, category := range AllErrorCategories() {
		set[category] = struct{}{}
	}
	return set
}()

// IsValidErrorCategory сообщает, входит ли категория в закрытый набор.
func IsValidErrorCategory(category ErrorCategory) bool {
	_, ok := knownErrorCategories[category]
	return ok
}

// ParseErrorCategory разбирает имя категории: регистр и окружающие пробелы не
// значимы, неизвестное имя отвергается.
func ParseErrorCategory(name string) (ErrorCategory, error) {
	normalized := ErrorCategory(strings.ToLower(strings.TrimSpace(name)))
	if !IsValidErrorCategory(normalized) {
		return "", fmt.Errorf("%w: %q", ErrUnknownErrorCategory, name)
	}
	return normalized, nil
}

// IsTemporaryCategory сообщает, считается ли категория временной: успех после
// такой ошибки в том же correlation-ref даёт integration-recovered.
func IsTemporaryCategory(category ErrorCategory) bool {
	switch category {
	case ErrorTemporarilyUnavailable, ErrorRateLimited:
		return true
	default:
		return false
	}
}

// AdapterError — нормализованное представление ошибки адаптера на входе политики
// error-normalization. Типов SDK здесь нет: категория уже выражена доменным
// значением, а Reason — диагностическая причина для журналов.
type AdapterError struct {
	// Category — категория ошибки. Обязательна.
	Category ErrorCategory
	// Reason — диагностическая причина. Секретов содержать не должна и в события
	// не попадает.
	Reason string
}

// FailureOutcome — результат политики error-normalization.
type FailureOutcome struct {
	// Category — нормализованная категория.
	Category ErrorCategory
	// Temporary — считается ли ошибка временной.
	Temporary bool
	// Diagnostic — диагностическая причина для журналов. В события не попадает.
	Diagnostic string
}

// NormalizeError — политика error-normalization: отображает ошибку адаптера в
// категорию контракта integration-error.
//
// Чистая функция: не имеет побочных эффектов, не читает время и не обращается к
// хранилищу. Неизвестная категория отвергается — в домен попадает только значение
// закрытого набора. Диагностическая причина сохраняется отдельно от категории и
// предназначена только для журналов.
func NormalizeError(err AdapterError) (FailureOutcome, error) {
	if !IsValidErrorCategory(err.Category) {
		return FailureOutcome{}, fmt.Errorf("%w: %q", ErrUnknownErrorCategory, err.Category)
	}
	return FailureOutcome{
		Category:   err.Category,
		Temporary:  IsTemporaryCategory(err.Category),
		Diagnostic: strings.TrimSpace(err.Reason),
	}, nil
}

// InvocationState — состояние вызова интеграции по docs/system-map.md §4:
// «выполнен / нормализованная ошибка; тайм-аут; отменён».
type InvocationState string

const (
	// InvocationPending — вызов начат и ещё не завершён.
	InvocationPending InvocationState = "pending"
	// InvocationSucceeded — вызов выполнен.
	InvocationSucceeded InvocationState = "succeeded"
	// InvocationFailed — вызов завершился нормализованной ошибкой.
	InvocationFailed InvocationState = "failed"
	// InvocationTimedOut — вызов прерван по истечении бюджета.
	InvocationTimedOut InvocationState = "timed-out"
	// InvocationCancelled — вызов отменён вызывающим.
	InvocationCancelled InvocationState = "cancelled"
)

// IsTerminal сообщает, что состояние завершает вызов.
func (s InvocationState) IsTerminal() bool { return s != InvocationPending }

// IsValidInvocationState сообщает, объявлено ли состояние доменом.
func (s InvocationState) IsValid() bool {
	switch s {
	case InvocationPending, InvocationSucceeded, InvocationFailed, InvocationTimedOut, InvocationCancelled:
		return true
	default:
		return false
	}
}

// ExternalRef — типизированная привязка внешнего идентификатора поставщика:
// тип интеграции, имя адаптера и внешний id. Внешний id не подменяет внутренний
// идентификатор сущности, а хранится отдельной записью.
type ExternalRef struct {
	// IntegrationType — тип интеграции. Обязателен.
	IntegrationType IntegrationType
	// AdapterName — имя адаптера, присвоившего идентификатор. Обязательно.
	AdapterName AdapterName
	// ExternalID — внешний идентификатор поставщика. Обязателен.
	ExternalID ExternalID
}

func (r ExternalRef) validate() error {
	if err := r.IntegrationType.validate(); err != nil {
		return err
	}
	if err := r.AdapterName.validate(); err != nil {
		return err
	}
	_, err := ParseExternalID(string(r.ExternalID))
	return err
}

// PriorFailure — зафиксированная ранее временная ошибка в том же correlation-ref,
// на основании которой успешный вызов даёт integration-recovered.
type PriorFailure struct {
	// Category — категория предыдущей ошибки. Обязательна и должна быть валидной.
	Category ErrorCategory
}

// IntegrationInvocation — aggregate root integration-invocation: факт вызова
// внешней интеграции. Создаётся на каждый вызов; результат успешного вызова
// хранит привязку внешнего идентификатора отдельно от внутреннего.
type IntegrationInvocation struct {
	id              InvocationID
	tenantID        TenantID
	correlationRef  CorrelationRef
	integrationType IntegrationType
	adapterName     AdapterName
	operation       Operation
	commandKey      CommandKey
	state           InvocationState
	category        ErrorCategory
	externalRef     *ExternalRef
	priorFailure    *PriorFailure
	startedAt       time.Time
	finishedAt      time.Time
	events          []DomainEvent
}

// BeginInvocationParams — данные начала вызова.
type BeginInvocationParams struct {
	ID              InvocationID
	TenantID        TenantID
	CorrelationRef  CorrelationRef
	IntegrationType IntegrationType
	AdapterName     AdapterName
	Operation       Operation
	CommandKey      CommandKey
	StartedAt       time.Time

	// PriorFailure — ранее зафиксированная временная ошибка в том же
	// correlation-ref. Необязательна; влияет только на эмиссию
	// integration-recovered при успехе.
	PriorFailure *PriorFailure
}

// BeginInvocation начинает вызов интеграции. Событий пока не эмитит: исход ещё не
// известен.
func BeginInvocation(params BeginInvocationParams) (*IntegrationInvocation, error) {
	if err := params.ID.validate(); err != nil {
		return nil, err
	}
	if err := params.TenantID.validate(); err != nil {
		return nil, err
	}
	if err := params.CorrelationRef.validate(); err != nil {
		return nil, err
	}
	if err := params.IntegrationType.validate(); err != nil {
		return nil, err
	}
	if err := params.AdapterName.validate(); err != nil {
		return nil, err
	}
	if err := params.Operation.validate(); err != nil {
		return nil, err
	}
	if err := params.CommandKey.validate(); err != nil {
		return nil, err
	}
	if params.StartedAt.IsZero() {
		return nil, fmt.Errorf("%w: вызов %s", ErrInvalidSnapshot, params.ID)
	}
	if params.PriorFailure != nil && !IsValidErrorCategory(params.PriorFailure.Category) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownErrorCategory, params.PriorFailure.Category)
	}

	return &IntegrationInvocation{
		id:              params.ID,
		tenantID:        params.TenantID,
		correlationRef:  params.CorrelationRef,
		integrationType: params.IntegrationType,
		adapterName:     params.AdapterName,
		operation:       params.Operation,
		commandKey:      params.CommandKey,
		state:           InvocationPending,
		priorFailure:    params.PriorFailure,
		startedAt:       params.StartedAt,
	}, nil
}

// ID возвращает нейтральный идентификатор вызова.
func (i *IntegrationInvocation) ID() InvocationID { return i.id }

// TenantID возвращает тенанта-владельца вызова.
func (i *IntegrationInvocation) TenantID() TenantID { return i.tenantID }

// CorrelationRef возвращает ссылку на сквозной сценарий.
func (i *IntegrationInvocation) CorrelationRef() CorrelationRef { return i.correlationRef }

// IntegrationType возвращает тип интеграции.
func (i *IntegrationInvocation) IntegrationType() IntegrationType { return i.integrationType }

// AdapterName возвращает имя выбранного адаптера.
func (i *IntegrationInvocation) AdapterName() AdapterName { return i.adapterName }

// Operation возвращает имя операции.
func (i *IntegrationInvocation) Operation() Operation { return i.operation }

// CommandKey возвращает ключ команды инициатора.
func (i *IntegrationInvocation) CommandKey() CommandKey { return i.commandKey }

// State возвращает состояние вызова.
func (i *IntegrationInvocation) State() InvocationState { return i.state }

// Category возвращает категорию нормализованной ошибки. Пуста у успешного,
// отменённого и начатого вызова.
func (i *IntegrationInvocation) Category() ErrorCategory { return i.category }

// ExternalRef возвращает привязку внешнего идентификатора. Второе значение —
// false, если вызов не завершился успехом.
func (i *IntegrationInvocation) ExternalRef() (ExternalRef, bool) {
	if i.externalRef == nil {
		return ExternalRef{}, false
	}
	return *i.externalRef, true
}

// StartedAt возвращает момент начала вызова.
func (i *IntegrationInvocation) StartedAt() time.Time { return i.startedAt }

// FinishedAt возвращает момент завершения вызова. Нулевой у незавершённого.
func (i *IntegrationInvocation) FinishedAt() time.Time { return i.finishedAt }

// Complete завершает вызов успехом. Привязка внешнего идентификатора необязательна:
// поставщик вправе не возвращать собственный идентификатор. Успех после
// зафиксированной временной ошибки в том же correlation-ref эмитит
// integration-recovered.
func (i *IntegrationInvocation) Complete(ref *ExternalRef, at time.Time) error {
	if err := i.requirePending(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("%w: вызов %s", ErrInvalidSnapshot, i.id)
	}
	if ref != nil {
		if err := ref.validate(); err != nil {
			return err
		}
		if ref.IntegrationType != i.integrationType || ref.AdapterName != i.adapterName {
			return fmt.Errorf("%w: вызов %s", ErrInvalidExternalRef, i.id)
		}
		bound := *ref
		i.externalRef = &bound
	}

	i.state = InvocationSucceeded
	i.finishedAt = at
	if i.priorFailure != nil && IsTemporaryCategory(i.priorFailure.Category) {
		emit(&i.events, IntegrationRecovered{
			InvocationID:    i.id,
			TenantID:        i.tenantID,
			CorrelationRef:  i.correlationRef,
			IntegrationType: i.integrationType,
			AdapterName:     i.adapterName,
			Operation:       i.operation,
			At:              at,
		})
	}
	return nil
}

// Fail завершает вызов нормализованной ошибкой. Диагностическая причина
// принимается только для журналов и в событие не попадает.
func (i *IntegrationInvocation) Fail(err AdapterError, at time.Time) error {
	if err := i.requirePending(); err != nil {
		return err
	}
	outcome, normalizeErr := NormalizeError(err)
	if normalizeErr != nil {
		return normalizeErr
	}
	if at.IsZero() {
		return fmt.Errorf("%w: вызов %s", ErrInvalidSnapshot, i.id)
	}

	i.state = InvocationFailed
	i.category = outcome.Category
	i.finishedAt = at
	emit(&i.events, IntegrationFailed{
		InvocationID:    i.id,
		TenantID:        i.tenantID,
		CorrelationRef:  i.correlationRef,
		IntegrationType: i.integrationType,
		AdapterName:     i.adapterName,
		Operation:       i.operation,
		Category:        outcome.Category,
		At:              at,
	})
	return nil
}

// Timeout завершает вызов по истечении бюджета. Тайм-аут — нормализованная ошибка
// с категорией temporarily-unavailable, поэтому эмитит integration-failed.
func (i *IntegrationInvocation) Timeout(at time.Time) error {
	if err := i.requirePending(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("%w: вызов %s", ErrInvalidSnapshot, i.id)
	}
	i.state = InvocationTimedOut
	i.category = ErrorTemporarilyUnavailable
	i.finishedAt = at
	emit(&i.events, IntegrationFailed{
		InvocationID:    i.id,
		TenantID:        i.tenantID,
		CorrelationRef:  i.correlationRef,
		IntegrationType: i.integrationType,
		AdapterName:     i.adapterName,
		Operation:       i.operation,
		Category:        ErrorTemporarilyUnavailable,
		At:              at,
	})
	return nil
}

// Cancel завершает вызов отменой вызывающего. Отмена не является ошибкой
// интеграции, поэтому события не эмитит.
func (i *IntegrationInvocation) Cancel(at time.Time) error {
	if err := i.requirePending(); err != nil {
		return err
	}
	if at.IsZero() {
		return fmt.Errorf("%w: вызов %s", ErrInvalidSnapshot, i.id)
	}
	i.state = InvocationCancelled
	i.finishedAt = at
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности вызова —
// (tenant_id, тип интеграции, операция, ключ команды).
func (i *IntegrationInvocation) IdempotencyKey() string {
	return InvocationKey(i.tenantID, i.integrationType, i.operation, i.commandKey)
}

// DomainEvents возвращает копию неподтверждённых событий.
func (i *IntegrationInvocation) DomainEvents() []DomainEvent { return copyEvents(i.events) }

// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
func (i *IntegrationInvocation) PullDomainEvents() []DomainEvent { return drainEvents(&i.events) }

// requirePending отвергает повторный переход завершённого вызова.
func (i *IntegrationInvocation) requirePending() error {
	if i.state.IsTerminal() {
		return fmt.Errorf("%w: вызов %s в состоянии %q", ErrInvalidInvocationTransition, i.id, i.state)
	}
	return nil
}

// InvocationKey вычисляет ключ идемпотентности вызова по его компонентам. Ключ
// внутренний; компоненты кодируются, чтобы разделитель не допускал коллизий.
func InvocationKey(tenant TenantID, integrationType IntegrationType, operation Operation, commandKey CommandKey) string {
	return strings.Join([]string{
		encodeKeyPart(string(tenant)),
		encodeKeyPart(string(integrationType)),
		encodeKeyPart(string(operation)),
		encodeKeyPart(string(commandKey)),
	}, "|")
}

func encodeKeyPart(part string) string { return url.QueryEscape(part) }

// Проверка на этапе компиляции: агрегат публикует свои события.
var _ EventProducer = (*IntegrationInvocation)(nil)
