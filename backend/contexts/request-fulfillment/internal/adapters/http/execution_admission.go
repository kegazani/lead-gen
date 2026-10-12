package httpapi

import (
	"crypto/rand"
	"encoding/base64"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
)

func (s *Server) executionAdmissionReady() bool {
	return s.deps.AdmitExecution != nil && s.deps.ExecutionConsumer != nil && s.deps.ExecutionConsumer.Active()
}

type executionAdmissionResponse struct {
	RequestID  string `json:"request_id"`
	WorkflowID string `json:"workflow_id"`
	AttemptID  string `json:"attempt_id"`
	CommandID  string `json:"command_id"`
	Generation int64  `json:"generation"`
	Status     string `json:"status"`
	State      string `json:"state"`
	Replay     bool   `json:"replay,omitempty"`
}

type executionStateResponse struct {
	RequestID             string `json:"request_id"`
	WorkflowID            string `json:"workflow_id"`
	AttemptID             string `json:"attempt_id,omitempty"`
	CommandID             string `json:"command_id,omitempty"`
	Generation            int64  `json:"generation,omitempty"`
	State                 string `json:"state"`
	TargetState           string `json:"target_state"`
	LeadTarget            *int   `json:"lead_target,omitempty"`
	ExecutionHistoryKnown bool   `json:"execution_history_known"`
}

func admissionResponse(result command.AdmissionResult) executionAdmissionResponse {
	return executionAdmissionResponse{
		RequestID: string(result.RequestID), WorkflowID: result.WorkflowID,
		AttemptID: result.AttemptID, CommandID: string(result.CommandID),
		Generation: result.Generation, Status: string(result.Status), State: string(result.State), Replay: result.Replay,
	}
}

func targetValue(target *int) int {
	if target == nil {
		return 0
	}
	return *target
}

func newHTTPCommandID() string {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw[:])
}
