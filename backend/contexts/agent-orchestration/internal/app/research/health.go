package research

import (
	"context"
	"fmt"
	"time"

	rm "lead-gen/contracts/agent-orchestration/researchmission"
)

type workerHealth struct {
	ctx       context.Context
	scanned   bool
	scanError error
	deadline  time.Time
}

var _ rm.HealthChecker = (*Service)(nil)

// RunWorker владеет наблюдаемым lifecycle. Ручной RunReady не объявляет сервис
// готовым: отсутствие реально запущенного фонового цикла остаётся ошибкой.
func (s *Service) RunWorker(ctx context.Context, limit int, report func(error)) error {
	if limit < 1 || limit > 1000 {
		return rm.ErrInvalid
	}
	s.workerMu.Lock()
	if s.worker != nil {
		s.workerMu.Unlock()
		return rm.ErrConflict
	}
	s.worker = &workerHealth{ctx: ctx, deadline: time.Now().Add(10 * time.Second)}
	s.workerMu.Unlock()
	defer func() { s.workerMu.Lock(); s.worker = nil; s.workerMu.Unlock() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := s.runReady(ctx, limit, true); err != nil && ctx.Err() == nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (s *Service) workerActivity(bound time.Duration) {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.worker != nil {
		s.worker.deadline = time.Now().Add(bound)
	}
}
func (s *Service) workerScanResult(err error) {
	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.worker != nil {
		s.worker.scanned = true
		s.worker.scanError = err
		s.worker.deadline = time.Now().Add(30 * time.Second)
	}
}

func (s *Service) CheckHealth(ctx context.Context) error {
	s.workerMu.Lock()
	state := s.worker
	if state == nil {
		s.workerMu.Unlock()
		return fmt.Errorf("%w: research worker не запущен", rm.ErrUnavailable)
	}
	alive := state.ctx.Err() == nil && state.scanned && state.scanError == nil && time.Now().Before(state.deadline)
	s.workerMu.Unlock()
	if !alive {
		return fmt.Errorf("%w: research worker остановлен или очередь недоступна", rm.ErrUnavailable)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := s.config.Repository.CheckHealth(probeCtx); err != nil {
		return fmt.Errorf("%w: research PostgreSQL: %v", rm.ErrUnavailable, err)
	}
	return nil
}
