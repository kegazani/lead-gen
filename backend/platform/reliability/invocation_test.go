package reliability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/platform/reliability/resilience"
)

// sample — зафиксированный образец телеметрии.
type sample struct {
	name   string
	labels map[string]string
}

// fakeSink — приёмник метрик для проверки меток оболочки.
type fakeSink struct {
	mu        sync.Mutex
	counters  []sample
	durations []sample
}

func (f *fakeSink) Incr(name string, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counters = append(f.counters, sample{name: name, labels: cloneLabels(labels)})
}

func (f *fakeSink) Observe(name string, v float64, labels map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.durations = append(f.durations, sample{name: name, labels: cloneLabels(labels)})
}

// counter возвращает единственный счётчик с именем name.
func (f *fakeSink) counter(name string) (sample, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.counters {
		if s.name == name {
			return s, true
		}
	}
	return sample{}, false
}

func (f *fakeSink) durationCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, s := range f.durations {
		if s.name == name {
			n++
		}
	}
	return n
}

func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// alwaysRetry — политика, которая повторяет вызов при любой ошибке, игнорируя
// признак идемпотентности. Нужна, чтобы доказать: оболочка сама, а не политика,
// защищает неидемпотентный вызов от повтора.
type alwaysRetry struct{ attempts int }

func (p alwaysRetry) Do(ctx context.Context, _ bool, fn func(context.Context) error) error {
	var last error
	for i := 0; i < p.attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if last = fn(ctx); last == nil {
			return nil
		}
	}
	return last
}

func TestCallSuccessReportsOutcomeAndLabels(t *testing.T) {
	sink := &fakeSink{}
	shell := New(WithTelemetry(sink))

	inv := Invocation{Port: "provider-port", Adapter: "fake", Operation: "enrich"}
	outcome, err := shell.Call(context.Background(), inv, func(context.Context) error { return nil })
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if outcome != OutcomeSuccess {
		t.Fatalf("исход = %q, ожидался %q", outcome, OutcomeSuccess)
	}

	got, ok := sink.counter(metricInvocations)
	if !ok {
		t.Fatal("счётчик вызовов не записан")
	}
	want := map[string]string{
		"port":      "provider-port",
		"adapter":   "fake",
		"operation": "enrich",
		"outcome":   "success",
	}
	if !labelsEqual(got.labels, want) {
		t.Fatalf("метки = %v, ожидались %v", got.labels, want)
	}
	if n := sink.durationCount(metricDuration); n != 1 {
		t.Fatalf("наблюдений длительности = %d, ожидалось 1", n)
	}
	if got, ok := sink.counter(metricInvocations); ok && len(got.labels) != 4 {
		t.Fatalf("метки = %v, ожидалось ровно четыре метки", got.labels)
	}
}

func TestCallRetriesIdempotentUntilSuccess(t *testing.T) {
	shell := New(
		WithTimeout(time.Second),
		WithRetrier(resilience.RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}),
	)
	calls := 0
	outcome, err := shell.Call(context.Background(), Invocation{Idempotent: true}, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("временный отказ")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if outcome != OutcomeSuccess {
		t.Fatalf("исход = %q, ожидался %q", outcome, OutcomeSuccess)
	}
	if calls != 3 {
		t.Fatalf("попыток = %d, ожидалось 3", calls)
	}
}

func TestCallDoesNotRepeatNonIdempotent(t *testing.T) {
	sink := &fakeSink{}
	shell := New(
		WithRetrier(alwaysRetry{attempts: 5}),
		WithTelemetry(sink),
	)
	want := errors.New("отказ")
	calls := 0
	outcome, err := shell.Call(context.Background(), Invocation{Idempotent: false}, func(context.Context) error {
		calls++
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, want)
	}
	if calls != 1 {
		t.Fatalf("неидемпотентный вызов повторён %d раз: внешний эффект задублирован", calls)
	}
	if outcome != OutcomeFailure {
		t.Fatalf("исход = %q, ожидался %q", outcome, OutcomeFailure)
	}
	got, _ := sink.counter(metricInvocations)
	if got.labels["outcome"] != "failure" {
		t.Fatalf("метка outcome = %q, ожидалась failure", got.labels["outcome"])
	}
}

func TestCallRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	shell := New(
		WithTimeout(time.Second),
		WithRetrier(resilience.RetryPolicy{MaxAttempts: 5, BaseDelay: time.Hour}),
	)
	calls := 0
	outcome, err := shell.Call(ctx, Invocation{Idempotent: true}, func(callCtx context.Context) error {
		calls++
		cancel()
		return callCtx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ожидался context.Canceled", err)
	}
	if outcome != OutcomeCanceled {
		t.Fatalf("исход = %q, ожидался %q", outcome, OutcomeCanceled)
	}
	if calls != 1 {
		t.Fatalf("после отмены попыток больше не должно быть: %d", calls)
	}
}

func TestCallReportsTimeout(t *testing.T) {
	sink := &fakeSink{}
	shell := New(
		WithTimeout(20*time.Millisecond),
		WithRetrier(resilience.RetryPolicy{MaxAttempts: 5, BaseDelay: time.Millisecond}),
		WithTelemetry(sink),
	)
	outcome, err := shell.Call(context.Background(), Invocation{Idempotent: true}, func(callCtx context.Context) error {
		<-callCtx.Done()
		return callCtx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка = %v, ожидался context.DeadlineExceeded", err)
	}
	if outcome != OutcomeTimeout {
		t.Fatalf("исход = %q, ожидался %q", outcome, OutcomeTimeout)
	}
	got, _ := sink.counter(metricInvocations)
	if got.labels["outcome"] != "timeout" {
		t.Fatalf("метка outcome = %q, ожидалась timeout", got.labels["outcome"])
	}
}

func TestCallNilTelemetryStillWorks(t *testing.T) {
	shell := New()
	outcome, err := shell.Call(context.Background(), Invocation{Idempotent: true}, func(context.Context) error { return nil })
	if err != nil || outcome != OutcomeSuccess {
		t.Fatalf("Call = (%q, %v), ожидалось (%q, nil)", outcome, err, OutcomeSuccess)
	}
}

func TestCallWithoutRetrierSingleAttempt(t *testing.T) {
	calls := 0
	shell := New()
	_, err := shell.Call(context.Background(), Invocation{Idempotent: true}, func(context.Context) error {
		calls++
		return errors.New("отказ")
	})
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if calls != 1 {
		t.Fatalf("попыток = %d, ожидалась 1 без политики повторов", calls)
	}
}

func labelsEqual(got, want map[string]string) bool {
	if len(got) != len(want) {
		return false
	}
	for k, v := range want {
		if got[k] != v {
			return false
		}
	}
	return true
}
