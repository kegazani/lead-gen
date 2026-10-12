// Package readiness предоставляет общий технический механизм оценки готовности.
// Пакет не знает о bounded contexts и конкретных зависимостях.
package readiness

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Status — конечное состояние проверки или профиля.
type Status string

const (
	StatusUp       Status = "up"
	StatusDown     Status = "down"
	StatusDegraded Status = "degraded"
	StatusUnknown  Status = "unknown"
)

// ReasonCode содержит только безопасные коды, пригодные для внешнего отображения.
type ReasonCode string

const (
	ReasonNone               ReasonCode = "none"
	ReasonProbeNotConfigured ReasonCode = "probe_not_configured"
	ReasonProbeFailed        ReasonCode = "probe_failed"
	ReasonProbeTimeout       ReasonCode = "probe_timeout"
	ReasonProbeCanceled      ReasonCode = "probe_canceled"
	ReasonInvalidResult      ReasonCode = "invalid_probe_result"
	ReasonCheckMissing       ReasonCode = "check_missing"
	ReasonNoChecks           ReasonCode = "no_checks"
	ReasonProbeCapacity      ReasonCode = "probe_capacity"
)

// ProbeResult — ответ probe. Reason игнорируется, если его нет в закрытом списке.
type ProbeResult struct {
	Status Status
	Reason ReasonCode
}

// Probe получает evaluator-owned timeout context без caller values. Probe
// обязана кооперативно завершаться после его отмены. Go не позволяет evaluator
// принудительно остановить probe, игнорирующую отмену; её слот остаётся занят.
type Probe func(context.Context) (ProbeResult, error)

// CheckSpec объявляет check в профиле. Key — стабильный глобальный ключ
// зависимости для cache/coalescing; tenant/user/request-specific значения запрещены.
type CheckSpec struct {
	ID       string
	Key      string
	Required bool
	Probe    Probe
}

// Profile задаёт явный набор проверок одной capability. Пустой профиль не готов.
type Profile struct {
	Capability string
	Checks     []CheckSpec
}

// Check — снимок результата одной проверки.
type Check struct {
	ID         string
	Status     Status
	Reason     ReasonCode
	ObservedAt time.Time
	Required   bool
}

// Snapshot — независимая от evaluator копия оценки capability.
type Snapshot struct {
	Capability string
	Status     Status
	Ready      bool
	Checks     []Check
}

// Config ограничивает время вызова и срок жизни результата.
type Config struct {
	Timeout       time.Duration
	TTL           time.Duration
	CacheCapacity int
	MaxInflight   int
	Now           func() time.Time
}

// Evaluator исполняет probes, совместно используя результаты с одинаковым ключом.
type Evaluator struct {
	timeout       time.Duration
	ttl           time.Duration
	cacheCapacity int
	maxInflight   int
	now           func() time.Time

	mu            sync.Mutex
	cache         map[string]cacheEntry
	flights       map[string]*flight
	cacheSequence uint64
}

type cacheEntry struct {
	result   ProbeResult
	observed time.Time
	expires  time.Time
	used     uint64
}

type flight struct {
	done     chan struct{}
	result   ProbeResult
	observed time.Time
	deadline time.Time
}

// New создаёт evaluator. Timeout обязан быть положительным; нулевой TTL отключает кэш.
func New(config Config) *Evaluator {
	now := config.Now
	if now == nil {
		now = time.Now
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = time.Second
	}
	ttl := config.TTL
	if ttl < 0 {
		ttl = 0
	}
	cacheCapacity := config.CacheCapacity
	if cacheCapacity <= 0 {
		cacheCapacity = 256
	}
	maxInflight := config.MaxInflight
	if maxInflight <= 0 {
		maxInflight = 32
	}
	return &Evaluator{
		timeout:       timeout,
		ttl:           ttl,
		cacheCapacity: cacheCapacity,
		maxInflight:   maxInflight,
		now:           now,
		cache:         make(map[string]cacheEntry),
		flights:       make(map[string]*flight),
	}
}

// Evaluate вычисляет отдельный неизменяемый снимок для профиля.
func (e *Evaluator) Evaluate(ctx context.Context, profile Profile) Snapshot {
	snapshot := Snapshot{Capability: profile.Capability, Status: StatusUp, Ready: true}
	if len(profile.Checks) == 0 {
		return Snapshot{Capability: profile.Capability, Status: StatusUnknown, Ready: false}
	}
	seen := make(map[string]struct{}, len(profile.Checks))
	for _, spec := range profile.Checks {
		if spec.ID == "" {
			spec.ID = spec.Key
		}
		if spec.Key == "" {
			spec.Key = spec.ID
		}
		check := Check{ID: spec.ID, Required: spec.Required, ObservedAt: e.now()}
		if _, duplicate := seen[spec.ID]; duplicate || spec.ID == "" || spec.Key == "" {
			check.Status, check.Reason = StatusUnknown, ReasonCheckMissing
		} else {
			seen[spec.ID] = struct{}{}
			check.Status, check.Reason, check.ObservedAt = e.check(ctx, spec)
		}
		snapshot.Checks = append(snapshot.Checks, check)
		if spec.Required {
			switch check.Status {
			case StatusDown:
				snapshot.Status = StatusDown
				snapshot.Ready = false
			case StatusUnknown:
				if snapshot.Status != StatusDown {
					snapshot.Status = StatusUnknown
				}
				snapshot.Ready = false
			case StatusDegraded:
				if snapshot.Status == StatusUp {
					snapshot.Status = StatusDegraded
				}
				snapshot.Ready = false
			}
		} else if check.Status != StatusUp {
			if snapshot.Status == StatusUp {
				snapshot.Status = StatusDegraded
			}
		}
	}
	return cloneSnapshot(snapshot)
}

func (e *Evaluator) check(caller context.Context, spec CheckSpec) (Status, ReasonCode, time.Time) {
	if spec.Probe == nil {
		return StatusUnknown, ReasonProbeNotConfigured, e.now()
	}
	if err := caller.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return StatusUnknown, ReasonProbeTimeout, e.now()
		}
		return StatusUnknown, ReasonProbeCanceled, e.now()
	}
	result, observed := e.execute(caller, spec.Key, spec.Probe)
	if !validStatus(result.Status) {
		return StatusUnknown, ReasonInvalidResult, observed
	}
	if result.Reason == "" {
		if result.Status == StatusUp {
			result.Reason = ReasonNone
		} else {
			result.Reason = ReasonInvalidResult
		}
	} else if !validReason(result.Reason) {
		result.Reason = ReasonInvalidResult
	}
	return result.Status, result.Reason, observed
}

func (e *Evaluator) execute(caller context.Context, key string, probe Probe) (ProbeResult, time.Time) {
	e.mu.Lock()
	now := e.now()
	if entry, ok := e.cache[key]; ok {
		if e.ttl > 0 && now.Before(entry.expires) {
			e.cacheSequence++
			entry.used = e.cacheSequence
			e.cache[key] = entry
			e.mu.Unlock()
			return entry.result, entry.observed
		}
		delete(e.cache, key)
	}
	if running, ok := e.flights[key]; ok {
		e.mu.Unlock()
		return e.await(caller, running)
	}
	if len(e.flights) >= e.maxInflight {
		e.mu.Unlock()
		return ProbeResult{Status: StatusUnknown, Reason: ReasonProbeCapacity}, e.now()
	}
	running := &flight{done: make(chan struct{}), deadline: time.Now().Add(e.timeout)}
	e.flights[key] = running
	e.mu.Unlock()

	probeCtx, cancel := context.WithTimeout(context.Background(), e.timeout)
	go func() {
		defer cancel()
		result, err := probe(probeCtx)
		switch {
		case errors.Is(probeCtx.Err(), context.DeadlineExceeded):
			result = ProbeResult{Status: StatusUnknown, Reason: ReasonProbeTimeout}
		case errors.Is(probeCtx.Err(), context.Canceled):
			result = ProbeResult{Status: StatusUnknown, Reason: ReasonProbeCanceled}
		case err != nil:
			switch {
			case errors.Is(err, context.DeadlineExceeded):
				result = ProbeResult{Status: StatusUnknown, Reason: ReasonProbeTimeout}
			case errors.Is(err, context.Canceled):
				result = ProbeResult{Status: StatusUnknown, Reason: ReasonProbeCanceled}
			default:
				result = ProbeResult{Status: StatusDown, Reason: ReasonProbeFailed}
			}
		}
		if !validStatus(result.Status) || result.Status == StatusDegraded {
			result = ProbeResult{Status: StatusUnknown, Reason: ReasonInvalidResult}
		} else if result.Reason != "" && !validReason(result.Reason) {
			result.Reason = ReasonInvalidResult
		}
		running.result, running.observed = result, e.now()
		e.mu.Lock()
		e.storeCompleted(key, cacheEntry{result: running.result, observed: running.observed, expires: running.observed.Add(e.ttl)})
		delete(e.flights, key)
		close(running.done)
		e.mu.Unlock()
	}()

	return e.await(caller, running)
}

func (e *Evaluator) await(caller context.Context, running *flight) (ProbeResult, time.Time) {
	remaining := time.Until(running.deadline)
	if remaining <= 0 {
		return ProbeResult{Status: StatusUnknown, Reason: ReasonProbeTimeout}, e.now()
	}
	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case <-caller.Done():
		if errors.Is(caller.Err(), context.DeadlineExceeded) {
			return ProbeResult{Status: StatusUnknown, Reason: ReasonProbeTimeout}, e.now()
		}
		return ProbeResult{Status: StatusUnknown, Reason: ReasonProbeCanceled}, e.now()
	case <-timer.C:
		return ProbeResult{Status: StatusUnknown, Reason: ReasonProbeTimeout}, e.now()
	case <-running.done:
		return running.result, running.observed
	}
}

func validStatus(status Status) bool {
	return status == StatusUp || status == StatusDown || status == StatusUnknown
}

func validReason(reason ReasonCode) bool {
	switch reason {
	case ReasonNone, ReasonProbeNotConfigured, ReasonProbeFailed, ReasonProbeTimeout,
		ReasonProbeCanceled, ReasonInvalidResult, ReasonCheckMissing, ReasonNoChecks, ReasonProbeCapacity:
		return true
	default:
		return false
	}
}

func (e *Evaluator) storeCompleted(key string, entry cacheEntry) {
	if e.ttl <= 0 {
		return
	}
	if len(e.cache) >= e.cacheCapacity {
		var oldestKey string
		var oldest uint64
		first := true
		for candidate, cached := range e.cache {
			if first || cached.used < oldest {
				oldestKey, oldest, first = candidate, cached.used, false
			}
		}
		if first {
			return
		}
		delete(e.cache, oldestKey)
	}
	e.cacheSequence++
	entry.used = e.cacheSequence
	e.cache[key] = entry
}

func cloneSnapshot(snapshot Snapshot) Snapshot {
	clone := snapshot
	clone.Checks = append([]Check(nil), snapshot.Checks...)
	return clone
}
