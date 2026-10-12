package ports

import (
	"context"
	"errors"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

var (
	ErrExecutionAttemptNotFound      = errors.New("порты: попытка исполнения не найдена")
	ErrExecutionCommandConflict      = errors.New("порты: конфликт CommandID")
	ErrExecutionFenceConflict        = errors.New("порты: fencing token или версия попытки устарели")
	ErrExecutionLeaseUnavailable     = errors.New("порты: lease попытки недоступна")
	ErrExecutionManifestConflict     = errors.New("порты: manifest попытки неизменяем")
	ErrExecutionReceiptConflict      = errors.New("порты: receipt не входит в manifest")
	ErrExecutionResolutionConflict   = errors.New("порты: конфликт resolution_id")
	ErrExecutionAdmissionBlocked     = errors.New("порты: новая admission заблокирована состоянием предыдущего исполнения")
	ErrExecutionAdmissionUnavailable = errors.New("порты: durable admission недоступна")
)

type AttemptState string

const (
	AttemptQueued   AttemptState = "queued"
	AttemptClaimed  AttemptState = "claimed"
	AttemptStarted  AttemptState = "started"
	AttemptUnknown  AttemptState = "unknown"
	AttemptTerminal AttemptState = "terminal"
)

// ExecutionAttempt — durable состояние попытки и fencing lease. Содержит
// неизменяемую intent и версию aggregate, на которой она была создана.
type ExecutionAttempt struct {
	Intent          domain.ExecutionIntent
	AttemptID       string
	RequestVersion  int
	State           AttemptState
	RunID           string
	LeaseOwner      string
	LeaseUntil      time.Time
	Fence           uint64
	ExpectedObjects []string
	ManifestSet     bool
	Receipts        []domain.AttemptReceipt
	ReceiptStates   map[string]domain.ReceiptState
}

type ExecutionLease struct {
	TenantID       domain.TenantID
	RequestID      domain.RequestID
	Generation     int64
	RequestVersion int
	CommandID      domain.CommandID
	Owner          string
	Fence          uint64
	Until          time.Time
}

// ExecutionOwnerState сохраняет различие «попытка отсутствует» и «исполнение
// доказанно никогда не запускалось»: legacy без попытки имеет runtime unknown.
type ExecutionOwnerState struct {
	TenantID              domain.TenantID
	RequestID             domain.RequestID
	ExecutionHistoryKnown bool
	HasAttempt            bool
	Target                domain.LeadTarget
	Runtime               AttemptState
	Attempt               *ExecutionAttempt
}

// ExecutionAttemptRepository вызывается из Transaction для записи intent и
// напрямую для lease claim. SaveIntent требует CAS по version, tenant и
// generation; уникальный CommandID неизменяем.
type ExecutionAttemptRepository interface {
	ReadOwnerState(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) (ExecutionOwnerState, error)
	SaveIntent(ctx context.Context, intent domain.ExecutionIntent, expectedRequestVersion int) error
	FindByCommand(ctx context.Context, tenantID domain.TenantID, commandID domain.CommandID) (ExecutionAttempt, error)
	ClaimNext(ctx context.Context, owner string, lease time.Duration) (ExecutionLease, error)
	RenewLease(ctx context.Context, lease ExecutionLease, duration time.Duration) (ExecutionLease, error)
	BindRun(ctx context.Context, lease ExecutionLease, runID string) error
	SetState(ctx context.Context, lease ExecutionLease, state AttemptState) error
	SetExpectedObjects(ctx context.Context, lease ExecutionLease, objectIDs []string) error
	RecordReceipt(ctx context.Context, lease ExecutionLease, objectID string, state domain.ReceiptState) error
}

// ExecutionResolutionRepository сохраняет append-only target corrections и
// unverified runtime observations. Повтор ID идемпотентен только при полном
// совпадении payload; новая запись требует expected-revision CAS.
type ExecutionResolutionRepository interface {
	// AppendResolution stores a target correction or an unverified runtime
	// observation. It is never evidence that the Temporal execution is terminal.
	AppendResolution(ctx context.Context, resolution domain.ExecutionResolution) error
	ListResolutions(ctx context.Context, tenantID domain.TenantID, requestID domain.RequestID) ([]domain.ExecutionResolution, error)
}

// VerifiedRuntimeResolutionWriter is available only through an application
// transaction. HTTP handlers receive neither Transaction nor this capability.
// Implementations must bind the complete evidence to the persisted attempt and
// use expected-revision CAS; actor/fingerprint/observation rows are insufficient.
type VerifiedRuntimeResolutionWriter interface {
	AppendVerifiedRuntimeResolution(ctx context.Context, resolution domain.VerifiedRuntimeResolution) error
}

// TerminalResolutionIdentity is the exact persisted attempt identity requested
// from the trusted Temporal history adapter.
type TerminalResolutionIdentity struct {
	WorkflowID string
	RunID      string
	RequestID  domain.RequestID
	TenantID   domain.TenantID
	AttemptID  string
	Generation int64
	CommandID  string
}

// TerminalHistoryVerification is returned only after full exact-run history
// verification by the Temporal adapter. Evidence contains the canonical safe
// serialized marker payload, never a claimed fingerprint alone.
type TerminalHistoryVerification struct {
	Intent      domain.TerminalIntentEvidence
	CloseStatus string
	Evidence    []byte
}

type TerminalHistoryVerifier interface {
	VerifyTerminalHistory(context.Context, TerminalResolutionIdentity) (TerminalHistoryVerification, error)
}
