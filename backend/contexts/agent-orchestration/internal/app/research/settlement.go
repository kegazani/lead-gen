package research

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	ra "lead-gen/contracts/integration-platform/researchacquisition"
)

func (s *Service) scheduleSettlement(target *domain.ResearchAction, receipt ra.Receipt) {
	target.SettlementState = "pending"
	target.SettlementReason = "receipt-settlement-pending"
	target.SettlementInput = actionJSON(receipt)
	next := s.now().Add(5 * time.Second)
	target.SettlementNextAt = &next
}

// Обязательство подтвердить owner receipt независимо от полезного результата.
// Оно переживает завершение/отмену миссии и terminal drain: потеря финального
// COMMIT владельца не должна навсегда оставить reserved credits без проверки.
func (s *Service) settlePending(ctx context.Context, scope rm.Scope, id string) (bool, error) {
	var selected *domain.ResearchAction
	mission, err := s.config.Repository.Update(ctx, scope, id, func(current *domain.ResearchMission) error {
		now := s.now()
		for i := range current.Actions {
			action := &current.Actions[i]
			eligible := action.SettlementState == "pending" && (action.SettlementNextAt == nil || !now.Before(*action.SettlementNextAt))
			eligible = eligible || (action.SettlementState == "running" && action.SettlementLeaseUntil != nil && !now.Before(*action.SettlementLeaseUntil))
			if !eligible {
				continue
			}
			action.SettlementState = "running"
			action.SettlementEpoch++
			until := now.Add(time.Duration(current.Request.Limits.ActionTimeoutSeconds+15) * time.Second)
			action.SettlementLeaseUntil = &until
			action.SettlementNextAt = nil
			copy := *action
			selected = &copy
			current.Touch(now)
			break
		}
		return nil
	})
	if err != nil || selected == nil {
		return false, err
	}
	action := *selected
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(mission.Request.Limits.ActionTimeoutSeconds)*time.Second)
	defer cancel()
	var receipt ra.Receipt
	var lookupErr error
	reader, available := s.config.Sources[action.SourceID].(ReceiptReader)
	if !available {
		lookupErr = ra.ErrUnsupported
	} else {
		receipt, lookupErr = reader.Lookup(callCtx, ra.Scope{TenantID: scope.TenantID, PurposeID: scope.PurposeID}, action.ID, action.Attempt)
	}
	persistCtx, cleanup := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cleanup()
	_, err = s.config.Repository.Update(persistCtx, scope, id, func(current *domain.ResearchMission) error {
		target := current.Action(action.ID)
		if target == nil || target.SettlementEpoch != action.SettlementEpoch || target.SettlementState != "running" {
			return rm.ErrConflict
		}
		target.SettlementLeaseUntil = nil
		var original ra.Receipt
		if err := json.Unmarshal(target.SettlementInput, &original); err != nil {
			return err
		}
		lineage := receipt.Scope.TenantID == scope.TenantID && receipt.Scope.PurposeID == scope.PurposeID && receipt.ActionID == action.ID && receipt.Attempt == action.Attempt && (original.OperationID == "" || receipt.OperationID == original.OperationID) && receipt.OperationID != "" && receipt.Provider == original.Provider
		// Commit-ошибка Lookup не является свидетельством устойчивого unknown.
		inferredUnknown := errors.Is(lookupErr, ra.ErrUnknown) && receipt.Status == "unknown" && receipt.SHA256 == "" && (receipt.CostState == "unknown" || receipt.CostState == "not_reported")
		if errors.Is(lookupErr, ra.ErrNotFound) && original.OperationID == "" {
			target.SettlementState, target.SettlementReason = "settled", ""
			target.State, target.Reason = "failed", "provider-operation-not-created"
		} else if receipt.Persistence == "persisted" || inferredUnknown {
			if !lineage || (original.OperationID != "" && receipt.Persistence == "persisted" && !inferredUnknown && !sameAcquisitionPayload(original, receipt)) {
				target.SettlementState = "quarantined"
				target.SettlementReason = "receipt-settlement-conflict"
			} else {
				target.SettlementState = "settled"
				target.SettlementReason = ""
				if inferredUnknown || receipt.CostState == "unknown" {
					target.SettlementState = "settled-unknown"
					target.SettlementReason = "receipt-settlement-outcome-unknown"
				}
				target.SettlementReceipt = actionJSON(receipt)
				if original.OperationID == "" && len(target.Receipt) == 0 && (receipt.Status == "success" || receipt.Status == "partial" || receipt.Status == "empty") {
					s.acceptReceipt(current, target, receipt)
				}
			}
		} else {
			target.SettlementState = "pending"
			target.SettlementReason = "receipt-settlement-unavailable"
			if errors.Is(lookupErr, ra.ErrInFlight) {
				target.SettlementReason = "receipt-settlement-in-flight"
			}
			delay := time.Duration(action.SettlementEpoch) * 15 * time.Second
			if delay > 5*time.Minute {
				delay = 5 * time.Minute
			}
			next := s.now().Add(delay)
			target.SettlementNextAt = &next
		}
		current.Touch(s.now())
		return nil
	})
	return true, err
}

func sameAcquisitionPayload(left, right ra.Receipt) bool {
	// Это состояние локальной фиксации, а не часть immutable provider payload.
	left.Persistence, right.Persistence = "", ""
	return string(actionJSON(left)) == string(actionJSON(right))
}
