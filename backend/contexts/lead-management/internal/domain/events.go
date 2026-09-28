package domain

// DomainEvent — доменное событие. События объявляются в домене и в срезе наружу
// контекста не публикуются: потребителей нет. Прикладной слой может вычитать их
// через Events() агрегата, но не отправляет за пределы контекста.
type DomainEvent interface {
	// EventName возвращает имя события.
	EventName() string
}

// LeadProfileEnriched — событие обогащения карточки компании новыми полями.
type LeadProfileEnriched struct {
	// CompanyID — идентификатор компании (нормализованный домен).
	CompanyID CompanyID
	// At — момент обогащения.
	At string
}

// EventName реализует DomainEvent.
func (LeadProfileEnriched) EventName() string { return "lead-profile-enriched" }

// LeadAssessed — событие присвоения компании оценки относительно конкретного
// задания (domain event lead-assessed агрегата lead-assessment).
type LeadAssessed struct {
	// JobID — идентификатор задания, в контексте которого получена оценка.
	JobID string
	// CompanyID — идентификатор оцениваемой компании.
	CompanyID string
	// Verdict — присвоенный вердикт.
	Verdict string
	// At — момент оценки.
	At string
}

// EventName реализует DomainEvent.
func (LeadAssessed) EventName() string { return "lead-assessed" }

// LeadAssessmentRevised — событие изменения вердикта оценки в рамках того же
// задания (domain event lead-assessment-revised агрегата lead-assessment).
type LeadAssessmentRevised struct {
	// JobID — идентификатор задания, в контексте которого изменена оценка.
	JobID string
	// CompanyID — идентификатор оцениваемой компании.
	CompanyID string
	// Verdict — новый вердикт.
	Verdict string
	// At — момент изменения.
	At string
}

// EventName реализует DomainEvent.
func (LeadAssessmentRevised) EventName() string { return "lead-assessment-revised" }

// MessageSent — событие успешной отправки письма лиду (domain event message-sent
// агрегата outbound-message). Объявлено в домене и наружу контекста не
// публикуется: потребитель вычитывает его через Events() агрегата.
type MessageSent struct {
	// MessageID — идентификатор отправленного сообщения.
	MessageID string
	// Recipient — адрес получателя.
	Recipient string
	// At — момент отправки.
	At string
}

// EventName реализует DomainEvent.
func (MessageSent) EventName() string { return "message-sent" }
