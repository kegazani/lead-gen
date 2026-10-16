package researchbridge

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/contracts/agent-orchestration/researchmission"
)

// Reconciler восстанавливает статус заявки по сохранённому исследованию.
// Чтение чужого владельца идёт до короткой транзакции команды заявки. После
// перезапуска validated/processing служат устойчивой очередью восстановления.
// Одобрение этим обработчиком никогда не создаётся.
type Reconciler struct {
	repository ports.RequestRepository
	missions   researchmission.Port
	start      *command.StartProcessingHandler
	fulfill    *command.FulfillRequestHandler
	fail       *command.FailRequestHandler
	cancel     *command.CancelRequestHandler
}

func NewReconciler(repository ports.RequestRepository, missions researchmission.Port, start *command.StartProcessingHandler, fulfill *command.FulfillRequestHandler, fail *command.FailRequestHandler, cancel *command.CancelRequestHandler) *Reconciler {
	return &Reconciler{repository: repository, missions: missions, start: start, fulfill: fulfill, fail: fail, cancel: cancel}
}

func (r *Reconciler) Scan(ctx context.Context) error {
	var failures []error
	for _, status := range []domain.RequestStatus{domain.StatusValidated, domain.StatusProcessing} {
		requests, err := r.repository.ListByStatus(ctx, status)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, request := range requests {
			if request.ExecutionMode() != domain.ExecutionModeResearch {
				continue
			}
			snapshots, err := r.missions.FindByIntent(ctx, researchmission.Scope{TenantID: string(request.TenantID()), PurposeID: request.ExecutionPurpose()}, string(request.ID()))
			if err != nil {
				failures = append(failures, fmt.Errorf("исследование заявки %s: %w", request.ID(), err))
				continue
			}
			// Первый путь запуска не создаёт несколько поколений на один intent.
			// Не выбираем произвольный успех при неожиданно неоднозначном результате.
			if len(snapshots) == 0 {
				continue
			}
			if len(snapshots) != 1 {
				failures = append(failures, ErrUnavailable)
				continue
			}
			if err = r.Observe(ctx, snapshots[0]); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (r *Reconciler) Observe(ctx context.Context, snapshot researchmission.Snapshot) error {
	request, err := r.repository.FindByID(ctx, domain.RequestID(snapshot.IntentID))
	if err != nil {
		return err
	}
	if request.ExecutionMode() != domain.ExecutionModeResearch || string(request.TenantID()) != snapshot.Scope.TenantID || request.ExecutionPurpose() != snapshot.Scope.PurposeID {
		return ErrDenied
	}
	selection := request.ResearchSelection()
	if selection == nil || selection.Quantity != snapshot.Goal.Quantity || selection.Unit != snapshot.Goal.Unit || selection.OfferRevision != snapshot.CriteriaVersion {
		return domain.ErrResearchSelectionConflict
	}
	if request.Status() != domain.StatusValidated && request.Status() != domain.StatusProcessing {
		return nil
	}
	if snapshot.MissionID == "" || snapshot.Revision < 1 {
		return ErrUnavailable
	}
	fields := command.LifecycleFields{RequestID: request.ID(), TenantID: request.TenantID(), Actor: "research-reconciler", CorrelationID: domain.CorrelationID(snapshot.MissionID), CausationID: domain.CausationID(fmt.Sprintf("%s:%d", snapshot.MissionID, snapshot.Revision))}
	if request.Status() == domain.StatusValidated {
		if _, err = r.start.Handle(ctx, command.StartProcessing{LifecycleFields: fields}); err != nil {
			return err
		}
	}
	switch snapshot.State {
	case "cancelled":
		_, err = r.cancel.Handle(ctx, command.CancelRequest{LifecycleFields: fields, Reason: "Исследование остановлено человеком. Сохранённые кандидаты остаются доступны."})
	case "completed", "failed":
		if snapshot.CompletedAt == nil {
			return ErrUnavailable
		}
		if snapshot.State == "completed" && snapshot.Outcome == "goal-reached" && snapshot.Counters.Accepted >= selection.Quantity {
			_, err = r.fulfill.Handle(ctx, command.FulfillRequest{LifecycleFields: fields})
		} else {
			reasonCode := "research_partial"
			if snapshot.State == "failed" {
				reasonCode = "research_failed"
			}
			reason := fmt.Sprintf("Исследование завершено с частичным результатом: зачтено %d из %d, сохранено кандидатов %d. Причина: %s. Карточки и основания доступны в исследовании.", snapshot.Counters.Accepted, selection.Quantity, snapshot.Counters.Saved, snapshot.StopReason)
			_, err = r.fail.Handle(ctx, command.FailRequest{LifecycleFields: fields, ReasonCode: reasonCode, Reason: reason})
		}
	}
	return err
}
