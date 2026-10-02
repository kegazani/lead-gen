package amqp

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestBindingKeyAndDLQName(t *testing.T) {
	// Контракт — последний сегмент имени очереди <downstream>.<contract>.
	if got, want := bindingKey("analytics.analytics-event"), "analytics-event.*"; got != want {
		t.Fatalf("bindingKey = %q, ожидалось %q", got, want)
	}
	// Имя без точки трактуется как сам контракт.
	if got, want := bindingKey("approved-action"), "approved-action.*"; got != want {
		t.Fatalf("bindingKey = %q, ожидалось %q", got, want)
	}
	if got, want := dlqName("analytics.analytics-event"), "analytics.analytics-event.dlq"; got != want {
		t.Fatalf("dlqName = %q, ожидалось %q", got, want)
	}
}

func TestHeadersTableAndTableToStringsRoundTrip(t *testing.T) {
	table := headersTable(map[string]string{"event_id": "e1", "contract": "analytics-event"})
	if got := table["event_id"]; got != "e1" {
		t.Fatalf("таблица заголовков = %+v", table)
	}
	if headersTable(nil) != nil {
		t.Fatal("пустые заголовки должны давать nil-таблицу")
	}

	back := tableToStrings(amqp.Table{
		"event_id": "e1",
		"blob":     []byte("bytes"),
		"count":    42,
		"nothing":  nil,
	})
	if back["event_id"] != "e1" || back["blob"] != "bytes" || back["count"] != "42" || back["nothing"] != "" {
		t.Fatalf("tableToStrings = %+v", back)
	}
	if tableToStrings(nil) != nil {
		t.Fatal("пустая таблица должна давать nil-карту")
	}
}

func TestDeliveryFromAMQPUsesHeaderThenMessageID(t *testing.T) {
	d := deliveryFromAMQP(amqp.Delivery{
		Body:        []byte("payload"),
		MessageId:   "msg-1",
		Headers:     amqp.Table{"event_id": "e1"},
		Redelivered: true,
	})
	if string(d.Body) != "payload" || d.EventID != "e1" || !d.Redelivered {
		t.Fatalf("доставка = %+v", d)
	}

	// Без заголовка event_id идентификатор берётся из MessageId.
	d = deliveryFromAMQP(amqp.Delivery{MessageId: "msg-2"})
	if d.EventID != "msg-2" {
		t.Fatalf("EventID = %q, ожидалось msg-2", d.EventID)
	}
}

// testBrokerURL возвращает адрес брокера из окружения либо пропускает тест:
// интеграционные проверки требуют поднятого RabbitMQ.
func testBrokerURL(t *testing.T) string {
	t.Helper()
	url := os.Getenv("RABBITMQ_URL")
	if url == "" {
		url = os.Getenv("AMQP_URL")
	}
	if url == "" {
		t.Skip("RabbitMQ недоступен: задайте RABBITMQ_URL для интеграционного теста")
	}
	return url
}

func TestRabbitMQBrokerRoundTrip(t *testing.T) {
	url := testBrokerURL(t)
	broker, err := NewRabbitMQBroker(url, 3)
	if err != nil {
		t.Fatalf("NewRabbitMQBroker: %v", err)
	}
	defer broker.Close()

	queue := "amqptest." + uniqueContract("roundtrip")
	routingKey := contractOf(queue) + ".1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan Delivery, 1)
	done := make(chan error, 1)
	go func() {
		done <- broker.Consume(ctx, queue, func(_ context.Context, d Delivery) error {
			received <- d
			return nil
		})
	}()
	waitReady(t)

	if err := broker.Publish(ctx, "lead-management", routingKey, []byte(`{"kind":"message-sent"}`), map[string]string{"event_id": "evt-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case d := <-received:
		if string(d.Body) != `{"kind":"message-sent"}` || d.EventID != "evt-1" || d.Redelivered {
			t.Fatalf("доставка = %+v", d)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("сообщение не доставлено")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Consume не завершился после отмены контекста")
	}
	cleanupQueue(t, url, queue)
}

func TestRabbitMQBrokerMovesToDLQAfterAttemptsExhausted(t *testing.T) {
	url := testBrokerURL(t)
	broker, err := NewRabbitMQBroker(url, 2)
	if err != nil {
		t.Fatalf("NewRabbitMQBroker: %v", err)
	}
	defer broker.Close()

	queue := "amqptest." + uniqueContract("dlq")
	routingKey := contractOf(queue) + ".1"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var attempts int
	done := make(chan error, 1)
	go func() {
		done <- broker.Consume(ctx, queue, func(_ context.Context, _ Delivery) error {
			attempts++
			return errors.New("постоянный сбой")
		})
	}()
	waitReady(t)

	if err := broker.Publish(ctx, "lead-management", routingKey, []byte("poison"), map[string]string{"event_id": "evt-dlq"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	// Ждём, пока сообщение окажется в DLQ.
	dlq := readDLQ(t, url, dlqName(queue))
	if string(dlq.Body) != "poison" {
		t.Fatalf("тело DLQ = %q", dlq.Body)
	}
	if attempts != 2 {
		t.Fatalf("доставок обработчику = %d, ожидалось 2 (maxAttempts)", attempts)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Consume: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Consume не завершился после отмены контекста")
	}
	cleanupQueue(t, url, queue)
}

// waitReady даёт потребителю зарегистрироваться до публикации.
func waitReady(t *testing.T) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
}

// readDLQ читает одно сообщение из очереди мёртвых писем, ожидая его появления.
func readDLQ(t *testing.T, url, queue string) amqp.Delivery {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatalf("Channel: %v", err)
	}
	defer ch.Close()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		d, ok, err := ch.Get(queue, false)
		if err != nil {
			t.Fatalf("Get %s: %v", queue, err)
		}
		if ok {
			_ = d.Ack(false)
			return d
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("сообщение не появилось в DLQ %s", queue)
	return amqp.Delivery{}
}

// cleanupQueue удаляет тестовые очереди после проверки.
func cleanupQueue(t *testing.T, url, queue string) {
	t.Helper()
	conn, err := amqp.Dial(url)
	if err != nil {
		return
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return
	}
	defer ch.Close()
	_, _ = ch.QueueDelete(queue, false, false, false)
	_, _ = ch.QueueDelete(dlqName(queue), false, false, false)
}

// uniqueContract возвращает имя контракта, уникальное для прогона и состоящее из
// одного сегмента (без точек): имя очереди <downstream>.<contract> разбирается по
// последней точке, и контракт не должен содержать точек сам.
func uniqueContract(prefix string) string {
	return prefix + strconv.FormatInt(time.Now().UnixNano(), 10)
}

// contractOf возвращает последний сегмент имени очереди — имя контракта.
func contractOf(queue string) string {
	if i := strings.LastIndex(queue, "."); i >= 0 {
		return queue[i+1:]
	}
	return queue
}
