// Package accessdecision объявляет опубликованный контракт access-decision
// контекста platform-governance: снимок правил доступа тенанта и решение о том,
// вправе ли актор действовать в тенанте.
//
// Контракт выражен типами и DTO, а не пакетами владельца: потребитель
// (outreach-orchestration, data-governance) держит только эту форму и не
// импортирует внутренние пакеты контекста-владельца. Зависимости —
// исключительно стандартная библиотека.
//
// Транспорт — async/amqp (интеграция policy): правило доставляется подпиской как
// снимок, потребитель применяет его локально. Синхронная проверка решения
// (`Decider`) при co-location реализуется прямым вызовом (пакет inproc), при
// разделении сервисов — сетевым HTTP-адаптером (пакет httpport, путь PathCanAct).
package accessdecision

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "access-decision"

// SchemaVersion — текущая версия схемы контракта.
const SchemaVersion = "1.0.0"

// Envelope — общий конверт контракта. Поля совпадают с требованиями
// docs/domain-model.md и позволяют потребителю дедуплицировать сообщения и
// выстраивать причинно-следственные связи (correlation_id / causation_id).
//
// Версионирование схемы — semver:
//   - MAJOR — несовместимое изменение смысла, типа или обязательности поля;
//   - MINOR — добавление опционального поля;
//   - PATCH — совместимое уточнение без изменения состава и смысла полей.
type Envelope struct {
	// EventID — стабильный идентификатор сообщения; используется для дедупликации.
	EventID string `json:"event_id"`
	// OccurredAt — момент возникновения факта в UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// TenantID — граница изоляции тенанта.
	TenantID string `json:"tenant_id"`
	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string `json:"correlation_id"`
	// CausationID — идентификатор сообщения-причины. Допускается пустым только у
	// корневого сообщения, у которого нет сообщения-причины.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (access-decision).
	Name string `json:"name"`
	// SchemaVersion — версия схемы контракта в формате semver.
	SchemaVersion string `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие или
// некорректные, а не возвращает ошибку по первому.
func (e Envelope) Validate() error {
	invalid := e.invalidFields()
	if len(invalid) > 0 {
		return fmt.Errorf("accessdecision: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// invalidFields возвращает имена отсутствующих или некорректных обязательных
// полей конверта. Служит для агрегирующей проверки контракта, собирающей дефекты
// конверта и полезной нагрузки одним сообщением.
func (e Envelope) invalidFields() []string {
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
	return invalid
}

// encodeKeyPart кодирует компонент ключа идемпотентности. Percent-кодирование
// делает разбор ключа однозначным: разделитель "|" внутри значения не склеит
// разные пары компонентов.
func encodeKeyPart(part string) string {
	return url.QueryEscape(part)
}

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией вида
// MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool {
	return semverRe.MatchString(version)
}
