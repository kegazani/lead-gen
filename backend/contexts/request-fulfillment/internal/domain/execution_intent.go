package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// CommandID — стабильный ключ идемпотентности команды запуска исполнения.
type CommandID string

// LeadTargetState различает заданную, не заданную и неразрешимую legacy-цель.
type LeadTargetState string

const (
	LeadTargetExplicit      LeadTargetState = "explicit"
	LeadTargetUnspecified   LeadTargetState = "unspecified"
	LeadTargetUnknownLegacy LeadTargetState = "unknownLegacy"
)

var (
	ErrInvalidExecutionIntent = errors.New("domain: некорректное намерение исполнения")
	ErrCommandConflict        = errors.New("domain: CommandID уже использован с другими параметрами")
)

// LeadTarget сохраняет семантику отсутствующего значения. Value значим только
// для explicit; ноль не используется как маркер отсутствия.
type LeadTarget struct {
	State LeadTargetState `json:"state"`
	Value int             `json:"value,omitempty"`
}

// ExecutionParameters — неизменяемый снимок параметров команды. Payload —
// канонический сериализованный набор owner-side параметров workflow.
type ExecutionParameters struct {
	RequestID  RequestID       `json:"request_id"`
	TenantID   TenantID        `json:"tenant_id"`
	LeadTarget LeadTarget      `json:"lead_target"`
	Payload    json.RawMessage `json:"payload"`
}

// ExecutionIntent — сохранённая команда одного поколения исполнения.
// Поля, определяющие идентичность команды, закрыты и доступны только через
// копирующие accessors.
type ExecutionIntent struct {
	commandID  CommandID
	requestID  RequestID
	tenantID   TenantID
	generation int64
	parameters ExecutionParameters
}

// ReceiptState — исход попытки по одному объекту handoff. Unknown и pending не
// являются подтверждением доставки.
type ReceiptState string

const (
	ReceiptPending   ReceiptState = "pending"
	ReceiptUnknown   ReceiptState = "unknown"
	ReceiptConfirmed ReceiptState = "confirmed"
)

// AttemptReceipt привязывает receipt к точной попытке и ожидаемому объекту.
type AttemptReceipt struct {
	RequestID  RequestID
	TenantID   TenantID
	Generation int64
	ObjectID   string
	State      ReceiptState
}

// NewExecutionIntent создаёт проверенную intent. Повторный CommandID сравнивается
// через SameCommand; различающиеся параметры не могут заменить исходную команду.
func NewExecutionIntent(commandID CommandID, requestID RequestID, tenantID TenantID, generation int64, target LeadTarget, payload json.RawMessage) (ExecutionIntent, error) {
	if strings.TrimSpace(string(commandID)) == "" || requestID.validate() != nil || tenantID.validate() != nil || generation < 1 {
		return ExecutionIntent{}, fmt.Errorf("%w: обязательные идентификаторы или generation", ErrInvalidExecutionIntent)
	}
	if err := target.Validate(); err != nil {
		return ExecutionIntent{}, err
	}
	canonical := json.RawMessage("{}")
	if len(payload) > 0 {
		var value any
		if err := json.Unmarshal(payload, &value); err != nil {
			return ExecutionIntent{}, fmt.Errorf("%w: параметры: %v", ErrInvalidExecutionIntent, err)
		}
		b, err := json.Marshal(value)
		if err != nil {
			return ExecutionIntent{}, fmt.Errorf("%w: параметры: %v", ErrInvalidExecutionIntent, err)
		}
		canonical = b
	}
	return ExecutionIntent{
		commandID: commandID, requestID: requestID, tenantID: tenantID, generation: generation,
		parameters: ExecutionParameters{RequestID: requestID, TenantID: tenantID, LeadTarget: target, Payload: canonical},
	}, nil
}

func (t LeadTarget) Validate() error {
	switch t.State {
	case LeadTargetExplicit:
		if t.Value < 0 {
			return fmt.Errorf("%w: LeadTarget меньше нуля", ErrInvalidExecutionIntent)
		}
	case LeadTargetUnspecified, LeadTargetUnknownLegacy:
		if t.Value != 0 {
			return fmt.Errorf("%w: значение допустимо только для explicit", ErrInvalidExecutionIntent)
		}
	default:
		return fmt.Errorf("%w: неизвестное состояние LeadTarget %q", ErrInvalidExecutionIntent, t.State)
	}
	return nil
}

func (i ExecutionIntent) CommandID() CommandID { return i.commandID }
func (i ExecutionIntent) RequestID() RequestID { return i.requestID }
func (i ExecutionIntent) TenantID() TenantID   { return i.tenantID }
func (i ExecutionIntent) Generation() int64    { return i.generation }
func (i ExecutionIntent) Parameters() ExecutionParameters {
	p := i.parameters
	p.Payload = append(json.RawMessage(nil), p.Payload...)
	return p
}

// SameCommand определяет безопасный идемпотентный повтор команды по всем
// неизменяемым полям; одинаковый ID с другим телом является конфликтом.
func (i ExecutionIntent) SameCommand(other ExecutionIntent) bool {
	return i.commandID == other.commandID && i.requestID == other.requestID && i.tenantID == other.tenantID &&
		i.generation == other.generation && i.parameters.LeadTarget == other.parameters.LeadTarget &&
		bytes.Equal(i.parameters.Payload, other.parameters.Payload)
}

// ExecutionIntentSnapshot — wire/storage представление intent, включая старые
// снимки, в которых target отсутствует (это unknownLegacy, а не ноль).
type ExecutionIntentSnapshot struct {
	CommandID   CommandID       `json:"command_id"`
	RequestID   RequestID       `json:"request_id"`
	TenantID    TenantID        `json:"tenant_id"`
	Generation  int64           `json:"generation"`
	TargetState LeadTargetState `json:"target_state,omitempty"`
	LeadTarget  *int            `json:"lead_target,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

func (i ExecutionIntent) Snapshot() ExecutionIntentSnapshot {
	s := ExecutionIntentSnapshot{CommandID: i.commandID, RequestID: i.requestID, TenantID: i.tenantID, Generation: i.generation, TargetState: i.parameters.LeadTarget.State, Payload: append(json.RawMessage(nil), i.parameters.Payload...)}
	if i.parameters.LeadTarget.State == LeadTargetExplicit {
		v := i.parameters.LeadTarget.Value
		s.LeadTarget = &v
	}
	return s
}

func RehydrateExecutionIntent(s ExecutionIntentSnapshot) (ExecutionIntent, error) {
	state := s.TargetState
	if state == "" {
		state = LeadTargetUnknownLegacy
	}
	if state == LeadTargetExplicit && s.LeadTarget == nil {
		state = LeadTargetUnknownLegacy
	}
	target := LeadTarget{State: state}
	if s.LeadTarget != nil {
		target.Value = *s.LeadTarget
	}
	return NewExecutionIntent(s.CommandID, s.RequestID, s.TenantID, s.Generation, target, s.Payload)
}
