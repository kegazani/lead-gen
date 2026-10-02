// Package engagementevent объявляет опубликованный контракт engagement-event
// контекста engagement-observation: квалифицированное событие взаимодействия.
//
// Контракт выражен типом и DTO, а не пакетами владельца: потребитель держит
// только эту форму и не импортирует внутренние пакеты контекста-владельца.
// Событие собирается без трекинговых пикселей: контракт описывает доставку,
// переход и ответ, но не открытие письма (policy pixel-free-observation).
//
// Зависимости — исключительно стандартная библиотека: контракт не импортирует
// ни внутренние пакеты владельца, ни platform, поэтому владелец может
// реализовать интерфейс, а потребитель — зависеть от контракта, не видя
// реализации (design.md, Решение 2).
package engagementevent

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "engagement-event"

// SchemaVersion — текущая версия схемы контракта (semver). Версионирование:
// MAJOR несовместимо, MINOR добавляет опциональное поле, PATCH уточняет смысл.
const SchemaVersion = "1.0.0"

// Допустимые виды квалифицированного взаимодействия. Значения совпадают с VO
// engagement-kind владельца. Набор замкнут; пиксельных видов в нём нет.
const (
	// KindDelivery — событие доставки письма.
	KindDelivery = "delivery"
	// KindTransition — переход по трекинг-ссылке.
	KindTransition = "transition"
	// KindReply — ответ на письмо.
	KindReply = "reply"
)

// allowedKinds — закрытый набор видов контракта.
var allowedKinds = map[string]struct{}{
	KindDelivery:   {},
	KindTransition: {},
	KindReply:      {},
}

// Допустимые степени уверенности. Значения совпадают с VO signal-confidence
// владельца.
const (
	// ConfidenceHigh — исход подтверждён источником.
	ConfidenceHigh = "high"
	// ConfidenceMedium — исход выведен из нормализованного события доставки.
	ConfidenceMedium = "medium"
	// ConfidenceLow — исход выведен из менее надёжного контекста.
	ConfidenceLow = "low"
)

var allowedConfidence = map[string]struct{}{
	ConfidenceHigh:   {},
	ConfidenceMedium: {},
	ConfidenceLow:    {},
}

// Envelope — общий конверт контрактов контекста. Поля совпадают с требованиями
// docs/domain-model.md к описанию контракта и позволяют потребителю дедуплицировать
// сообщения и выстраивать причинно-следственные связи (correlation_id /
// causation_id).
type Envelope struct {
	// EventID — стабильный идентификатор сообщения; ключ дедупликации.
	EventID string `json:"event_id"`
	// OccurredAt — момент возникновения факта в UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// TenantID — граница изоляции тенанта.
	TenantID string `json:"tenant_id"`
	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string `json:"correlation_id"`
	// CausationID — идентификатор сообщения-причины. Допускается пустым только у
	// корневого сообщения.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (engagement-event).
	Name string `json:"name"`
	// SchemaVersion — версия схемы контракта в формате semver.
	SchemaVersion string `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие или
// некорректные, а не возвращает ошибку по первому.
func (e Envelope) Validate() error {
	var invalid []string
	if strings.TrimSpace(e.EventID) == "" {
		invalid = append(invalid, "event_id")
	}
	if e.OccurredAt.IsZero() {
		invalid = append(invalid, "occurred_at")
	}
	if strings.TrimSpace(e.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if strings.TrimSpace(e.CorrelationID) == "" {
		invalid = append(invalid, "correlation_id")
	}
	if strings.TrimSpace(e.Name) == "" {
		invalid = append(invalid, "name")
	}
	if !IsValidSemver(e.SchemaVersion) {
		invalid = append(invalid, "schema_version")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("engagementevent: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// EngagementEvent — публикуемый контракт квалифицированного взаимодействия.
//
// Контракт передаёт минимум: вид, уверенность и исход, привязанные к компании.
// Пиксельных метрик открытий здесь нет по построению. Для перехода дополнительно
// несётся непрозрачная ссылка и адрес назначения.
//
// Ключ идемпотентности — EventID: повторная публикация того же события не создаёт
// второго эффекта у потребителя.
type EngagementEvent struct {
	Envelope

	// EngagementEventID — идентификатор события взаимодействия.
	EngagementEventID string `json:"engagement_event_id"`
	// CompanyID — компания, к которой отнесено взаимодействие.
	CompanyID string `json:"company_id"`
	// Kind — квалифицированный вид взаимодействия (значение VO engagement-kind).
	Kind string `json:"kind"`
	// Confidence — уверенность в квалификации (значение VO signal-confidence).
	Confidence string `json:"confidence"`
	// Outcome — квалифицированный исход.
	Outcome string `json:"outcome"`
	// LinkRef — непрозрачная ссылка перехода (только для вида transition).
	LinkRef string `json:"link_ref,omitempty"`
	// Target — адрес назначения перехода (только для вида transition).
	Target string `json:"target,omitempty"`
}

// NewEngagementEvent возвращает контракт с проставленными именем и версией схемы.
// Остальные поля заполняет вызывающий.
func NewEngagementEvent(env Envelope) EngagementEvent {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return EngagementEvent{Envelope: env}
}

// Validate проверяет envelope и обязательные поля контракта. Вид и уверенность
// сверяются с закрытыми наборами, а не с произвольной непустой строкой: пиксельный
// вид в контракт не проходит. Ошибка называет все отсутствующие поля.
func (e EngagementEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if e.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if strings.TrimSpace(e.EngagementEventID) == "" {
		invalid = append(invalid, "engagement_event_id")
	}
	if strings.TrimSpace(e.CompanyID) == "" {
		invalid = append(invalid, "company_id")
	}

	kind := strings.ToLower(strings.TrimSpace(e.Kind))
	if _, ok := allowedKinds[kind]; !ok {
		invalid = append(invalid, "kind")
	}
	if _, ok := allowedConfidence[strings.ToLower(strings.TrimSpace(e.Confidence))]; !ok {
		invalid = append(invalid, "confidence")
	}
	if strings.TrimSpace(e.Outcome) == "" {
		invalid = append(invalid, "outcome")
	}
	if kind == KindTransition {
		if strings.TrimSpace(e.LinkRef) == "" {
			invalid = append(invalid, "link_ref")
		}
		if strings.TrimSpace(e.Target) == "" {
			invalid = append(invalid, "target")
		}
	}
	if len(invalid) > 0 {
		return fmt.Errorf("engagementevent: engagement-event: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IdempotencyKey возвращает ключ идемпотентности контракта. Ключ внутренний и не
// является частью wire-формата; компоненты кодируются, чтобы разделитель не
// допускал коллизий.
func (e EngagementEvent) IdempotencyKey() string {
	return fmt.Sprintf("%s|%s", encodeKeyPart(e.TenantID), encodeKeyPart(e.EventID))
}

// encodeKeyPart кодирует компонент ключа идемпотентности.
func encodeKeyPart(part string) string { return url.QueryEscape(part) }

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией вида
// MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool { return semverRe.MatchString(version) }
