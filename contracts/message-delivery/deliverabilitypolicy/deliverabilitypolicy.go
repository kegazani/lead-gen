package deliverabilitypolicy

import (
	"fmt"
	"strings"
	"time"

	"lead-gen/contracts/message-delivery/envelope"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры (architecture/domain-catalog.yaml).
const ContractName = "deliverability-policy"

// SchemaVersion — текущая версия схемы. Изменять по правилам semver из envelope.go.
const SchemaVersion = "1.0.0"

// Envelope — общий конверт контрактов контекста. Реэкспорт пакета envelope.
type Envelope = envelope.Envelope

// Constraint — имя активного ограничения отправляющего домена. Значения
// совпадают с доменными ограничениями владельца (internal/domain): контракт не
// изобретает ограничений, которых нет в домене.
type Constraint string

const (
	// ConstraintPaused — новые отправки домена приостановлены.
	ConstraintPaused Constraint = "paused"
	// ConstraintThrottled — отправки домена ограничены по интенсивности.
	ConstraintThrottled Constraint = "throttled"
)

// Policy — снимок правил доставляемости отправляющего домена.
//
// Contract минимален: домен и его ограничения, предел попыток и момент
// вступления. Сырых ответов провайдера здесь нет — только нормализованный итог
// (инвариант sender-reputation).
type Policy struct {
	envelope.Envelope

	// PolicyID — идентификатор снимка политики.
	PolicyID string `json:"policy_id"`
	// SenderDomain — отправляющий домен, к которому относится снимок.
	SenderDomain string `json:"sender_domain"`
	// Scope — область действия снимка (например, tenant или домен).
	Scope string `json:"scope"`
	// MaxAttempts — предел попыток доставки сообщения этого домена.
	MaxAttempts int64 `json:"max_attempts"`
	// Suspended — приостановлены ли новые отправки домена.
	Suspended bool `json:"suspended"`
	// Constraints — активные ограничения домена.
	Constraints []Constraint `json:"constraints,omitempty"`
	// EffectiveAt — момент вступления снимка в силу.
	EffectiveAt time.Time `json:"effective_at"`
}

// NewPolicy возвращает снимок с проставленными именем и версией схемы.
// Остальные поля заполняет вызывающий.
func NewPolicy(env Envelope) Policy {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return Policy{Envelope: env}
}

// Validate проверяет envelope и обязательные поля контракта. Ограничения
// сверяются с закрытым набором, а не проходят произвольной строкой. Ошибка
// называет все отсутствующие или некорректные поля.
func (p Policy) Validate() error {
	if err := p.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if p.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(p.PolicyID) == "" {
		invalid = append(invalid, "policy_id")
	}
	if strings.TrimSpace(p.SenderDomain) == "" {
		invalid = append(invalid, "sender_domain")
	}
	if strings.TrimSpace(p.Scope) == "" {
		invalid = append(invalid, "scope")
	}
	if p.MaxAttempts <= 0 {
		invalid = append(invalid, "max_attempts")
	}
	if len(p.Constraints) == 0 && p.Suspended {
		// Приостановка обязана быть выражена активным ограничением: иначе
		// потребитель не знает причины и не может сопоставить снимок с доменом.
		invalid = append(invalid, "constraints")
	}
	for _, c := range p.Constraints {
		if !ValidConstraint(c) {
			invalid = append(invalid, "constraints")
			break
		}
	}
	if p.EffectiveAt.IsZero() {
		invalid = append(invalid, "effective_at")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("deliverabilitypolicy: deliverability-policy: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// ValidConstraint сообщает, объявлено ли ограничение доменом.
func ValidConstraint(c Constraint) bool {
	switch c {
	case ConstraintPaused, ConstraintThrottled:
		return true
	default:
		return false
	}
}

// IdempotencyKey возвращает ключ идемпотентности снимка. Повтор снимка той же
// версии политики для того же домена не создаёт второго эффекта у потребителя.
func (p Policy) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s|%s",
		envelope.EncodeKeyPart(p.TenantID),
		envelope.EncodeKeyPart(p.SenderDomain),
		envelope.EncodeKeyPart(p.PolicyID),
	)
}
