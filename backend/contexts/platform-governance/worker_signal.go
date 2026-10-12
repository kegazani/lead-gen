package platformgovernance

import (
	"context"
	"fmt"
	"sync"

	"lead-gen/backend/platform/readiness"
)

// workerSignal отражает жизненный цикл outbox relay, а не наличие его адаптеров.
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

func (s *workerSignal) check() readiness.CheckSpec {
	if s == nil {
		return readiness.CheckSpec{ID: "outbox_relay_loop", Required: true}
	}
	s.mu.RLock()
	generation := s.generation
	phase := s.phase
	lastError := s.lastError
	s.mu.RUnlock()
	key := fmt.Sprintf("platform-governance.worker.outbox-relay.%d", generation)
	return readiness.CheckSpec{
		ID: "outbox_relay_loop", Key: key, Required: true,
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
		return fmt.Errorf("platform-governance: worker loop не настроен")
	}
	signal.begin()
	defer func() { signal.finish(runErr) }()
	// Конфигурация relay не считается готовностью: up публикуется только при
	// фактическом входе в Relay.Run.
	signal.activate()
	return run(ctx)
}
