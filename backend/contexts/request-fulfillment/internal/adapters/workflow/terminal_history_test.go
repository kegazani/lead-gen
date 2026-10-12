package workflowadapter

import (
	"context"
	"testing"

	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/converter"

	platformworkflow "lead-gen/backend/platform/workflow"
)

type terminalHistoryFake struct {
	description platformworkflow.Execution
	pages       []platformworkflow.HistoryPage
	describeErr error
	pageErr     error
}

func (f terminalHistoryFake) Describe(context.Context, string, string) (platformworkflow.Execution, error) {
	return f.description, f.describeErr
}
func (f terminalHistoryFake) ReadHistoryPage(_ context.Context, workflowID, runID string, token []byte, _ int) (platformworkflow.HistoryPage, error) {
	if f.pageErr != nil {
		return platformworkflow.HistoryPage{}, f.pageErr
	}
	index := 0
	if len(token) > 0 {
		index = int(token[0])
	}
	page := f.pages[index]
	if page.WorkflowID == "" {
		page.WorkflowID = workflowID
	}
	if page.RunID == "" {
		page.RunID = runID
	}
	return page, nil
}

func TestTerminalHistoryVerifierRequiresAllPagesAndExactIdentity(t *testing.T) {
	expected := TerminalIdentity{WorkflowID: "workflow-1", RunID: "run-1", RequestID: "request-1", TenantID: "tenant-1", AttemptID: "attempt-1", Generation: 7, CommandID: "command-1"}
	intent := TerminalIntent{Version: 1, WorkflowID: expected.WorkflowID, RunID: expected.RunID, RequestID: expected.RequestID, TenantID: expected.TenantID,
		AttemptID: expected.AttemptID, Generation: expected.Generation, CommandID: expected.CommandID, ReasonCode: failCodeSearch, OriginalReason: failReason(failCodeSearch)}
	intent.Checksum = terminalIntentChecksum(intent)
	startedInput, err := converter.GetDefaultDataConverter().ToPayloads(Params{RequestID: expected.RequestID, TenantID: expected.TenantID, AttemptID: expected.AttemptID, Generation: expected.Generation, CommandID: expected.CommandID})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := converter.GetDefaultDataConverter().ToPayloads(intent)
	if err != nil {
		t.Fatal(err)
	}
	events := []*historypb.HistoryEvent{
		{EventId: 1, EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED, Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{Input: startedInput}}},
		{EventId: 2, EventType: enumspb.EVENT_TYPE_MARKER_RECORDED, Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{MarkerName: "SideEffect", Details: map[string]*commonpb.Payloads{"data": marker}}}},
		{EventId: 3, EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED, Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{}}},
	}
	fake := terminalHistoryFake{description: platformworkflow.Execution{WorkflowID: expected.WorkflowID, RunID: expected.RunID, Status: enumspb.WORKFLOW_EXECUTION_STATUS_FAILED.String()}, pages: []platformworkflow.HistoryPage{
		{Events: events[:2], NextToken: []byte{1}}, {Events: events[2:], Complete: true},
	}}
	verified, err := (TerminalHistoryVerifier{Source: fake, PageSize: 2}).Verify(context.Background(), expected)
	if err != nil {
		t.Fatalf("verify history: %v", err)
	}
	if verified.Intent.OriginalReason != intent.OriginalReason || verified.EventCount != 3 {
		t.Fatalf("unexpected verified result: %+v", verified)
	}

	unsupportedVersion := intent
	unsupportedVersion.Version = 2
	unsupportedVersion.Checksum = terminalIntentChecksum(unsupportedVersion)
	unsupportedPayload, err := converter.GetDefaultDataConverter().ToPayloads(unsupportedVersion)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedEvents := append([]*historypb.HistoryEvent(nil), events...)
	unsupportedEvents[1] = &historypb.HistoryEvent{
		EventId: 2, EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{
			MarkerName: "SideEffect", Details: map[string]*commonpb.Payloads{"data": unsupportedPayload},
		}},
	}
	unsupported := terminalHistoryFake{description: fake.description, pages: []platformworkflow.HistoryPage{{Events: unsupportedEvents, Complete: true}}}
	if _, err := (TerminalHistoryVerifier{Source: unsupported}).Verify(context.Background(), expected); err == nil {
		t.Fatal("неподдерживаемая версия TerminalIntent не должна подтверждаться")
	}

	partial := terminalHistoryFake{description: fake.description, pages: fake.pages[:1]}
	if _, err := (TerminalHistoryVerifier{Source: partial, PageSize: 2, MaxPages: 1}).Verify(context.Background(), expected); err == nil {
		t.Fatal("одна первая страница не должна подтверждать terminal")
	}
	foreign := expected
	foreign.AttemptID = "other-attempt"
	if _, err := (TerminalHistoryVerifier{Source: fake, PageSize: 2}).Verify(context.Background(), foreign); err == nil {
		t.Fatal("чужая attempt identity не должна подтверждаться")
	}

	wrongExecution := terminalHistoryFake{description: fake.description, pages: []platformworkflow.HistoryPage{
		{WorkflowID: "foreign-workflow", RunID: expected.RunID, Events: events, Complete: true},
	}}
	if _, err := (TerminalHistoryVerifier{Source: wrongExecution}).Verify(context.Background(), expected); err == nil {
		t.Fatal("страница чужого workflow не должна подтверждаться")
	}

	noMarkerEvents := []*historypb.HistoryEvent{events[0], events[2]}
	noMarkerEvents[1] = &historypb.HistoryEvent{EventId: 2, EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{}}}
	noMarker := terminalHistoryFake{description: fake.description, pages: []platformworkflow.HistoryPage{{Events: noMarkerEvents, Complete: true}}}
	if _, err := (TerminalHistoryVerifier{Source: noMarker}).Verify(context.Background(), expected); err == nil {
		t.Fatal("история без terminal marker не должна подтверждаться")
	}

	completed := terminalHistoryFake{description: platformworkflow.Execution{WorkflowID: expected.WorkflowID, RunID: expected.RunID,
		Status: enumspb.WORKFLOW_EXECUTION_STATUS_COMPLETED.String()}, pages: fake.pages}
	if _, err := (TerminalHistoryVerifier{Source: completed}).Verify(context.Background(), expected); err == nil {
		t.Fatal("успешное завершение не является доказательством terminal compensation")
	}

	wrongClose := terminalHistoryFake{description: fake.description, pages: []platformworkflow.HistoryPage{{Events: []*historypb.HistoryEvent{events[0], events[1], {
		EventId: 3, EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED,
		Attributes: &historypb.HistoryEvent_WorkflowExecutionCanceledEventAttributes{WorkflowExecutionCanceledEventAttributes: &historypb.WorkflowExecutionCanceledEventAttributes{}},
	}}, Complete: true}}}
	if _, err := (TerminalHistoryVerifier{Source: wrongClose}).Verify(context.Background(), expected); err == nil {
		t.Fatal("несовпадающие close event и Describe status не должны подтверждаться")
	}

	badChecksum := intent
	badChecksum.OriginalReason = "подменённая причина"
	badMarker, err := converter.GetDefaultDataConverter().ToPayloads(badChecksum)
	if err != nil {
		t.Fatal(err)
	}
	badEvents := append([]*historypb.HistoryEvent(nil), events...)
	badEvents[1] = &historypb.HistoryEvent{EventId: 2, EventType: enumspb.EVENT_TYPE_MARKER_RECORDED,
		Attributes: &historypb.HistoryEvent_MarkerRecordedEventAttributes{MarkerRecordedEventAttributes: &historypb.MarkerRecordedEventAttributes{MarkerName: "SideEffect", Details: map[string]*commonpb.Payloads{"data": badMarker}}}}
	badHistory := terminalHistoryFake{description: fake.description, pages: []platformworkflow.HistoryPage{{Events: badEvents, Complete: true}}}
	if _, err := (TerminalHistoryVerifier{Source: badHistory}).Verify(context.Background(), expected); err == nil {
		t.Fatal("причина без соответствующего checksum не должна подтверждаться")
	}

	legacy := expected
	legacy.AttemptID = ""
	legacy.Generation = 0
	legacy.CommandID = ""
	if _, err := (TerminalHistoryVerifier{Source: fake}).Verify(context.Background(), legacy); err == nil {
		t.Fatal("v1 identity без attempt не должна выводиться из request payload")
	}
}
