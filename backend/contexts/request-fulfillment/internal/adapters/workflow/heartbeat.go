package workflowadapter

import (
	"context"
	"sync"
	"time"

	"go.temporal.io/sdk/activity"
)

// defaultHeartbeatInterval — верхняя граница периода отправки heartbeat: при
// большом HeartbeatTimeout нет смысла «пинговать» чаще.
const defaultHeartbeatInterval = 20 * time.Second

// heartbeatIntervalDivisor — доля HeartbeatTimeout, отводимая на период отправки
// heartbeat: пропуск должен обнаруживаться заметно раньше истечения
// HeartbeatTimeout, поэтому интервал берётся втрое короче.
const heartbeatIntervalDivisor = 3

// heartbeatInterval выводит период отправки heartbeat из настроенного
// HeartbeatTimeout: доля от таймаута (втрое короче), но не чаще разумного
// потолка. Нулевой или отрицательный таймаут даёт значение по умолчанию — тогда
// таймаут в опциях activities не задан, и heartbeat не критичен.
func heartbeatInterval(heartbeatTimeout time.Duration) time.Duration {
	if heartbeatTimeout <= 0 {
		return defaultHeartbeatInterval
	}
	interval := heartbeatTimeout / heartbeatIntervalDivisor
	if interval <= 0 || interval > defaultHeartbeatInterval {
		return defaultHeartbeatInterval
	}
	return interval
}

// startHeartbeat запускает фоновую отправку heartbeat длинной activity и
// возвращает функцию остановки. Отправка идёт периодически, пока activity
// работает: пропущенный heartbeat приводит к повтору activity по политике
// повтора, а не к вечному ожиданию.
//
// Юнит-тесты вызывают activities напрямую, без activity-контекста, и
// activity.RecordHeartbeat вне activity паникует. Поэтому при не-activity
// контексте возвращается no-op, а сама отправка дополнительно защищена recover:
// heartbeat — наблюдательный сигнал, его сбой не меняет исход шага. Интервал
// срабатывает только при реальном долгом ожидании — мгновенный тестовый вызов не
// успевает выйти на тик.
func startHeartbeat(ctx context.Context, interval time.Duration) func() {
	if interval <= 0 {
		interval = defaultHeartbeatInterval
	}
	if !activity.IsActivity(ctx) {
		return func() {}
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				safeRecordHeartbeat(ctx)
			}
		}
	}()
	return func() { once.Do(func() { close(done) }) }
}

// safeRecordHeartbeat отправляет heartbeat, не роняя activity. Паника возможна,
// если контекст перестал быть activity-контекстом (гонка остановки воркера), —
// её подавляем: heartbeat не является результатом работы activity.
func safeRecordHeartbeat(ctx context.Context) {
	defer func() { _ = recover() }()
	activity.RecordHeartbeat(ctx, "heartbeat")
}
