// Package reliability — технический примитив оболочки вызова порта: единый
// бюджет тайм-аута, соблюдение отмены context.Context, согласованная политика
// повторов без двойных эффектов и нормализованные метки телеметрии `port`,
// `adapter`, `operation`, `outcome` (design.md, Решение 1; задача §3.6 миграции).
//
// # Граница с доменом
//
// Пакет не знает имён контрактов, категорий ошибок и сущностей контекстов: он
// не импортирует contracts/ и backend/contexts/. Оболочка возвращает
// абстрактный исход вызова (Outcome), а в доменные категории (`integration-error`)
// его переводит адаптер и домен. Так примитив остаётся механизмом, а не shared
// domain (docs/system-map.md §2.2, docs/domain-model.md «Реализация на Go»).
//
// # Двойные эффекты
//
// Повтор разрешён только для вызова, помеченного Invocation.Idempotent:
// оболочка сама гарантирует ровно одну попытку для неидемпотентной операции,
// независимо от переданной политики. Повтор идемпотентного вызова (query или
// command со стабильным ключом идемпотентности) поэтому не создаёт второго
// внешнего эффекта.
package reliability

import (
	"context"
	"errors"
	"time"
)

// Invocation — координаты вызова порта. Служат метками телеметрии и признаком
// допустимости повтора: оболочка не разбирает значения, только переносит их.
type Invocation struct {
	// Port — имя контракта-порта.
	Port string
	// Adapter — имя адаптера, обслуживающего порт.
	Adapter string
	// Operation — операция вызова внутри порта.
	Operation string
	// Idempotent разрешает повтор вызова. Неидемпотентная операция выполняется
	// ровно один раз, чтобы повтор не создал второй внешний эффект.
	Idempotent bool
}

// Outcome — абстрактный исход вызова. Это не доменная категория: оболочка
// различает успех, отказ, тайм-аут и отмену, а нормализацию в категории
// выполняет адаптер и домен.
type Outcome string

const (
	// OutcomeSuccess — вызов завершился без ошибки.
	OutcomeSuccess Outcome = "success"
	// OutcomeFailure — вызов вернул ошибку операции.
	OutcomeFailure Outcome = "failure"
	// OutcomeTimeout — бюджет вызова исчерпан.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeCanceled — вызов отменён вызывающим.
	OutcomeCanceled Outcome = "canceled"
)

// Sink — приёмник меток телеметрии. Форма совпадает с observ.Metrics, поэтому
// реальный приёмник подставляется без адаптера, а пакет остаётся на стандартной
// библиотеке.
type Sink interface {
	// Incr увеличивает счётчик name с набором меток на единицу.
	Incr(name string, labels map[string]string)
	// Observe добавляет наблюдение v к гистограмме name с набором меток.
	Observe(name string, v float64, labels map[string]string)
}

// Retrier — политика повторов. Форма совпадает с resilience.RetryPolicy,
// поэтому согласованная с транспортом политика подставляется из точки сборки,
// а пакет не зависит от соседнего примитива.
type Retrier interface {
	// Do выполняет fn и повторяет её только при idempotent == true.
	Do(ctx context.Context, idempotent bool, fn func(context.Context) error) error
}

// Имена образцов телеметрии: счётчик вызовов и гистограмма их длительности.
const (
	metricInvocations = "port_invocations_total"
	metricDuration    = "port_invocation_duration_seconds"
)

// defaultTimeout — бюджет вызова по умолчанию. Синхронный вызов без границы
// недопустим, поэтому нулевой бюджет сборки заменяется этим значением.
const defaultTimeout = 30 * time.Second

// Shell — оболочка вызова порта. После сборки примитив не изменяется и пригоден
// для конкурентного использования.
type Shell struct {
	timeout time.Duration
	retrier Retrier
	sink    Sink
}

// Option настраивает оболочку при сборке.
type Option func(*Shell)

// WithTimeout задаёт общий бюджет вызова, включая все попытки: одна отмена или
// тайм-аут останавливает повтор. Неположительное значение оставляет бюджет по
// умолчанию.
func WithTimeout(timeout time.Duration) Option {
	return func(s *Shell) { s.timeout = timeout }
}

// WithRetrier задаёт политику повторов. Она применяется только к вызовам,
// помеченным Idempotent. Без неё повтор не выполняется.
func WithRetrier(retrier Retrier) Option {
	return func(s *Shell) { s.retrier = retrier }
}

// WithTelemetry задаёт приёмник метрик. Нулевой приёмник отключает телеметрию.
func WithTelemetry(sink Sink) Option {
	return func(s *Shell) { s.sink = sink }
}

// New собирает оболочку. Без WithTimeout действует бюджет по умолчанию, без
// WithRetrier повтор не выполняется.
func New(options ...Option) *Shell {
	s := &Shell{timeout: defaultTimeout}
	for _, option := range options {
		if option != nil {
			option(s)
		}
	}
	if s.timeout <= 0 {
		s.timeout = defaultTimeout
	}
	return s
}

// Call выполняет вызов порта и возвращает его абстрактный исход вместе с
// ошибкой операции (её нормализует вызывающий).
//
// Порядок шагов: единый бюджет, отмена, вызов, повтор только для идемпотентной
// операции, телеметрия. Бюджет общий на все попытки: истечение бюджета или
// отмена останавливают повтор.
func (s *Shell) Call(ctx context.Context, inv Invocation, op func(context.Context) error) (Outcome, error) {
	if s == nil {
		return OutcomeFailure, errors.New("reliability: оболочка не собрана")
	}
	if op == nil {
		return OutcomeFailure, errors.New("reliability: не задана операция вызова")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	callCtx := ctx
	if s.timeout > 0 {
		var cancel context.CancelFunc
		callCtx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	started := time.Now()
	var err error
	if inv.Idempotent && s.retrier != nil {
		err = s.retrier.Do(callCtx, true, op)
	} else {
		// Неидемпотентный вызов повтору не подлежит: оболочка гарантирует ровно
		// одну попытку независимо от переданной политики.
		err = op(callCtx)
	}

	outcome := classify(err)
	s.record(inv, outcome, time.Since(started))
	return outcome, err
}

// classify переводит ошибку вызова в абстрактный исход. Доменные категории
// сюда не входят: любая ошибка, кроме тайм-аута и отмены, — это отказ.
func classify(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeSuccess
	case errors.Is(err, context.DeadlineExceeded):
		return OutcomeTimeout
	case errors.Is(err, context.Canceled):
		return OutcomeCanceled
	default:
		return OutcomeFailure
	}
}

// record отправляет метки телеметрии вызова: счётчик и длительность с одним
// набором меток `port`, `adapter`, `operation`, `outcome`.
func (s *Shell) record(inv Invocation, outcome Outcome, elapsed time.Duration) {
	if s.sink == nil {
		return
	}
	labels := map[string]string{
		"port":      inv.Port,
		"adapter":   inv.Adapter,
		"operation": inv.Operation,
		"outcome":   string(outcome),
	}
	s.sink.Incr(metricInvocations, labels)
	s.sink.Observe(metricDuration, elapsed.Seconds(), labels)
}
