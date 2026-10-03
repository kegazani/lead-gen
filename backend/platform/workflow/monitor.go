package workflow

import (
	"context"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

// Значения фонового монитора доступности рантайма по умолчанию.
const (
	// DefaultHealthInterval — период проверки доступности рантайма. Достаточно
	// редкий, чтобы не нагружать сервер, и достаточно частый, чтобы проба
	// готовности отражала сбой в обозримый срок.
	DefaultHealthInterval = 10 * time.Second

	// DefaultHealthTimeout — предельное время одной проверки: недоступный сервер
	// не должен подвешивать монитор.
	DefaultHealthTimeout = 5 * time.Second
)

// HealthChecker — источник проверки доступности рантайма. Реализуется клиентом
// Temporal (CheckHealth): проверяется фактическое соединение, а не признак,
// выставленный один раз при старте.
type HealthChecker interface {
	CheckHealth(ctx context.Context) error
}

// AvailabilityMonitor периодически проверяет доступность рантайма и переключает
// признак готовности: сбой проверки — неготовность, восстановление — снова
// готовность. Монитор живёт ровно столько, сколько живёт роль, владеющая
// клиентом: Run возвращается по отмене контекста.
//
// Ошибка отдельной проверки не завершает монитор: иначе транзиентный сбой
// навсегда лишил бы пробу готовности наблюдаемости, а восстановление рантайма
// осталось бы незамеченным.
type AvailabilityMonitor struct {
	// Checker — источник проверки доступности. Обязателен.
	Checker HealthChecker

	// Availability — переключаемый признак готовности. nil — монитор ничего не
	// делает (watchdog без цели не нужен).
	Availability *Availability

	// Interval — период проверки. Ноль — DefaultHealthInterval.
	Interval time.Duration

	// Timeout — предел одной проверки. Ноль — DefaultHealthTimeout.
	Timeout time.Duration

	// Logger — логгер сервиса. nil — без логирования переходов готовности.
	Logger observ.Logger
}

// Run запускает монитор до отмены контекста. Проверка выполняется сразу при
// старте: проба готовности не должна ждать первого тика. Возвращает nil по
// отмене контекста — недоступность рантайма это неготовность, а не отказ роли.
func (m *AvailabilityMonitor) Run(ctx context.Context) error {
	if m == nil || m.Checker == nil || m.Availability == nil {
		return nil
	}
	interval := m.Interval
	if interval <= 0 {
		interval = DefaultHealthInterval
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = DefaultHealthTimeout
	}

	monitored := m.Availability.Ready()
	m.probe(ctx, timeout, &monitored)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			m.probe(ctx, timeout, &monitored)
		}
	}
}

// probe выполняет одну проверку и переключает признак готовности при смене
// исхода. Готовность меняется только на границе перехода: лишние записи в
// атомарный признак и логи не нужны.
func (m *AvailabilityMonitor) probe(ctx context.Context, timeout time.Duration, monitored *bool) {
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	err := m.Checker.CheckHealth(checkCtx)
	ready := err == nil
	if ready == *monitored {
		return
	}
	*monitored = ready
	if ready {
		m.Availability.MarkReady()
		if m.Logger != nil {
			m.Logger.Info("workflow: рантайм исполнения доступен", observ.Fields{})
		}
		return
	}
	m.Availability.MarkNotReady()
	if m.Logger != nil {
		m.Logger.Warn("workflow: рантайм исполнения недоступен", observ.Fields{}, "error", errText(err))
	}
}

// errText безопасно печатает ошибку проверки: nil-ошибка сюда не доходит.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
