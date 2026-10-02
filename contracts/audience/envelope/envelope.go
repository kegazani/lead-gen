// Package envelope объявляет общий конверт опубликованных контрактов контекста
// audience.
//
// Конверт — часть единого определения проводной формы: поля совпадают с
// требованиями docs/domain-model.md к описанию контракта и позволяют потребителю
// дедуплицировать сообщения и выстраивать причинно-следственные связи
// (correlation_id / causation_id). Контракты audience-snapshot и
// segment-membership реэкспортируют этот тип, поэтому форма конверта на границе
// одна.
//
// Зависимости пакета — исключительно стандартная библиотека.
package envelope

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Envelope — общий конверт контрактов контекста.
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
	// CausationID — идентификатор сообщения-причины. Допускается пустым только у
	// корневого сообщения, у которого нет сообщения-причины.
	CausationID string `json:"causation_id,omitempty"`
	// Name — имя контракта (например, audience-snapshot).
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
		return fmt.Errorf("envelope: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

var semverRe = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

// IsValidSemver сообщает, является ли значение корректной semver-версией вида
// MAJOR.MINOR.PATCH с необязательными pre-release и build-суффиксами.
func IsValidSemver(version string) bool {
	return semverRe.MatchString(version)
}
