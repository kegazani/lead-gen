// Package supervisor — application-сервис супервизора агентов: порождает агентов по
// мере поступления компаний, соблюдает предел одновременной обработки и очередность,
// повторяет неуспешный шаг в пределах попыток и завершает агента по команде
// оператора.
//
// Супервизор не принимает предметных решений за блоки пайплайна и не обходит
// утверждение оператора: он только управляет запуском (design.md, Реш. 4). Предел
// одновременной обработки считается по активным запускам тенанта.
package supervisor

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// defaultMaxConcurrent — предел одновременных агентов, если задано некорректное
// значение.
const defaultMaxConcurrent = 1

// Supervisor — политика запуска агентов. Предметного состояния у него нет:
// состояние живёт в агрегате agent-run.
type Supervisor struct {
	uow       ports.UnitOfWork
	runs      ports.RunRepository
	workflows ports.WorkflowRunner
	clock     ports.Clock
	ids       ports.IDGenerator

	maxConcurrent int
}

// New собирает супервизор. maxConcurrent <= 0 заменяется значением по умолчанию.
func New(uow ports.UnitOfWork, runs ports.RunRepository, workflows ports.WorkflowRunner, clock ports.Clock, ids ports.IDGenerator, maxConcurrent int) *Supervisor {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	return &Supervisor{
		uow:           uow,
		runs:          runs,
		workflows:     workflows,
		clock:         clock,
		ids:           ids,
		maxConcurrent: maxConcurrent,
	}
}

// Limit возвращает предел одновременной обработки.
func (s *Supervisor) Limit() int { return s.maxConcurrent }

// Admit принимает нового агента: сохраняет запуск в очереди и делает проход
// запуска, если предел одновременной обработки не достигнут.
func (s *Supervisor) Admit(ctx context.Context, run *domain.AgentRun) error {
	if run == nil {
		return fmt.Errorf("супервизор: nil-запуск")
	}
	if err := s.saveNewRun(ctx, run); err != nil {
		return err
	}
	_, err := s.Pump(ctx, run.TenantID())
	return err
}

// Pump запускает запуски из очереди тенанта, пока не достигнут предел
// одновременной обработки. Возвращает число запущенных агентов. Недоступность
// рантайма долгих процессов — ошибка: агент остаётся в очереди, а не выдаётся за
// запущенного.
func (s *Supervisor) Pump(ctx context.Context, tenant domain.TenantID) (int, error) {
	if s.workflows == nil {
		return 0, ports.ErrWorkflowUnavailable
	}
	active, err := s.runs.CountActive(ctx, tenant)
	if err != nil {
		return 0, err
	}
	started := 0
	for active < s.maxConcurrent {
		queued, err := s.runs.ListByStatus(ctx, domain.StatusQueued, tenant, 1)
		if err != nil {
			return started, err
		}
		if len(queued) == 0 {
			break
		}
		if err := s.startOne(ctx, queued[0]); err != nil {
			return started, err
		}
		active++
		started++
	}
	return started, nil
}

// Stop завершает запуск по команде оператора: переводит агрегат в терминальное
// состояние «остановлен» и подаёт команду остановки рантайму.
func (s *Supervisor) Stop(ctx context.Context, runID domain.RunID, tenant domain.TenantID, actor domain.ActorID, reason, correlationID string) error {
	now := s.clock.Now()
	err := s.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		run, err := tx.Runs().FindByID(ctx, runID, tenant)
		if err != nil {
			return err
		}
		revision := run.Revision()
		if err := run.Stop(actor, reason, now, header(s.ids, correlationID, "")); err != nil {
			return err
		}
		if err := tx.Runs().Save(ctx, run, revision); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, run.PullDomainEvents()...)
	})
	if err != nil {
		return err
	}
	if s.workflows != nil {
		if err := s.workflows.StopRun(ctx, ports.StopInput{
			RunID:    string(runID),
			TenantID: string(tenant),
			Actor:    string(actor),
			Reason:   reason,
		}); err != nil {
			return fmt.Errorf("супервизор: остановка записана, но рантайм недоступен: %w", err)
		}
	}
	return nil
}

// RetryStep сообщает, можно ли повторить текущий неуспешный шаг запуска в
// пределах числа попыток. Исполнение повтора выполняет прогон шага; супервизор
// только применяет правило попыток (policy step-idempotency).
func (s *Supervisor) RetryStep(ctx context.Context, runID domain.RunID, tenant domain.TenantID) (bool, error) {
	run, err := s.runs.FindByID(ctx, runID, tenant)
	if err != nil {
		return false, err
	}
	step, ok := run.CurrentStep()
	if !ok {
		return false, nil
	}
	if step.Status() != domain.StepRunFailed {
		return false, nil
	}
	return step.Attempts() < step.MaxAttempts(), nil
}

// saveNewRun сохраняет новый запуск в очереди и записывает его события.
func (s *Supervisor) saveNewRun(ctx context.Context, run *domain.AgentRun) error {
	return s.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Runs().Save(ctx, run, 0); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, run.PullDomainEvents()...)
	})
}

// startOne запускает durable-исполнение запуска и переводит агрегат в состояние
// «исполняется». Отказ рантайма оставляет запуск в очереди.
func (s *Supervisor) startOne(ctx context.Context, run *domain.AgentRun) error {
	mission := run.Mission()
	if _, err := s.workflows.StartRun(ctx, ports.StartRunInput{
		RunID:           string(run.ID()),
		TenantID:        string(run.TenantID()),
		MissionID:       string(mission.ID()),
		CompanyID:       string(mission.CompanyID()),
		PipelineID:      string(run.PipelineID()),
		PipelineVersion: run.PipelineVersion(),
		Criteria:        mission.Criteria(),
	}); err != nil {
		return err
	}
	now := s.clock.Now()
	return s.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		fresh, err := tx.Runs().FindByID(ctx, run.ID(), run.TenantID())
		if err != nil {
			return err
		}
		revision := fresh.Revision()
		if fresh.Status() == domain.StatusQueued {
			if err := fresh.Start(now, header(s.ids, "", "")); err != nil {
				return err
			}
		}
		if err := tx.Runs().Save(ctx, fresh, revision); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, fresh.PullDomainEvents()...)
	})
}

// header собирает конверт события с новым event_id.
func header(ids ports.IDGenerator, correlation, causation string) domain.EventHeader {
	var eventID domain.EventID
	if ids != nil {
		eventID = ids.NewEventID()
	}
	return domain.EventHeader{
		EventID:       eventID,
		CorrelationID: domain.CorrelationID(correlation),
		CausationID:   domain.CausationID(causation),
	}
}
