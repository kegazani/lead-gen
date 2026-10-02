package ports

import (
	"context"
	"time"
)

// Имена видов нормализованных событий дашборда клиента (контракт
// analytics-event). Совпадают с видами, которые читает проекция analytics, чтобы
// продюсер и потребитель говорили об одном наборе.
const (
	// AnalyticsKindLeadWorkflow — смена стадии воронки лида.
	AnalyticsKindLeadWorkflow = "lead-workflow"
	// AnalyticsKindMessageSent — успешная отправка письма лиду.
	AnalyticsKindMessageSent = "message-sent"
)

// AnalyticsEvent — нормализованное доменное событие дашборда клиента. Форма
// повторяет тело контракта analytics-event и приём dev-контура dashboard:
// событие без тенанта или компании проекция игнорирует, поэтому продюсер обязан
// заполнить TenantID и CompanyID.
type AnalyticsEvent struct {
	// EventID — идентификатор сообщения для дедупликации (at-least-once).
	EventID string
	// TenantID — тенант, в чьём срезе учитывается событие.
	TenantID string
	// Kind — вид события (см. AnalyticsKind*).
	Kind string
	// CompanyID — идентификатор компании (лида).
	CompanyID string
	// Status — статус события: для lead-workflow — стадия воронки.
	Status string
	// Outcome — исход события: для message-sent — отправлено.
	Outcome string
	// OccurredAt — момент события.
	OccurredAt time.Time
	// ReplyText — текст ответа компании; проекцией не сохраняется.
	ReplyText string
}

// AnalyticsPublisher публикует нормализованное событие дашборда клиента в
// брокер. nil означает, что публикация не собрана: событие никому не уходит.
//
// Реализация должна соблюдать семантику контракта: принимать context.Context и
// уважать отмену. Публикация — вторичный эффект пользовательского действия, и её
// сбой не отменяет уже выполненный доменный эффект (решение о реакции принимает
// вызывающий).
type AnalyticsPublisher interface {
	Publish(ctx context.Context, event AnalyticsEvent) error
}
