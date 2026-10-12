// Package approvedaction — опубликованный (published language) контракт
// approved-action контекста outreach-orchestration.
//
// Модуль содержит только публичные типы контракта и интерфейс синхронной связи.
// Его зависимости — исключительно стандартная библиотека: он не импортирует ни
// один модуль контекста и не импортирует platform. Поэтому владелец может
// реализовать интерфейс, а потребитель — зависеть от контракта, не видя
// внутренних пакетов владельца (design.md, Решение 2).
//
// Форма контракта здесь — единственное определение wire-формата approved-action.
// Внутренний код владельца не повторяет её, а реэкспортирует
// (contexts/outreach-orchestration/internal/ports/contracts/approved_action.go):
// `type ApprovedAction = approvedaction.ApprovedAction` и те же константы. Тот же
// конверт Envelope, та же версия схемы и тот же ключ идемпотентности
// (tenant_id, campaign_id, draft_id, draft_version). Смысл полей не изменён.
package approvedaction

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Envelope — общий конверт контрактов контекста. Поля совпадают с требованиями
// docs/domain-model.md к описанию контракта и позволяют потребителю
// дедуплицировать сообщения и выстраивать причинно-следственные связи
// (correlation_id / causation_id).
//
// Версионирование схемы — semver:
//   - MAJOR — несовместимое изменение смысла, типа или обязательности поля;
//   - MINOR — добавление опционального поля;
//   - PATCH — совместимое уточнение без изменения состава и смысла полей.
//
// Потребитель обязан отвергать MAJOR выше поддерживаемой и продолжать читать
// более высокую MINOR/PATCH (docs/domain-model.md, «Межконтекстные отношения»).
type Envelope struct {
	// EventID — стабильный идентификатор сообщения; используется для дедупликации.
	EventID string `json:"event_id"`
	// OccurredAt — момент возникновения факта в UTC.
	OccurredAt time.Time `json:"occurred_at"`
	// TenantID — граница изоляции тенанта.
	TenantID string `json:"tenant_id"`
	// CorrelationID — идентификатор сквозного бизнес-процесса.
	CorrelationID string `json:"correlation_id"`
	// CausationID — идентификатор сообщения-причины. Входит в состав контракта
	// (docs/domain-model.md, «Межконтекстные отношения»). Допускается пустым
	// только у корневого сообщения, у которого нет сообщения-причины.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (например, approved-action).
	Name string `json:"name"`
	// SchemaVersion — версия схемы контракта в формате semver.
	SchemaVersion string `json:"schema_version"`
}

// Validate проверяет обязательные поля конверта и собирает все отсутствующие
// или некорректные, а не возвращает ошибку по первому. CausationID допускается
// пустым: у корневой команды нет сообщения-причины.
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
		return fmt.Errorf("approvedaction: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// encodeKeyPart кодирует компонент ключа идемпотентности. Без кодирования
// разделитель "|" внутри значения склеил бы разные пары компонентов в один
// ключ; percent-кодирование делает разбор ключа однозначным. Кодирование
// совпадает с внутренним контрактом владельца, поэтому ключ, вычисленный
// потребителем и владельцем, совпадает.
func encodeKeyPart(part string) string {
	return url.QueryEscape(part)
}

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией
// вида MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool {
	return semverRe.MatchString(version)
}
