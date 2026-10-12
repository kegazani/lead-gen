package workflowadapter

import (
	"testing"
	"time"
)

// TestActivityOptionsSeparateShortAndLong проверяет чистую сборку опций: короткие
// переходы получают малый таймаут без heartbeat, длинные шаги — таймаут в минутах
// и HeartbeatTimeout. Проверка не требует живого сервера.
func TestActivityOptionsSeparateShortAndLong(t *testing.T) {
	policy := ExecutionPolicy{}.withDefaults()
	transition, long := activityOptions(policy)

	if transition.StartToCloseTimeout != policy.TransitionStartToClose {
		t.Fatalf("StartToCloseTimeout короткого перехода = %v, ожидался %v",
			transition.StartToCloseTimeout, policy.TransitionStartToClose)
	}
	if transition.HeartbeatTimeout != 0 {
		t.Fatalf("короткий переход не должен задавать heartbeat, получен %v", transition.HeartbeatTimeout)
	}
	if long.StartToCloseTimeout != policy.LongStartToClose {
		t.Fatalf("StartToCloseTimeout длинной activity = %v, ожидался %v",
			long.StartToCloseTimeout, policy.LongStartToClose)
	}
	if long.HeartbeatTimeout != policy.LongHeartbeat {
		t.Fatalf("HeartbeatTimeout длинной activity = %v, ожидался %v", long.HeartbeatTimeout, policy.LongHeartbeat)
	}
	if long.StartToCloseTimeout <= transition.StartToCloseTimeout {
		t.Fatalf("таймаут длинной activity %v не больше короткой %v",
			long.StartToCloseTimeout, transition.StartToCloseTimeout)
	}
	if transition.RetryPolicy == nil || transition.RetryPolicy.MaximumAttempts != int32(policy.TransitionMaxAttempts) {
		t.Fatalf("политика повтора коротких переходов = %+v", transition.RetryPolicy)
	}
	if long.RetryPolicy == nil || long.RetryPolicy.MaximumAttempts != int32(policy.LongMaxAttempts) {
		t.Fatalf("политика повтора длинных activities = %+v", long.RetryPolicy)
	}
}

// TestLegacyCompensationOptsRemainUnbounded подтверждает, что уже записанные v1
// histories сохраняют прежние activity options.
func TestLegacyCompensationOptsRemainUnbounded(t *testing.T) {
	policy := ExecutionPolicy{}.withDefaults()
	opts := compensationOpts(policy, false)

	if opts.StartToCloseTimeout != policy.TransitionStartToClose {
		t.Fatalf("StartToCloseTimeout = %v, ожидался %v", opts.StartToCloseTimeout, policy.TransitionStartToClose)
	}
	if opts.RetryPolicy == nil {
		t.Fatal("компенсация обязана задавать RetryPolicy")
	}
	if opts.RetryPolicy.MaximumAttempts != 0 {
		t.Fatalf("MaximumAttempts = %d, ожидался 0 (повтор до успеха)", opts.RetryPolicy.MaximumAttempts)
	}
	if opts.RetryPolicy.InitialInterval <= 0 || opts.RetryPolicy.MaximumInterval <= 0 {
		t.Fatalf("интервалы повтора не заданы: %+v", opts.RetryPolicy)
	}
}

func TestNewCompensationOptsBoundRetriesAndScheduleToClose(t *testing.T) {
	policy := ExecutionPolicy{}.withDefaults()
	opts := compensationOpts(policy, true)
	if opts.RetryPolicy == nil || opts.RetryPolicy.MaximumAttempts != int32(policy.CompensationMaxAttempts) {
		t.Fatalf("MaximumAttempts = %+v, ожидалось %d", opts.RetryPolicy, policy.CompensationMaxAttempts)
	}
	if opts.ScheduleToCloseTimeout != policy.CompensationScheduleToClose {
		t.Fatalf("ScheduleToCloseTimeout = %v, ожидалось %v", opts.ScheduleToCloseTimeout, policy.CompensationScheduleToClose)
	}
}

// настроенным HeartbeatTimeout: при коротком таймауте интервал сокращается, чтобы
// heartbeat не опаздывал, а сверх разумного потолка не растёт.
func TestHeartbeatIntervalFollowsTimeout(t *testing.T) {
	if got := heartbeatInterval(0); got != defaultHeartbeatInterval {
		t.Fatalf("интервал без таймаута = %v, ожидался %v", got, defaultHeartbeatInterval)
	}
	if got := heartbeatInterval(time.Minute); got != defaultHeartbeatInterval {
		t.Fatalf("интервал при таймауте 1 мин = %v, ожидался потолок %v", got, defaultHeartbeatInterval)
	}
	if got := heartbeatInterval(15 * time.Second); got != 5*time.Second {
		t.Fatalf("интервал при таймауте 15 с = %v, ожидался 5s", got)
	}
	if got := heartbeatInterval(9 * time.Second); got != 3*time.Second {
		t.Fatalf("интервал при таймауте 9 с = %v, ожидался 3s", got)
	}
	// Интервал обязан быть строго меньше таймаута — иначе heartbeat опаздывает.
	for _, timeout := range []time.Duration{time.Second, 5 * time.Second, 10 * time.Second, 15 * time.Second, 19 * time.Second} {
		if interval := heartbeatInterval(timeout); interval >= timeout {
			t.Fatalf("интервал %v не меньше HeartbeatTimeout %v", interval, timeout)
		}
	}
}
