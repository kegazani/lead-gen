package analytics

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/platform/readiness"
)

// workerSignal отражает только жизненный цикл конкретного цикла роли.
type workerSignal struct {
	mu         sync.RWMutex
	phase      workerPhase
	lastError  bool
	generation uint64
}

type workerPhase uint8

const (
	workerNotStarted workerPhase = iota
	workerStarting
	workerActive
	workerStopped
)

func (s *workerSignal) begin() {
	s.mu.Lock()
	s.generation++
	s.phase = workerStarting
	s.lastError = false
	s.mu.Unlock()
}

func (s *workerSignal) activate() {
	s.mu.Lock()
	s.generation++
	s.phase = workerActive
	s.mu.Unlock()
}

func (s *workerSignal) finish(err error) {
	s.mu.Lock()
	s.generation++
	s.phase = workerStopped
	s.lastError = err != nil
	s.mu.Unlock()
}

func (s *workerSignal) check(id string, required bool) readiness.CheckSpec {
	if s == nil {
		return readiness.CheckSpec{ID: id, Required: required}
	}
	s.mu.RLock()
	generation := s.generation
	phase := s.phase
	lastError := s.lastError
	s.mu.RUnlock()
	key := fmt.Sprintf("analytics.worker.%s.%d", id, generation)
	return readiness.CheckSpec{
		ID: id, Key: key, Required: required,
		Probe: func(context.Context) (readiness.ProbeResult, error) {
			if phase == workerNotStarted || phase == workerStarting {
				return readiness.ProbeResult{Status: readiness.StatusUnknown, Reason: readiness.ReasonCheckMissing}, nil
			}
			if phase == workerActive {
				return readiness.ProbeResult{Status: readiness.StatusUp, Reason: readiness.ReasonNone}, nil
			}
			reason := readiness.ReasonProbeFailed
			if !lastError {
				reason = readiness.ReasonProbeCanceled
			}
			return readiness.ProbeResult{Status: readiness.StatusDown, Reason: reason}, nil
		},
	}
}

func runTracked(ctx context.Context, signal *workerSignal, run func(context.Context) error) (runErr error) {
	if signal == nil || run == nil {
		return fmt.Errorf("analytics: worker loop не настроен")
	}
	signal.begin()
	defer func() { signal.finish(runErr) }()
	// Сигнал становится up только когда RoleRunner действительно дошёл до вызова
	// цикла, а не при сборке subscriber/executor.
	signal.activate()
	return run(ctx)
}
