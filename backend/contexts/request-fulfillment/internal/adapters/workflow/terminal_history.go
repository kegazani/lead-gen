package workflowadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	platformworkflow "lead-gen/backend/platform/workflow"
)

var ErrTerminalHistoryUnknown = errors.New("request-fulfillment: terminal history неизвестна или неполна")

// TerminalHistorySource даёт описание и страницы истории конкретного server run.
// Реальный platform Client реализует этот интерфейс; тестовые реализации допустимы
// только в unit tests.
type TerminalHistorySource interface {
	Describe(context.Context, string, string) (platformworkflow.Execution, error)
	ReadHistoryPage(context.Context, string, string, []byte, int) (platformworkflow.HistoryPage, error)
}

// TerminalIdentity — ожидаемая owner identity принятой попытки.
type TerminalIdentity struct {
	WorkflowID string
	RunID      string
	RequestID  string
	TenantID   string
	AttemptID  string
	Generation int64
	CommandID  string
}

// TerminalVerification содержит marker только после полной проверки истории.
type TerminalVerification struct {
	Intent      TerminalIntent
	CloseStatus string
	EventCount  int
	Evidence    []byte
}

// TerminalHistoryVerifier проверяет полную Temporal history, а не observation,
// совпадение ID либо произвольный actor/fingerprint.
type TerminalHistoryVerifier struct {
	Source    TerminalHistorySource
	PageSize  int
	MaxPages  int
	MaxEvents int
}

func (v TerminalHistoryVerifier) Verify(ctx context.Context, expected TerminalIdentity) (TerminalVerification, error) {
	if v.Source == nil || expected.WorkflowID == "" || expected.RunID == "" || expected.RequestID == "" || expected.TenantID == "" || expected.AttemptID == "" || expected.Generation < 1 || expected.CommandID == "" {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	pageSize, maxPages, maxEvents := v.PageSize, v.MaxPages, v.MaxEvents
	if pageSize <= 0 {
		pageSize = 100
	}
	if maxPages <= 0 {
		maxPages = 100
	}
	if maxEvents <= 0 {
		maxEvents = 10000
	}
	description, err := v.Source.Describe(ctx, expected.WorkflowID, expected.RunID)
	if err != nil {
		return TerminalVerification{}, fmt.Errorf("описать terminal workflow: %w", err)
	}
	if description.WorkflowID != expected.WorkflowID || description.RunID != expected.RunID || !terminalCloseStatus(description.Status) {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	var events []*historypb.HistoryEvent
	var token []byte
	seenTokens := make(map[string]struct{})
	complete := false
	for pageNo := 0; pageNo < maxPages; pageNo++ {
		page, err := v.Source.ReadHistoryPage(ctx, expected.WorkflowID, expected.RunID, token, pageSize)
		if err != nil {
			return TerminalVerification{}, fmt.Errorf("прочитать terminal history: %w", err)
		}
		if page.WorkflowID != expected.WorkflowID || page.RunID != expected.RunID || len(page.Events) == 0 {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		events = append(events, page.Events...)
		if len(events) > maxEvents {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		if len(page.NextToken) != 0 {
			key := string(page.NextToken)
			if _, exists := seenTokens[key]; exists {
				return TerminalVerification{}, ErrTerminalHistoryUnknown
			}
			seenTokens[key] = struct{}{}
		}
		token = append(token[:0], page.NextToken...)
		if page.Complete {
			if len(token) != 0 {
				return TerminalVerification{}, ErrTerminalHistoryUnknown
			}
			complete = true
			break
		}
		if len(token) == 0 {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
	}
	if !complete || len(events) < 2 || !contiguousHistory(events) {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	started := events[0].GetWorkflowExecutionStartedEventAttributes()
	if events[0].GetEventType() != enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED || started == nil {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	var params Params
	if err := converter.GetDefaultDataConverter().FromPayloads(started.GetInput(), &params); err != nil ||
		params.RequestID != expected.RequestID || params.TenantID != expected.TenantID || params.AttemptID != expected.AttemptID ||
		params.Generation != expected.Generation || params.CommandID != expected.CommandID {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	if !closeEventMatchesStatus(events[len(events)-1].GetEventType(), description.Status) {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	var intent *TerminalIntent
	markerCount := 0
	for _, event := range events {
		if event.GetEventType() != enumspb.EVENT_TYPE_MARKER_RECORDED {
			continue
		}
		attrs := event.GetMarkerRecordedEventAttributes()
		if attrs.GetMarkerName() != "SideEffect" {
			continue
		}
		payload := attrs.GetDetails()["data"]
		if payload == nil {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		var candidate TerminalIntent
		if err := converter.GetDefaultDataConverter().FromPayloads(payload, &candidate); err != nil {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		if candidate.Version != 1 || candidate.Checksum == "" || candidate.Checksum != terminalIntentChecksum(candidate) {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		if candidate.WorkflowID != expected.WorkflowID || candidate.RunID != expected.RunID || candidate.RequestID != expected.RequestID ||
			candidate.TenantID != expected.TenantID || candidate.AttemptID != expected.AttemptID || candidate.Generation != expected.Generation || candidate.CommandID != expected.CommandID ||
			!validTerminalReason(candidate.ReasonCode, candidate.OriginalReason) {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		markerCount++
		copy := candidate
		if intent != nil && *intent != copy {
			return TerminalVerification{}, ErrTerminalHistoryUnknown
		}
		intent = &copy
	}
	if intent == nil || markerCount != 1 {
		return TerminalVerification{}, ErrTerminalHistoryUnknown
	}
	encoded, err := json.Marshal(intent)
	if err != nil {
		return TerminalVerification{}, fmt.Errorf("закодировать проверенное terminal evidence: %w", err)
	}
	return TerminalVerification{Intent: *intent, CloseStatus: description.Status, EventCount: len(events), Evidence: encoded}, nil
}

// VerifyTerminalHistory implements the application port. The adapter exposes
// only proof returned after Describe and every history page have been validated.
func (v TerminalHistoryVerifier) VerifyTerminalHistory(ctx context.Context, expected ports.TerminalResolutionIdentity) (ports.TerminalHistoryVerification, error) {
	result, err := v.Verify(ctx, TerminalIdentity{
		WorkflowID: expected.WorkflowID, RunID: expected.RunID, RequestID: string(expected.RequestID),
		TenantID: string(expected.TenantID), AttemptID: expected.AttemptID, Generation: expected.Generation,
		CommandID: expected.CommandID,
	})
	if err != nil {
		return ports.TerminalHistoryVerification{}, err
	}
	return ports.TerminalHistoryVerification{
		Intent: domain.TerminalIntentEvidence{
			Version: result.Intent.Version, WorkflowID: result.Intent.WorkflowID, RunID: result.Intent.RunID,
			RequestID: result.Intent.RequestID, TenantID: result.Intent.TenantID, AttemptID: result.Intent.AttemptID,
			Generation: result.Intent.Generation, CommandID: result.Intent.CommandID, ReasonCode: result.Intent.ReasonCode,
			OriginalReason: result.Intent.OriginalReason, Checksum: result.Intent.Checksum,
		},
		CloseStatus: result.CloseStatus, Evidence: append([]byte(nil), result.Evidence...),
	}, nil
}

func validTerminalReason(code, reason string) bool {
	if reason == "" {
		return false
	}
	switch code {
	case failCodeSearch, failCodeDraft, failCodeHandoff, failCodeTransitionRejected, failCodeWorkflowTimedOut:
		return reason == failReason(code)
	default:
		return false
	}
}

func terminalCloseStatus(status string) bool {
	switch status {
	case enumspb.WORKFLOW_EXECUTION_STATUS_FAILED.String(), enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT.String(), enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED.String(), enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED.String():
		return true
	default:
		return false
	}
}

func closeEventMatchesStatus(event enumspb.EventType, status string) bool {
	switch event {
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_FAILED.String()
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT.String()
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_CANCELED.String()
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED.String()
	case enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		return status == enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED.String()
	default:
		return false
	}
}

func contiguousHistory(events []*historypb.HistoryEvent) bool {
	for index, event := range events {
		if event == nil || event.GetEventId() != int64(index+1) {
			return false
		}
	}
	return true
}
