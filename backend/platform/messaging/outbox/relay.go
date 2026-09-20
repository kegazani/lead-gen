package outbox

import (
	"context"
	"time"
)

// relayBatch — размер порции записей за проход. Ограничение не даёт одному
// проходу занять журнал надолго и позволяет уложиться в бюджет при большом
// накоплении.
const relayBatch = 100

// defaultInterval — интервал проходов, если задано некорректное значение.
const defaultInterval = time.Second

// Relay публикует накопленные записи journal в publisher после commit.
type Relay struct {
	journal   Journal
	publisher Publisher
	interval  time.Duration
}

// NewRelay собирает relay. Некорректный интервал (<= 0) заменяется значением по
// умолчанию; первый проход выполняется сразу при запуске, не дожидаясь интервала.
func NewRelay(j Journal, p Publisher, interval time.Duration) *Relay {
	if interval <= 0 {
		interval = defaultInterval
	}
	return &Relay{journal: j, publisher: p, interval: interval}
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
	_ = r.publishPending(ctx)

	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			_ = r.publishPending(ctx)
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
// event_id (at-least-once).
func (r *Relay) publishPending(ctx context.Context) error {
	pending, err := r.journal.Pending(ctx, relayBatch)
	if err != nil {
		return err
	}
	for _, m := range pending {
		if err := r.publisher.Publish(ctx, m.Exchange, m.RoutingKey, m.Body, m.Headers); err != nil {
			return err
		}
		if err := r.journal.MarkPublished(ctx, m.ID); err != nil {
			return err
		}
	}
	return nil
}
