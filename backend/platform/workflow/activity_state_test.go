package workflow

import (
	"encoding/json"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strings"
	"testing"
	"time"
)

func TestActivityStateReportsRetryWithoutExceptionPayload(t *testing.T) {
	now := time.Now()
	got := activityState(&workflowpb.PendingActivityInfo{
		ActivityType: &commonpb.ActivityType{Name: "SearchLeads"}, State: enumspb.PENDING_ACTIVITY_STATE_SCHEDULED, Attempt: 2,
		LastStartedTime: timestamppb.New(now.Add(-time.Minute)), LastHeartbeatTime: timestamppb.New(now.Add(-20 * time.Second)),
		NextAttemptScheduleTime: timestamppb.New(now.Add(time.Minute)),
		LastFailure:             &failurepb.Failure{Message: "private-provider-exception", FailureInfo: &failurepb.Failure_TimeoutFailureInfo{TimeoutFailureInfo: &failurepb.TimeoutFailureInfo{}}},
	})
	if got.Type != "SearchLeads" || got.State != "Scheduled" || got.Attempt != 2 || got.FailureKind != "timeout" || !got.NextAttemptAt.Equal(now.Add(time.Minute)) || !got.HeartbeatAt.Equal(now.Add(-20*time.Second)) {
		t.Fatalf("метаданные шага: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "private-provider-exception") {
		t.Fatal("свободный текст ошибки попал в сведения для UI")
	}
}
