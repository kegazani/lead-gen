package outbox

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

// relayBatch — размер порции записей за проход. Ограничение не даёт одному
// проходу занять журнал надолго и позволяет уложиться в бюджет при большом
// накоплении.
const relayBatch = 100

// defaultInterval — интервал проходов, если задано некорректное значение.
const defaultInterval = time.Second

// outboxPublishFailuresTotal — счётчик отказов публикации записи outbox. Растёт
// при каждой ошибке публикации, запись остаётся неподтверждённой и повторяется
// (design.md, Решение 5).
const outboxPublishFailuresTotal = "outbox_publish_failures_total"

// correlationHeader — ключ заголовков записи outbox, несущий correlation_id.
// Совпадает с именем поля конверта (platform/contracts.Envelope.CorrelationID).
const correlationHeader = "correlation_id"

// publishError — ошибка публикации записи outbox (отказ publisher.Publish).
// Обёртка отличает её от ошибок чтения журнала и подтверждения записи: счётчик
// отказов публикации учитывает только этот исход, а не любую ошибку прохода
// (design.md, Решение 5).
type publishError struct{ err error }

func (e *publishError) Error() string { return e.err.Error() }
func (e *publishError) Unwrap() error { return e.err }

// Relay публикует накопленные записи journal в publisher после commit.
type Relay struct {
	journal   Journal
	publisher Publisher
	interval  time.Duration

	// logger и metrics — наблюдаемость публикации. Значения подставляет
	// composition root; nil — публикация не логируется и отказы не считаются.
	logger  observ.Logger
	metrics observ.Metrics
}

// NewRelay собирает relay. Некорректный интервал (<= 0) заменяется значением по
// умолчанию; первый проход выполняется сразу при запуске, не дожидаясь интервала.
func NewRelay(j Journal, p Publisher, interval time.Duration) *Relay {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Relay{journal: j, publisher: p, interval: interval}
}

// WithLogger задаёт логгер жизненного цикла публикации и возвращает relay.
func (r *Relay) WithLogger(logger observ.Logger) *Relay {
	r.logger = logger
	return r
}

// WithMetrics задаёт приёмник метрик для счётчика отказов публикации.
func (r *Relay) WithMetrics(metrics observ.Metrics) *Relay {
	r.metrics = metrics
	return r
}

// Run публикует ожидающие записи и повторяет проходы с заданным интервалом до
// отмены контекста. Отмена — штатное завершение, Run возвращает nil.
//
// Ошибка отдельного прохода не прерывает цикл: запись, не прошедшая
// подтверждение, остаётся в журнале и будет повторена. Так временный отказ
// брокера не останавливает relay и не теряет сообщение.
func (r *Relay) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Немедленный первый проход: после commit сообщение уходит без задержки.
	r.publish(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.publish(ctx)
		}
	}
}

// publish выполняет один проход публикации. Ошибка прохода не прерывает цикл и
// не подтверждает запись. Счётчик отказов публикации растёт только при отказе
// самого publisher.Publish (publishError); ошибки чтения журнала и подтверждения
// записи логируются, но отказом публикации не считаются (design.md, Решение 5).
func (r *Relay) publish(ctx context.Context) {
	if err := r.publishPending(ctx); err != nil {
		var pubErr *publishError
		if errors.As(err, &pubErr) && r.metrics != nil {
			r.metrics.Incr(outboxPublishFailuresTotal, nil)
		}
	}
}

// publishPending публикует все ожидающие записи в порядке записи и подтверждает
// каждую сразу после успешной публикации.
//
// Подтверждение идёт строго после публикации (publish after commit): падение
// между ними оставляет запись неподтверждённой, и она будет опубликована
// повторно. Ошибка публикации останавливает проход, не подтверждая текущую и
// последующие записи, — порядок сохраняется, а повтор разбирает потребитель по
// event_id (at-least-once). Ошибка публикации возвращается как *publishError,
// чтобы вызывающий отличал её от ошибок чтения журнала и подтверждения записи.
func (r *Relay) publishPending(ctx context.Context) error {
	pending, err := r.journal.Pending(ctx, relayBatch)
	if err != nil {
		if r.logger != nil {
			r.logger.Error("outbox: чтение журнала не удалось", nil, observ.Fields{}, "failure_category", "journal_read")
		}
		return err
	}
	for _, m := range pending {
		fields := observ.Fields{CorrelationID: m.Headers[correlationHeader]}
		if err := r.publisher.Publish(ctx, m.Exchange, m.RoutingKey, m.Body, m.Headers); err != nil {
			if r.logger != nil {
				r.logger.Error("outbox: публикация записи не удалась", nil, fields, "record_id", m.ID, "failure_category", "publish")
			}
			return &publishError{err: err}
		}
		if err := r.journal.MarkPublished(ctx, m.ID); err != nil {
			if r.logger != nil {
				r.logger.Error("outbox: подтверждение записи не удалось", nil, fields, "record_id", m.ID, "failure_category", "mark_published")
			}
			return err
		}
		if r.logger != nil {
			r.logger.Info("outbox: запись опубликована", fields, "record_id", m.ID)
		}
	}
	return nil
}
