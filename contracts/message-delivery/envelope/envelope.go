// Package envelope — общий конверт публикуемых контрактов контекста
// message-delivery (delivery-event, deliverability-policy).
//
// Форма конверта совпадает с конвертами контрактов других контекстов
// (contracts/outreach-orchestration/approvedaction, platform/contracts): поля
// позволяют потребителю дедуплицировать сообщения по event_id и выстраивать
// причинно-следственные связи (correlation_id / causation_id). Пакет зависит
// только от стандартной библиотеки: контракты не тянут за собой доменные типы
// владельца.
//
// Версионирование схемы — semver:
//   - MAJOR — несовместимое изменение смысла, типа или обязательности поля;
//   - MINOR — добавление опционального поля;
//   - PATCH — совместимое уточнение без изменения состава и смысла полей.
package envelope

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Envelope — общий конверт контрактов контекста message-delivery.
type Envelope struct {
	// EventID — стабильный идентификатор сообщения; используется для дедупликации.
	EventID string `json:"event_id"`
	// OccurredAt — момент возникновения факта в UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// TenantID — граница изоляции тенанта.
	TenantID string `json:"tenant_id"`
	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string `json:"correlation_id"`
	// CausationID — идентификатор сообщения-причины. Допускается пустым только
	// у корневого сообщения, у которого нет сообщения-причины.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (например, delivery-event).
	Name string `json:"name"`
	// SchemaVersion — версия схемы контракта в формате semver.
	SchemaVersion string `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие
// или некорректные, а не возвращает ошибку по первому. CausationID допускается
// пустым: у корневого сообщения нет сообщения-причины.
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
		return fmt.Errorf("envelope: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// EncodeKeyPart кодирует компонент ключа идемпотентности. Без кодирования
// разделитель "|" внутри значения склеил бы разные пары компонентов в один
// ключ; percent-кодирование делает разбор ключа однозначным. Кодирование
// совпадает с другими контрактами платформы, поэтому ключ, вычисленный в разных
// местах, совпадает.
func EncodeKeyPart(part string) string {
	return url.QueryEscape(part)
}

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией
// вида MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool {
	return semverRe.MatchString(version)
}
