// Package amqp — асинхронный транспорт границы сервиса поверх RabbitMQ.
//
// Связи вида event и policy идут через topic exchange, названный по
// контексту-владельцу (например, outreach-orchestration); ключ маршрутизации —
// <contract>.<major-version>; очередь — на каждого потребителя и контракт
// (<downstream>.<contract>), durable, с ручным подтверждением (design.md,
// Решение 3). Публикация идёт после commit через transactional outbox
// (platform/messaging/outbox), поэтому в брокер попадает только уже
// зафиксированное изменение.
//
// Publisher confirms. Publish возвращает nil только после того, как брокер
// принял сообщение (эмуляция подтверждения публикации): успешный ответ означает
// «сообщение в очереди», а ошибка — «не принято, повторит relay». Так отправитель
// не считает опубликованным то, что потерялось на входе в брокер.
//
// Доставка — at-least-once. Сбой обработчика или падение между обработкой и
// подтверждением приводит к повтору в пределах maxAttempts, поэтому потребитель
// дедуплицирует сообщения по event_id во входящем inbox
// (platform/messaging/inbox): повтор не создаёт второго эффекта.
//
// Пакет не содержит доменных типов: тело сообщения — непрозрачные байты,
// метаданные — карта строк. Реальный AMQP-клиент подключается реализациями
// Publisher и Consumer; InMemoryBroker воспроизводит те же гарантии в памяти для
// тестов и совмещённого процесса без внешней сети.
package amqp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Publisher публикует сообщение в topic exchange.
//
// exchange назван по контексту-владельцу события; routingKey — <contract>.<major>.
// Тело и метаданные непрозрачны для транспорта. Возврат nil означает, что брокер
// подтвердил приём (publisher confirm), и только тогда вызывающий вправе считать
// публикацию состоявшейся.
type Publisher interface {
	Publish(ctx context.Context, exchange, routingKey string, body []byte, headers map[string]string) error
}

// Delivery — доставленное потребителю сообщение.
type Delivery struct {
	// Body — тело сообщения (например, JSON контракта).
	Body []byte
	// Headers — метаданные конверта, включая event_id.
	Headers map[string]string
	// EventID — идентификатор сообщения для дедупликации, извлечённый из Headers.
	EventID string
	// Redelivered — признак повтора: true, если это не первая доставка сообщения.
	Redelivered bool
}

// Handler обрабатывает сообщение. nil = успех (ack); ошибка = повтор в пределах
// maxAttempts, после исчерпания — уход в DLQ очереди.
//
// Handler обязан быть идемпотентным по Delivery.EventID: доставка at-least-once,
// и повтор возможен даже после частично выполненного эффекта.
type Handler func(ctx context.Context, d Delivery) error

// Consumer читает очередь с ручным подтверждением.
//
// queue — durable-очередь на потребителя и контракт (<downstream>.<contract>).
// Consume блокирует горутину до отмены контекста; nil означает штатное
// завершение (контекст отменён).
type Consumer interface {
	Consume(ctx context.Context, queue string, handler Handler) error
}

// headerEventID — ключ метаданных с идентификатором сообщения. Совпадает с
// именем поля конверта (contracts.Envelope.EventID), чтобы конверт переносился
// без переименования полей.
const headerEventID = "event_id"

// Параметры повтора по умолчанию. Задержка экспоненциальная с ограничением:
// сообщение не «бьёт» по обработчику залпом, а укладывается в разумное время
// между попытками.
const (
	retryBaseDelay = 5 * time.Millisecond
	retryMaxDelay  = 250 * time.Millisecond
)

// published — сообщение, принятое брокером. Хранится как общий журнал: каждая
// очередь читает его со своего курсора, поэтому одна публикация раскладывается по
// всем подходящим очередям (fan-out topic exchange), а публикация до регистрации
// очереди не теряется (durable-семантика в памяти).
type published struct {
	exchange   string
	routingKey string
	body       []byte
	headers    map[string]string
}

// queuedDelivery — сообщение в очереди вместе со счётчиком неудачных попыток.
type queuedDelivery struct {
	d        Delivery
	attempts int
}

// queue — состояние одной очереди: курсор чтения журнала, ожидающие доставки,
// DLQ и сигнал о новом сообщении.
type queue struct {
	name    string
	cursor  int
	pending []*queuedDelivery
	dlq     []Delivery
	running bool
	// notify буферизован на единицу: публикация будит ожидающего потребителя без
	// блокировки; лишние токены схлопываются, потеря пробуждения невозможна, так
	// как потребитель перечитывает pending перед каждой паузой.
	notify chan struct{}
}

// InMemoryBroker — брокер в памяти, реализующий Publisher и Consumer.
//
// Повторяет гарантии боевого брокера: durable-очереди (сообщение живёт до
// успешного ack), ручное подтверждение, ограниченное число попыток с backoff и
// DLQ очереди после исчерпания. Потокобезопасен: вся работа с состоянием идёт под
// одним мьютексом, обработчик вызывается без него.
type InMemoryBroker struct {
	maxAttempts int

	mu       sync.Mutex
	messages []published
	queues   map[string]*queue
}

// NewInMemoryBroker собирает брокер с ограничением числа попыток на сообщение.
//
// maxAttempts — общее число доставок, включая первую. Значение меньше единицы
// трактуется как одна попытка: сообщение доставляется ровно один раз, и при
// ошибке сразу уходит в DLQ.
func NewInMemoryBroker(maxAttempts int) *InMemoryBroker {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return &InMemoryBroker{
		maxAttempts: maxAttempts,
		queues:      make(map[string]*queue),
	}
}

// Publish принимает сообщение и раскладывает его по подходящим очередям.
//
// Возврат nil — подтверждение приёма (publisher confirm). Сообщение сохраняется
// в журнале брокера, поэтому очередь, зарегистрированная позже, всё равно получит
// его: публикация после commit не теряется при рассинхронизации старта.
func (b *InMemoryBroker) Publish(ctx context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.messages = append(b.messages, published{
		exchange:   exchange,
		routingKey: routingKey,
		body:       cloneBytes(body),
		headers:    cloneHeaders(headers),
	})
	for _, q := range b.queues {
		b.pullLocked(q)
		q.signal()
	}
	return nil
}

// Consume читает очередь queue, вызывая handler для каждого сообщения.
//
// Успех handler — ack: сообщение снимается с очереди. Ошибка — повтор с backoff,
// пока число доставок меньше maxAttempts; после исчерпания сообщение уходит в DLQ
// очереди и больше не доставляется. Отмена контекста завершает Consume с nil,
// возвращая недоставленное сообщение в очередь, чтобы его не потерять.
//
// Повторное обслуживание уже читаемой очереди — ошибка: у очереди один потребитель
// (durable-очередь на потребителя и контракт).
func (b *InMemoryBroker) Consume(ctx context.Context, queue string, handler Handler) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if handler == nil {
		return fmt.Errorf("amqp: очередь %q без обработчика", queue)
	}

	b.mu.Lock()
	q := b.queueForLocked(queue)
	if q.running {
		b.mu.Unlock()
		return fmt.Errorf("amqp: очередь %q уже обслуживается", queue)
	}
	q.running = true
	b.pullLocked(q)
	b.mu.Unlock()

	defer func() {
		b.mu.Lock()
		q.running = false
		b.mu.Unlock()
	}()

	for {
		b.mu.Lock()
		b.pullLocked(q)
		if len(q.pending) > 0 {
			current := q.pending[0]
			q.pending = q.pending[1:]
			b.mu.Unlock()

			if err := handler(ctx, current.d); err == nil {
				continue
			}

			current.attempts++
			current.d.Redelivered = true

			if current.attempts >= b.maxAttempts {
				// Попытки исчерпаны: сообщение покидает очередь в DLQ и больше не
				// доставляется, но остаётся видимым для разбора.
				b.mu.Lock()
				q.dlq = append(q.dlq, cloneDelivery(current.d))
				b.mu.Unlock()
				continue
			}

			if err := b.backoff(ctx, current.attempts); err != nil {
				// Контекст отменён во время паузы: возвращаем сообщение в очередь
				// (спереди, чтобы сохранить порядок), не теряя попытку.
				b.requeueFront(q, current)
				return nil
			}
			b.requeueFront(q, current)
			continue
		}
		b.mu.Unlock()

		select {
		case <-ctx.Done():
			return nil
		case <-q.notify:
		}
	}
}

// DLQ возвращает копию очереди мёртвых писем (сообщения, исчерпавшие попытки).
func (b *InMemoryBroker) DLQ(queue string) []Delivery {
	b.mu.Lock()
	defer b.mu.Unlock()

	q := b.queueForLocked(queue)
	out := make([]Delivery, len(q.dlq))
	for i, d := range q.dlq {
		out[i] = cloneDelivery(d)
	}
	return out
}

// Pending возвращает число сообщений, ожидающих доставки в очереди. Метод
// отражает durable-семантику: опубликованное сообщение видно как ожидающее до
// первого успешного ack.
func (b *InMemoryBroker) Pending(queue string) int {
	b.mu.Lock()
	defer b.mu.Unlock()

	q := b.queueForLocked(queue)
	b.pullLocked(q)
	return len(q.pending)
}

// queueForLocked возвращает состояние очереди, создавая его при первом обращении.
// Мьютекс должен быть удержан вызывающим.
func (b *InMemoryBroker) queueForLocked(name string) *queue {
	q, ok := b.queues[name]
	if !ok {
		q = &queue{name: name, notify: make(chan struct{}, 1)}
		b.queues[name] = q
	}
	return q
}

// pullLocked переносит из журнала в очередь все подходящие ещё не прочитанные
// сообщения. Курсор двигается по всем записям, поэтому каждая очередь читает
// журнал один раз, а неподходящие сообщения пропускает. Мьютекс должен быть
// удержан вызывающим.
func (b *InMemoryBroker) pullLocked(q *queue) {
	for ; q.cursor < len(b.messages); q.cursor++ {
		m := b.messages[q.cursor]
		if !b.routes(q.name, m.exchange, m.routingKey) {
			continue
		}
		q.pending = append(q.pending, &queuedDelivery{d: Delivery{
			Body:    cloneBytes(m.body),
			Headers: cloneHeaders(m.headers),
			EventID: headerValue(m.headers, headerEventID),
		}})
	}
}

// routes определяет, доставляется ли сообщение (exchange, routingKey) в очередь
// queue. Реальный topic exchange связывает очередь с exchange по шаблону
// <contract>.*; очередь названа <downstream>.<contract>, поэтому договор —
// последний сегмент имени очереди. Дополнительно очередь принимает сообщения,
// выпущенные напрямую в неё (совпадение с routingKey) и весь свой exchange
// (совпадение с exchange) — этого достаточно для сборки и тестов.
func (b *InMemoryBroker) routes(queueName, exchange, routingKey string) bool {
	if queueName == routingKey || queueName == exchange {
		return true
	}
	contract := queueName
	if i := strings.LastIndex(queueName, "."); i >= 0 {
		contract = queueName[i+1:]
	}
	if contract == "" {
		return false
	}
	return strings.HasPrefix(routingKey, contract+".")
}

// backoff ждёт перед повтором номер attempt (1 — первый повтор), прерываясь по
// отмене контекста.
func (b *InMemoryBroker) backoff(ctx context.Context, attempt int) error {
	delay := retryBaseDelay
	for i := 1; i < attempt; i++ {
		if delay >= retryMaxDelay {
			break
		}
		delay *= 2
	}
	if delay > retryMaxDelay {
		delay = retryMaxDelay
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// requeueFront возвращает сообщение в начало очереди под мьютексом.
func (b *InMemoryBroker) requeueFront(q *queue, d *queuedDelivery) {
	b.mu.Lock()
	defer b.mu.Unlock()
	q.pending = append([]*queuedDelivery{d}, q.pending...)
}

// signal будит ожидающего потребителя, не блокируясь.
func (q *queue) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// headerValue возвращает значение заголовка или пустую строку.
func headerValue(headers map[string]string, key string) string {
	if headers == nil {
		return ""
	}
	return headers[key]
}

// cloneDelivery копирует доставку, чтобы состояние брокера не делило буферы с
// вызывающим.
func cloneDelivery(d Delivery) Delivery {
	return Delivery{
		Body:        cloneBytes(d.Body),
		Headers:     cloneHeaders(d.Headers),
		EventID:     d.EventID,
		Redelivered: d.Redelivered,
	}
}

// cloneBytes копирует срез байт, сохраняя nil.
func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// cloneHeaders копирует карту заголовков, сохраняя nil.
func cloneHeaders(h map[string]string) map[string]string {
	if h == nil {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}
