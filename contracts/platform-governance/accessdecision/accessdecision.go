package accessdecision

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrUnavailable — владелец решения недоступен: контракт не подключён или сбой
// транспорта. Потребитель SHALL NOT выдавать это за положительное решение.
var ErrUnavailable = errors.New("accessdecision: владелец решения недоступен")

// Состояние тенанта в снимке правил. Значения совпадают с доменными состояниями
// владельца; положительное решение допускается только для active.
const (
	// StateActive — тенант активен и выдаёт решения доступа.
	StateActive = "active"
	// StateDisabled — тенант отключён.
	StateDisabled = "disabled"
	// StatePending — регистрация тенанта ожидает решения.
	StatePending = "pending"
	// StateRejected — регистрация тенанта отклонена.
	StateRejected = "rejected"
)

// Assignment — правило снимка: набор ролей актора в тенанте. Потребитель не
// воспроизводит RBAC: он механически сверяет запрос со снимком.
type Assignment struct {
	// Actor — пользователь тенанта.
	Actor string `json:"actor"`
	// Roles — роли, назначенные актору.
	Roles []string `json:"roles"`
}

// RuleSnapshot — снимок правил тенанта, публикуемый контрактом access-decision
// как policy. Ключ идемпотентности/версии — (tenant_id, snapshot_version).
type RuleSnapshot struct {
	Envelope

	// State — состояние тенанта (active/disabled/pending/rejected).
	State string `json:"state"`
	// SnapshotVersion — монотонная версия снимка. Потребитель применяет снимок
	// только при версии выше уже применённой.
	SnapshotVersion uint64 `json:"snapshot_version"`
	// Assignments — правила «актор → набор ролей».
	Assignments []Assignment `json:"assignments"`
}

// Validate проверяет конверт и обязательные поля снимка, собирая все дефекты
// одним сообщением.
func (s RuleSnapshot) Validate() error {
	invalid := s.Envelope.invalidFields()
	if s.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if s.State != StateActive && s.State != StateDisabled && s.State != StatePending && s.State != StateRejected {
		invalid = append(invalid, "state")
	}
	if s.SnapshotVersion == 0 {
		invalid = append(invalid, "snapshot_version")
	}
	for i, assignment := range s.Assignments {
		if strings.TrimSpace(assignment.Actor) == "" {
			invalid = append(invalid, fmt.Sprintf("assignments[%d].actor", i))
		}
		if len(assignment.Roles) == 0 {
			invalid = append(invalid, fmt.Sprintf("assignments[%d].roles", i))
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("accessdecision: access-decision: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности/версии снимка. Ключ внутренний
// и не является частью wire-формата.
func (s RuleSnapshot) IdempotencyKey() string {
	return encodeKeyPart(s.TenantID) + "|" + strconv.FormatUint(s.SnapshotVersion, 10)
}

// Decider — синхронный контракт проверки права: вправе ли актор действовать в
// тенанте. Форма совпадает с портом потребителя, поэтому порт потребителя не
// меняется, а его реализация переключается на контракт.
//
// Ошибка означает отказ инфраструктуры проверить право (в том числе
// недоступность владельца или пустой субъект), а не отрицательное решение:
// потребитель SHALL NOT трактовать ошибку как разрешение.
type Decider interface {
	// CanAct сообщает, вправе ли актор действовать в тенанте.
	CanAct(ctx context.Context, actor, tenant string) (bool, error)
}

// Unavailable — честный отказ, когда владелец решения не подключён. Это не
// положительное решение и не «заглушка-разрешение»: потребитель получает
// ошибку и честно отвечает отказом.
type Unavailable struct{}

// CanAct реализует Decider.
func (Unavailable) CanAct(context.Context, string, string) (bool, error) {
	return false, ErrUnavailable
}

// Проверка соответствия интерфейсу на этапе компиляции.
var _ Decider = Unavailable{}
