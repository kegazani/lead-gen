package amqp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// PlatformExchange — общий topic exchange платформы, через который идут
// асинхронные события-границы. Физически брокер не разводит обмен по
// контексту-владельцу (как это делал бы вариант «exchange на контекст»): порт
// Consumer знает только имя очереди, поэтому потребитель не может привязаться к
// обмену, имени которого он не видит. Маршрутизация идёт по контракту в ключе
// <contract>.<major> — так же, как в InMemoryBroker, — а очередь
// <downstream>.<contract> привязывается к общему обмену шаблоном <contract>.*.
const PlatformExchange = "lead-gen.events"

// RabbitMQBroker — брокер поверх RabbitMQ (github.com/rabbitmq/amqp091-go),
// реализующий Publisher и Consumer.
//
// Повторяет контракт InMemoryBroker: durable-очередь на потребителя и контракт,
// ручное подтверждение, ограниченное число попыток обработки с backoff и DLQ
// очереди (<queue>.dlq) после исчерпания. Publisher confirms: Publish возвращает
// nil только после подтверждения приёма брокером.
//
// Потокобезопасность: публикация сериализуется мьютексом на одном канале;
// каждый Consume открывает собственный канал, поэтому потребители не мешают друг
// другу и каналу публикации. Автопереподключения нет: обрыв соединения — ошибка
// публикации/чтения, и вызывающий (relay outbox) повторяет публикацию, а
// потребитель перезапускается ролью.
type RabbitMQBroker struct {
	conn        *amqp.Connection
	maxAttempts int

	mu    sync.Mutex
	pubCh *amqp.Channel
}

// NewRabbitMQBroker подключается к RabbitMQ по url и объявляет общий topic
// exchange. maxAttempts — общее число доставок обработчику, включая первую
// (значение меньше единицы трактуется как одна попытка).
func NewRabbitMQBroker(url string, maxAttempts int) (*RabbitMQBroker, error) {
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("amqp: пустой адрес брокера")
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("amqp: подключение к RabbitMQ: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("amqp: канал публикации: %w", err)
	}
	if err := ch.ExchangeDeclare(PlatformExchange, "topic", true, false, false, false, nil); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("amqp: объявление exchange %q: %w", PlatformExchange, err)
	}
	// Режим подтверждений: Publish обязан дождаться ack брокера, иначе успех
	// публикации был бы ложью.
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("amqp: режим подтверждений: %w", err)
	}

	return &RabbitMQBroker{conn: conn, maxAttempts: maxAttempts, pubCh: ch}, nil
}

// Close закрывает канал публикации и соединение. После Close брокер непригоден.
func (b *RabbitMQBroker) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()

	var err error
	if b.pubCh != nil {
		if cerr := b.pubCh.Close(); cerr != nil {
			err = cerr
		}
		b.pubCh = nil
	}
	if b.conn != nil {
		if cerr := b.conn.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}
	return err
}

// Publish публикует сообщение в общий topic exchange платформы и ждёт
// подтверждения брокера.
//
// Параметр exchange (контекст-владелец) сохраняется в сигнатуре ради
// совместимости с Publisher и InMemoryBroker, но физический обмен не задаёт:
// маршрутизация идёт по контракту в routingKey (см. PlatformExchange).
func (b *RabbitMQBroker) Publish(ctx context.Context, exchange, routingKey string, body []byte, headers map[string]string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_ = exchange

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pubCh == nil {
		return errors.New("amqp: брокер закрыт")
	}
	msg := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		MessageId:    headerValue(headers, headerEventID),
		Headers:      headersTable(headers),
		Body:         body,
	}
	confirm, err := b.pubCh.PublishWithDeferredConfirmWithContext(ctx, PlatformExchange, routingKey, false, false, msg)
	if err != nil {
		return fmt.Errorf("amqp: публикация %s: %w", routingKey, err)
	}
	if confirm == nil {
		return errors.New("amqp: публикация без подтверждения (канал не в режиме confirm)")
	}
	acked, err := confirm.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("amqp: ожидание подтверждения %s: %w", routingKey, err)
	}
	if !acked {
		return fmt.Errorf("amqp: публикация %s не подтверждена брокером", routingKey)
	}
	return nil
}

// Consume читает durable-очередь queue, вызывая handler для каждого сообщения.
//
// Очередь объявляется durable и привязывается к общему обмену шаблоном
// <contract>.*, где contract — последний сегмент имени очереди. Успех handler —
// ack; ошибка — повтор с backoff, пока число доставок меньше maxAttempts, после
// исчерпания сообщение уходит в очередь <queue>.dlq и подтверждается. Отмена
// контекста завершает Consume с nil, возвращая недоставленное сообщение в
// очередь. Дополнительно объявляется очередь мёртвых писем <queue>.dlq.
func (b *RabbitMQBroker) Consume(ctx context.Context, queue string, handler Handler) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(queue) == "" {
		return errors.New("amqp: пустое имя очереди")
	}
	if handler == nil {
		return fmt.Errorf("amqp: очередь %q без обработчика", queue)
	}

	ch, err := b.conn.Channel()
	if err != nil {
		return fmt.Errorf("amqp: канал очереди %q: %w", queue, err)
	}
	defer func() { _ = ch.Close() }()

	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return fmt.Errorf("amqp: объявление очереди %q: %w", queue, err)
	}
	if _, err := ch.QueueDeclare(dlqName(queue), true, false, false, false, nil); err != nil {
		return fmt.Errorf("amqp: объявление DLQ %q: %w", dlqName(queue), err)
	}
	if err := ch.QueueBind(queue, bindingKey(queue), PlatformExchange, false, nil); err != nil {
		return fmt.Errorf("amqp: привязка очереди %q: %w", queue, err)
	}
	// По одной неподтверждённой доставке за раз: обработчик не «заваливается»
	// пачкой, а повторы укладываются в backoff, как в InMemoryBroker.
	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("amqp: QoS очереди %q: %w", queue, err)
	}

	deliveries, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("amqp: чтение очереди %q: %w", queue, err)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-deliveries:
			if !ok {
				// Канал закрыт брокером (обрыв соединения либо удаление очереди).
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("amqp: канал очереди %q закрыт брокером", queue)
			}
			if err := b.handleDelivery(ctx, queue, d, handler); err != nil {
				return err
			}
		}
	}
}

// handleDelivery обрабатывает одну доставку с повторами в пределах maxAttempts и
// уходом в DLQ после исчерпания попыток.
func (b *RabbitMQBroker) handleDelivery(ctx context.Context, queue string, d amqp.Delivery, handler Handler) error {
	delivery := deliveryFromAMQP(d)
	for attempt := 1; ; attempt++ {
		if attempt > 1 {
			delivery.Redelivered = true
		}
		if err := handler(ctx, delivery); err == nil {
			if ackErr := d.Ack(false); ackErr != nil {
				return fmt.Errorf("amqp: подтверждение доставки очереди %q: %w", queue, ackErr)
			}
			return nil
		}

		if attempt >= b.maxAttempts {
			// Попытки исчерпаны: переносим в DLQ и подтверждаем исходную доставку.
			if pubErr := b.publishToDLQ(ctx, queue, d); pubErr != nil {
				// Не смогли положить в DLQ — возвращаем в очередь, чтобы не потерять.
				if nackErr := d.Nack(false, true); nackErr != nil {
					return fmt.Errorf("amqp: возврат доставки очереди %q в очередь: %w", queue, nackErr)
				}
				return nil
			}
			if ackErr := d.Ack(false); ackErr != nil {
				return fmt.Errorf("amqp: подтверждение доставки очереди %q после DLQ: %w", queue, ackErr)
			}
			return nil
		}

		if waitErr := waitRetry(ctx, attempt); waitErr != nil {
			// Контекст отменён во время паузы: возвращаем сообщение в очередь, не
			// теряя его.
			if nackErr := d.Nack(false, true); nackErr != nil {
				return fmt.Errorf("amqp: возврат доставки очереди %q в очередь: %w", queue, nackErr)
			}
			return nil
		}
	}
}

// publishToDLQ кладёт доставку в очередь <queue>.dlq через exchange по умолчанию
// (маршрутизация по имени очереди).
func (b *RabbitMQBroker) publishToDLQ(ctx context.Context, queue string, d amqp.Delivery) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.pubCh == nil {
		return errors.New("amqp: брокер закрыт")
	}
	msg := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Timestamp:    time.Now().UTC(),
		MessageId:    d.MessageId,
		Headers:      d.Headers,
		Body:         d.Body,
	}
	return b.pubCh.PublishWithContext(ctx, "", dlqName(queue), false, false, msg)
}

// bindingKey возвращает шаблон привязки очереди к обмену: <contract>.*, где
// contract — последний сегмент имени очереди (<downstream>.<contract>). Пустой
// контракт отвергается на уровне имени очереди.
func bindingKey(queue string) string {
	contract := queue
	if i := strings.LastIndex(queue, "."); i >= 0 {
		contract = queue[i+1:]
	}
	return contract + ".*"
}

// dlqName возвращает имя очереди мёртвых писем для очереди queue.
func dlqName(queue string) string { return queue + ".dlq" }

// headersTable переводит строковые метаданные в amqp.Table.
func headersTable(headers map[string]string) amqp.Table {
	if len(headers) == 0 {
		return nil
	}
	table := make(amqp.Table, len(headers))
	for k, v := range headers {
		table[k] = v
	}
	return table
}

// deliveryFromAMQP переводит доставку AMQP в транспортную форму. Идентификатор
// события берётся из заголовка event_id, а при его отсутствии — из MessageId.
func deliveryFromAMQP(d amqp.Delivery) Delivery {
	headers := tableToStrings(d.Headers)
	eventID := headerValue(headers, headerEventID)
	if eventID == "" {
		eventID = d.MessageId
	}
	return Delivery{
		Body:        cloneBytes(d.Body),
		Headers:     headers,
		EventID:     eventID,
		Redelivered: d.Redelivered,
	}
}

// tableToStrings переводит заголовки AMQP в строковую карту контракта: значения
// представляются строкой, чтобы форма не зависела от типа, выбранного отправителем.
func tableToStrings(table amqp.Table) map[string]string {
	if len(table) == 0 {
		return nil
	}
	out := make(map[string]string, len(table))
	for k, v := range table {
		switch typed := v.(type) {
		case string:
			out[k] = typed
		case []byte:
			out[k] = string(typed)
		case nil:
			out[k] = ""
		default:
			out[k] = fmt.Sprint(typed)
		}
	}
	return out
}
