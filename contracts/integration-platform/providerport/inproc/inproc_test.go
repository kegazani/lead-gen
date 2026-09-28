package inproc

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/contracts/integration-platform/providerport"
)

var testInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func validRequest() providerport.Request {
	return providerport.Request{
		Envelope: providerport.Envelope{
			EventID:       "evt-1",
			OccurredAt:    testInstant,
			TenantID:      "tenant-1",
			CorrelationID: "corr-1",
			CausationID:   "cause-1",
			Name:          providerport.ContractName,
			SchemaVersion: providerport.SchemaVersion,
		},
		AdapterName:     "firecrawl",
		IntegrationType: "web-research",
		Operation:       "search",
		CommandKey:      "cmd-1",
		Payload:         json.RawMessage(`{"query":"логисты"}`),
	}
}

// recorder — реализация владельца: считает вызовы и, по желанию, блокируется,
// игнорируя контекст. Так проверяется, что границу вызова держит адаптер, а не
// только реализация.
type recorder struct {
	mu       sync.Mutex
	requests []providerport.Request
	result   providerport.Result
	err      error
	release  chan struct{}
}

func (r *recorder) Invoke(_ context.Context, request providerport.Request) (providerport.Result, error) {
	r.mu.Lock()
	r.requests = append(r.requests, request)
	r.mu.Unlock()
	if r.release != nil {
		<-r.release
	}
	return r.result, r.err
}

func (r *recorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.requests)
}

// Связывание отвергает отсутствующую реализацию владельца, а полный вызов
// доходит до реализации владельца и возвращает её результат.
func TestBindDelegatesToOwnerImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind с nil-реализацией вернул nil-ошибку")
	}

	want := providerport.Result{Operation: "search", ExternalID: "ext-1", Payload: json.RawMessage(`{"ok":true}`)}
	impl := &recorder{result: want}
	port, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	request := validRequest()
	got, err := port.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if impl.callCount() != 1 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидался 1", impl.callCount())
	}
	if impl.requests[0].IdempotencyKey() != request.IdempotencyKey() {
		t.Fatalf("реализация получила %+v, ожидалось %+v", impl.requests[0], request)
	}
	if got.Operation != want.Operation || got.ExternalID != want.ExternalID {
		t.Fatalf("результат: got %+v, want %+v", got, want)
	}
}

// Невалидный контракт отвергается адаптером и не доходит до владельца.
func TestInvokeRejectsInvalidContract(t *testing.T) {
	impl := &recorder{}
	port, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if _, err := port.Invoke(context.Background(), providerport.Request{}); err == nil {
		t.Fatal("пустой контракт не отвергнут")
	}
	if impl.callCount() != 0 {
		t.Fatalf("невалидный контракт дошёл до владельца: вызовов %d", impl.callCount())
	}
}

// Отмена по контексту прерывает вызов: адаптер возвращает ошибку контекста,
// даже если реализация владельца контекст не проверяет.
func TestInvokeInterruptedByContextCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	impl := &recorder{release: release}
	port, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	_, err = port.Invoke(ctx, validRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, context.Canceled)", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("вызов прерван через %s, ожидалось быстро", elapsed)
	}
}

// Дедлайн вызова соблюдается адаптером: блокирующаяся реализация не держит
// вызывающего дольше бюджета.
func TestInvokeInterruptedByDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	impl := &recorder{release: release}
	port, err := Bind(impl, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	if _, err = port.Invoke(context.Background(), validRequest()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, context.DeadlineExceeded)", err)
	}
}

// Повторная доставка с тем же ключом идемпотентности применяется один раз:
// второй вызов не доходит до реализации владельца и возвращает прежний результат.
func TestInvokeAppliesOnceByIDempotencyKey(t *testing.T) {
	want := providerport.Result{Operation: "search", ExternalID: "ext-1"}
	impl := &recorder{result: want}
	port, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	request := validRequest()
	first, err := port.Invoke(context.Background(), request)
	if err != nil {
		t.Fatalf("первый Invoke: %v", err)
	}
	if first.ExternalID != "ext-1" {
		t.Fatalf("результат первого вызова: %+v", first)
	}

	// Повторная доставка: другой event_id, тот же ключ (tenant, тип, операция, ключ).
	repeat := request
	repeat.EventID = "evt-2"
	second, err := port.Invoke(context.Background(), repeat)
	if err != nil {
		t.Fatalf("повторный Invoke: %v", err)
	}
	if impl.callCount() != 1 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидался 1", impl.callCount())
	}
	if second.ExternalID != first.ExternalID {
		t.Fatalf("повтор вернул %+v, ожидался результат прежнего вызова %+v", second, first)
	}

	// Новая команда — другой ключ: вызывается владелец.
	next := request
	next.EventID = "evt-3"
	next.CommandKey = "cmd-2"
	if _, err := port.Invoke(context.Background(), next); err != nil {
		t.Fatalf("Invoke новой команды: %v", err)
	}
	if impl.callCount() != 2 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидалось 2", impl.callCount())
	}
}

// Неуспешный вызов не «съедается»: повтор того же ключа после ошибки снова
// доходит до владельца.
func TestInvokeRetriesAfterFailure(t *testing.T) {
	impl := &recorder{err: errors.New("integrationerror: temporarily-unavailable")}
	port, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	request := validRequest()
	if _, err := port.Invoke(context.Background(), request); err == nil {
		t.Fatal("ожидалась ошибка владельца")
	}
	if _, err := port.Invoke(context.Background(), request); err == nil {
		t.Fatal("ожидалась ошибка владельца при повторе")
	}
	if impl.callCount() != 2 {
		t.Fatalf("владелец вызван %d раз, ожидалось 2: ошибка не должна кэшироваться", impl.callCount())
	}
}
