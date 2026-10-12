package workflowadapter_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	sdkworkflow "go.temporal.io/sdk/workflow"

	workflowadapter "lead-gen/backend/contexts/request-fulfillment/internal/adapters/workflow"
)

// TestReplayExistingFulfillmentHistoriesInTemporal connects read-only to the
// explicitly configured local Temporal service and replays existing open and
// closed Fulfillment histories against the bridge binary. It never starts,
// signals, or mutates a workflow, and suppresses workflow/run IDs from output.
func TestReplayExistingFulfillmentHistoriesInTemporal(t *testing.T) {
	if os.Getenv("LEADGEN_TEMPORAL_REPLAY_ACCEPTANCE") != "1" {
		t.Skip("requires explicit opt-in to read-only replay against existing local Temporal histories")
	}
	address := strings.TrimSpace(os.Getenv("LEADGEN_TEMPORAL_TEST_ADDRESS"))
	if address == "" {
		t.Fatal("LEADGEN_TEMPORAL_TEST_ADDRESS is required for opted-in history replay")
	}
	namespace := strings.TrimSpace(os.Getenv("LEADGEN_TEMPORAL_TEST_NAMESPACE"))
	if namespace == "" {
		namespace = "default"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	temporalClient, err := client.DialContext(ctx, client.Options{HostPort: address, Namespace: namespace})
	if err != nil {
		t.Fatal("read-only Temporal history replay client could not connect")
	}
	defer temporalClient.Close()

	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(workflowadapter.Fulfillment, sdkworkflow.RegisterOptions{Name: workflowadapter.WorkflowName})
	replayer.RegisterWorkflowWithOptions(workflowadapter.Fulfillment, sdkworkflow.RegisterOptions{Name: workflowadapter.LegacyWorkflowName})
	types := map[string]int{}
	replay := func(executionType, workflowID, runID string) bool {
		types[executionType]++
		if executionType != workflowadapter.WorkflowName && executionType != workflowadapter.LegacyWorkflowName {
			return false
		}
		identity := sdkworkflow.Execution{ID: workflowID, RunID: runID}
		if err := replayer.ReplayWorkflowExecution(ctx, temporalClient.WorkflowService(), nil, namespace, identity); err != nil {
			t.Fatalf("existing Fulfillment history is not replay-compatible with G1.5 bridge (workflow identity suppressed, error type %T)", err)
		}
		return true
	}
	replayClosed := func() (listed, replayed int) {
		pageToken := []byte(nil)
		for page := 0; page < 100; page++ {
			response, err := temporalClient.ListClosedWorkflow(ctx, &workflowservice.ListClosedWorkflowExecutionsRequest{
				Namespace: namespace, MaximumPageSize: 100, NextPageToken: pageToken,
			})
			if err != nil {
				t.Fatal("read-only closed Temporal workflow inventory query failed")
			}
			listed += len(response.Executions)
			for _, execution := range response.Executions {
				if execution == nil || execution.Execution == nil || execution.Type == nil {
					continue
				}
				if replay(execution.Type.Name, execution.Execution.WorkflowId, execution.Execution.RunId) {
					replayed++
				}
			}
			pageToken = response.NextPageToken
			if len(pageToken) == 0 {
				break
			}
		}
		return listed, replayed
	}
	replayOpen := func() (listed, replayed int) {
		pageToken := []byte(nil)
		for page := 0; page < 100; page++ {
			response, err := temporalClient.ListOpenWorkflow(ctx, &workflowservice.ListOpenWorkflowExecutionsRequest{
				Namespace: namespace, MaximumPageSize: 100, NextPageToken: pageToken,
			})
			if err != nil {
				t.Fatal("read-only open Temporal workflow inventory query failed")
			}
			listed += len(response.Executions)
			for _, execution := range response.Executions {
				if execution == nil || execution.Execution == nil || execution.Type == nil {
					continue
				}
				if replay(execution.Type.Name, execution.Execution.WorkflowId, execution.Execution.RunId) {
					replayed++
				}
			}
			pageToken = response.NextPageToken
			if len(pageToken) == 0 {
				break
			}
		}
		return listed, replayed
	}
	closedRows, closedReplayed := replayClosed()
	openRows, openReplayed := replayOpen()
	replayed := closedReplayed + openReplayed
	if replayed == 0 {
		t.Fatalf("no existing open/closed Fulfillment history was available for replay acceptance (closed rows=%d, open rows=%d, types=%v)", closedRows, openRows, types)
	}
	t.Logf("read-only Temporal compatibility replay passed for %d existing Fulfillment history/histories (%d closed, %d open); identifiers suppressed", replayed, closedReplayed, openReplayed)
}
