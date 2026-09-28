package domain

import "time"

// EventName — имя domain event. Имена событий домена совпадают с именами из
// architecture/domain-catalog.yaml; публикуемые контракты оборачивают их в
// envelope на границе адаптера.
type EventName string

const (
	// EventAdapterRegistered — адаптер зарегистрирован с объявленным manifest.
	EventAdapterRegistered EventName = "adapter-registered"

	// EventIntegrationFailed — вызов завершился нормализованной ошибкой
	// (включая тайм-аут).
	EventIntegrationFailed EventName = "integration-failed"

	// EventIntegrationRecovered — вызов успешен после зафиксированной временной
	// ошибки в том же correlation-ref.
	EventIntegrationRecovered EventName = "integration-recovered"
)

// DomainEvent — факт домена, произведённый агрегатом.
type DomainEvent interface {
	// Name возвращает имя события.
	Name() EventName

	// OccurredAt возвращает время события.
	OccurredAt() time.Time
}

// AdapterRegistered — событие о регистрации адаптера.
//
// Несёт только идентификатор, имя, версию manifest и объявленные возможности:
// секретов в событии нет (инвариант о секретах).
type AdapterRegistered struct {
	AdapterID       AdapterID
	AdapterName     AdapterName
	ManifestVersion ManifestVersion
	Capabilities    []CapabilityName
	At              time.Time
}

// Name реализует DomainEvent.
func (e AdapterRegistered) Name() EventName { return EventAdapterRegistered }

// OccurredAt реализует DomainEvent.
func (e AdapterRegistered) OccurredAt() time.Time { return e.At }

// IntegrationFailed — событие о вызове, завершившемся нормализованной ошибкой.
//
// Несёт категорию, но не диагностическую причину: причина предназначена только
// для журналов и в события не попадает (инвариант о секретах).
type IntegrationFailed struct {
	InvocationID    InvocationID
	TenantID        TenantID
	CorrelationRef  CorrelationRef
	IntegrationType IntegrationType
	AdapterName     AdapterName
	Operation       Operation
	Category        ErrorCategory
	At              time.Time
}

// Name реализует DomainEvent.
func (e IntegrationFailed) Name() EventName { return EventIntegrationFailed }

// OccurredAt реализует DomainEvent.
func (e IntegrationFailed) OccurredAt() time.Time { return e.At }

// IntegrationRecovered — событие об успешном вызове, следующем за зафиксированной
// временной ошибкой в том же correlation-ref.
type IntegrationRecovered struct {
	InvocationID    InvocationID
	TenantID        TenantID
	CorrelationRef  CorrelationRef
	IntegrationType IntegrationType
	AdapterName     AdapterName
	Operation       Operation
	At              time.Time
}

// Name реализует DomainEvent.
func (e IntegrationRecovered) Name() EventName { return EventIntegrationRecovered }

// OccurredAt реализует DomainEvent.
func (e IntegrationRecovered) OccurredAt() time.Time { return e.At }

// EventProducer — агрегат, публикующий свои неподтверждённые события.
type EventProducer interface {
	// DomainEvents возвращает копию неподтверждённых событий.
	DomainEvents() []DomainEvent

	// PullDomainEvents возвращает неподтверждённые события и очищает буфер.
	PullDomainEvents() []DomainEvent
}

// emit добавляет событие в буфер агрегата.
func emit(events *[]DomainEvent, event DomainEvent) {
	*events = append(*events, event)
}

// copyEvents и drainEvents — общие помощники агрегатов для работы с буфером.
func copyEvents(events []DomainEvent) []DomainEvent {
	return append([]DomainEvent(nil), events...)
}

func drainEvents(events *[]DomainEvent) []DomainEvent {
	drained := copyEvents(*events)
	*events = nil
	return drained
}
