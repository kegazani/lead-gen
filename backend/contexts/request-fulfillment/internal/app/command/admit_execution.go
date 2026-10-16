package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

type AdmissionKind string

const (
	AdmissionApprove AdmissionKind = "approve"
	AdmissionRerun   AdmissionKind = "rerun"
	AdmissionExecute AdmissionKind = "execute"
)

// AdmitExecution — единая команда owner admission для approve, rerun и прямого
// endpoint исполнения. CommandID поступает из Idempotency-Key/скрытого поля.
type AdmitExecution struct {
	LifecycleFields
	CommandID      domain.CommandID
	Kind           AdmissionKind
	LeadTarget     *int
	Reason         string
	RequestVersion int
	OfferRevision  int
	Confidence     *float64
	FormParameters json.RawMessage
}

type AdmissionResult struct {
	RequestID  domain.RequestID
	WorkflowID string
	AttemptID  string
	CommandID  domain.CommandID
	Generation int64
	Status     domain.RequestStatus
	State      ports.AttemptState
	Replay     bool
}

type AdmitExecutionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

func NewAdmitExecutionHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *AdmitExecutionHandler {
	return &AdmitExecutionHandler{uow: uow, clock: clock, ids: ids}
}

func (h *AdmitExecutionHandler) Handle(ctx context.Context, cmd AdmitExecution) (AdmissionResult, error) {
	if cmd.CommandID == "" || (cmd.Kind != AdmissionApprove && cmd.Kind != AdmissionRerun && cmd.Kind != AdmissionExecute) {
		return AdmissionResult{}, domain.ErrInvalidExecutionIntent
	}
	in := transitionInput(cmd.Actor, cmd.At, cmd.Reason, h.clock, h.ids, cmd.CorrelationID, cmd.CausationID)
	var result AdmissionResult
	err := h.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
		request, err := tx.Requests().FindByID(txCtx, cmd.RequestID)
		if err != nil {
			return err
		}
		if request.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: заявка %s", ErrTenantMismatch, cmd.RequestID)
		}

		// Идемпотентный replay проверяется до текущего lifecycle/owner-state gate:
		// запрос с тем же CommandID после конкурентного перехода возвращает ранее
		// принятое исполнение, а не оценивается как новая команда.
		if existing, findErr := tx.ExecutionAttempts().FindByCommand(txCtx, cmd.TenantID, cmd.CommandID); findErr == nil {
			candidate, candidateErr := replayCandidate(existing, cmd)
			if candidateErr != nil || !existing.Intent.SameCommand(candidate) {
				return ports.ErrExecutionCommandConflict
			}
			result = admissionResult(request, existing, true)
			return nil
		} else if !errors.Is(findErr, ports.ErrExecutionAttemptNotFound) {
			return findErr
		}

		owner, err := tx.ExecutionAttempts().ReadOwnerState(txCtx, cmd.TenantID, cmd.RequestID)
		if err != nil {
			return err
		}
		if owner.HasAttempt || !owner.ExecutionHistoryKnown || owner.Target.State == domain.LeadTargetUnknownLegacy {
			// В этой задаче нет доверенного verifier-а terminal history. Ни target
			// resolution, ни runtime observation не дают права открыть gate.
			return ports.ErrExecutionAdmissionBlocked
		}
		if (cmd.Kind == AdmissionApprove && request.Status() != domain.StatusValidating) ||
			(cmd.Kind == AdmissionExecute && (!request.ExecutionHistoryKnown() || (request.Status() != domain.StatusValidated && request.Status() != domain.StatusValidating))) ||
			cmd.Kind == AdmissionRerun {
			// Legacy request без доказуемой истории исполнения остаётся blocked.
			return ports.ErrExecutionAdmissionBlocked
		}

		if request.ExecutionMode() == domain.ExecutionModeResearch {
			return domain.ErrExecutionModeConflict
		}

		target := domain.LeadTarget{State: domain.LeadTargetUnspecified}
		if cmd.LeadTarget != nil {
			target = domain.LeadTarget{State: domain.LeadTargetExplicit, Value: *cmd.LeadTarget}
		}
		requestVersion := cmd.RequestVersion
		if requestVersion == 0 {
			requestVersion = request.Version()
		}
		offerRevision := cmd.OfferRevision
		if offerRevision == 0 {
			offerRevision = request.Offer().Revision()
		}
		if offerRevision != request.Offer().Revision() {
			return ports.ErrVersionConflict
		}
		offerSnapshot := sha256.Sum256([]byte(request.Offer().Content().Text()))
		payload, err := commandPayload(requestVersion, offerRevision, request.Offer().ID().String(), hex.EncodeToString(offerSnapshot[:]), target, cmd)
		if err != nil {
			return err
		}
		generation := int64(1)
		intent, err := domain.NewExecutionIntent(cmd.CommandID, cmd.RequestID, cmd.TenantID, generation, target, payload)
		if err != nil {
			return err
		}
		expected := request.Version()
		var events []domain.DomainEvent
		switch cmd.Kind {
		case AdmissionApprove:
			events, err = request.Validate(in)
		case AdmissionExecute:
			if request.Status() == domain.StatusValidating {
				events, err = request.Validate(in)
			}
		case AdmissionRerun:
			events, err = request.Rerun(in)
		}
		if err != nil {
			return err
		}
		if request.Version() != expected {
			if err := tx.Requests().Save(txCtx, request, expected); err != nil {
				return err
			}
		}
		if err := tx.ExecutionAttempts().SaveIntent(txCtx, intent, expected); err != nil {
			return err
		}
		attemptID := fmt.Sprintf("%s#%d", cmd.RequestID, generation)
		workflowID := "fulfillment-" + string(cmd.RequestID)
		event := domain.ExecutionIntentAccepted{
			EventHeader: domain.EventHeader{
				EventID: h.ids.NewEventID(), OccurredAt: in.At,
				TenantID: cmd.TenantID, CorrelationID: cmd.CorrelationID, CausationID: cmd.CausationID,
			},
			RequestID: cmd.RequestID, CommandID: cmd.CommandID, AttemptID: attemptID,
			Generation: generation, WorkflowID: workflowID, Target: target, State: string(ports.AttemptQueued),
		}
		allEvents := append(events, event)
		if err := tx.Outbox().Append(txCtx, allEvents...); err != nil {
			return err
		}
		request.PullDomainEvents()
		attempt, err := tx.ExecutionAttempts().FindByCommand(txCtx, cmd.TenantID, cmd.CommandID)
		if err != nil {
			return err
		}
		result = admissionResult(request, attempt, false)
		return nil
	})
	if err != nil {
		originalErr := err
		// Конкурентный победитель мог закоммитить тот же CommandID после первой
		// проверки, пока эта транзакция ждала version/row lock. Перечитываем его
		// отдельной короткой UoW и возвращаем accepted attempt только при полном
		// совпадении параметров.
		var replay AdmissionResult
		replayErr := h.uow.WithinTransaction(ctx, func(txCtx context.Context, tx ports.Transaction) error {
			attempt, findErr := tx.ExecutionAttempts().FindByCommand(txCtx, cmd.TenantID, cmd.CommandID)
			if findErr != nil {
				return findErr
			}
			candidate, candidateErr := replayCandidate(attempt, cmd)
			if candidateErr != nil || !attempt.Intent.SameCommand(candidate) {
				return ports.ErrExecutionCommandConflict
			}
			request, findErr := tx.Requests().FindByID(txCtx, cmd.RequestID)
			if findErr != nil {
				return findErr
			}
			if request.TenantID() != cmd.TenantID {
				return ErrTenantMismatch
			}
			replay = admissionResult(request, attempt, true)
			return nil
		})
		if replayErr == nil {
			return replay, nil
		}
		if errors.Is(replayErr, ports.ErrExecutionCommandConflict) {
			return AdmissionResult{}, replayErr
		}
		return AdmissionResult{}, admissionError(originalErr)
	}
	return result, nil
}

func admissionError(err error) error {
	if err == nil {
		return nil
	}
	known := []error{
		ports.ErrRequestNotFound, ports.ErrVersionConflict, ports.ErrExecutionAttemptNotFound,
		ports.ErrExecutionCommandConflict, ports.ErrExecutionFenceConflict, ports.ErrExecutionAdmissionBlocked,
		domain.ErrInvalidExecutionIntent, domain.ErrInvalidTransition, domain.ErrTerminalState,
		domain.ErrEmptyID, domain.ErrMissingActor, domain.ErrMissingTime, domain.ErrRequestLimitReached,
		ErrTenantMismatch,
	}
	for _, candidate := range known {
		if errors.Is(err, candidate) {
			return err
		}
	}
	return ports.ErrExecutionAdmissionUnavailable
}

func replayCandidate(existing ports.ExecutionAttempt, cmd AdmitExecution) (domain.ExecutionIntent, error) {
	params := existing.Intent.Parameters()
	var input executionPayload
	if err := json.Unmarshal(params.Payload, &input); err != nil {
		return domain.ExecutionIntent{}, err
	}
	semanticBytes, marshalErr := json.Marshal(input.executionPayloadSemantic)
	if marshalErr != nil || input.CanonicalVersion != executionPayloadVersion || input.CanonicalFingerprint != fingerprintPayload(semanticBytes) ||
		input.Kind != cmd.Kind || input.Reason != cmd.Reason ||
		(cmd.RequestVersion != 0 && input.RequestVersion != cmd.RequestVersion) ||
		(cmd.OfferRevision != 0 && input.OfferRevision != cmd.OfferRevision) ||
		!sameOptionalFloat(input.Confidence, cmd.Confidence) || !sameCanonicalJSON(input.FormParameters, cmd.FormParameters) {
		return domain.ExecutionIntent{}, domain.ErrCommandConflict
	}
	target := domain.LeadTarget{State: domain.LeadTargetUnspecified}
	if cmd.LeadTarget != nil {
		target = domain.LeadTarget{State: domain.LeadTargetExplicit, Value: *cmd.LeadTarget}
	}
	payload, err := makeExecutionPayload(input.RequestVersion, input.OfferRevision, input.OfferID, input.OfferSnapshot, target, cmd)
	if err != nil {
		return domain.ExecutionIntent{}, err
	}
	return domain.NewExecutionIntent(cmd.CommandID, cmd.RequestID, cmd.TenantID, existing.Intent.Generation(), target, payload)
}

const executionPayloadVersion = 1

type executionPayloadSemantic struct {
	CanonicalVersion int               `json:"canonical_version"`
	RequestVersion   int               `json:"request_version"`
	OfferRevision    int               `json:"offer_revision"`
	OfferID          string            `json:"offer_id"`
	OfferSnapshot    string            `json:"offer_snapshot_fingerprint"`
	Kind             AdmissionKind     `json:"admission_kind"`
	Reason           string            `json:"reason"`
	Confidence       *float64          `json:"confidence,omitempty"`
	FormParameters   json.RawMessage   `json:"form_parameters,omitempty"`
	Actor            domain.ActorID    `json:"actor"`
	LeadTarget       domain.LeadTarget `json:"lead_target"`
}

type executionPayload struct {
	executionPayloadSemantic
	CanonicalFingerprint string `json:"canonical_fingerprint"`
}

func commandPayload(requestVersion, offerRevision int, offerID, offerSnapshot string, target domain.LeadTarget, cmd AdmitExecution) (json.RawMessage, error) {
	return makeExecutionPayload(requestVersion, offerRevision, offerID, offerSnapshot, target, cmd)
}

func makeExecutionPayload(requestVersion, offerRevision int, offerID, offerSnapshot string, target domain.LeadTarget, cmd AdmitExecution) (json.RawMessage, error) {
	formParameters, err := canonicalJSON(cmd.FormParameters)
	if err != nil {
		return nil, fmt.Errorf("некорректные параметры формы: %w", err)
	}
	semantic := executionPayloadSemantic{
		CanonicalVersion: executionPayloadVersion, RequestVersion: requestVersion,
		OfferRevision: offerRevision, OfferID: offerID, OfferSnapshot: offerSnapshot,
		Kind: cmd.Kind, Reason: cmd.Reason, Confidence: cloneFloat(cmd.Confidence), FormParameters: formParameters,
		Actor: cmd.Actor, LeadTarget: target,
	}
	encoded, err := json.Marshal(semantic)
	if err != nil {
		return nil, err
	}
	return json.Marshal(executionPayload{executionPayloadSemantic: semantic, CanonicalFingerprint: fingerprintPayload(encoded)})
}

func fingerprintPayload(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func sameCanonicalJSON(left, right json.RawMessage) bool {
	a, errA := canonicalJSON(left)
	b, errB := canonicalJSON(right)
	return errA == nil && errB == nil && string(a) == string(b)
}

func sameOptionalFloat(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func cloneFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func admissionResult(request *domain.Request, attempt ports.ExecutionAttempt, replay bool) AdmissionResult {
	return AdmissionResult{
		RequestID: request.ID(), WorkflowID: "fulfillment-" + string(request.ID()),
		AttemptID: attempt.AttemptID, CommandID: attempt.Intent.CommandID(),
		Generation: attempt.Intent.Generation(), Status: request.Status(), State: attempt.State, Replay: replay,
	}
}
