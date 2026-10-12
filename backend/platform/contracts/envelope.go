// Package contracts фиксирует форму межконтекстных контрактов и правила их
// версионирования как технический примитив платформы.
//
// Конверт повторяет форму конверта событий контекста
// (contexts/outreach-orchestration/internal/ports/contracts) как собственный
// тип. Платформенный транспорт не импортирует контекст: общий конверт не
// должен тянуть за собой доменные типы одного из потребителей, а совпадение
// формы обеспечивается тестом, а не импортом.
//
// Пакет не знает о сети, хранилищах и бизнес-терминах: он проверяет поля
// конверта, разбирает semver и вычисляет ключ маршрутизации.
package contracts

import (
	"fmt"
	"strings"
	"time"
)

// Envelope — общий конверт контрактов границы сервиса. Его поля позволяют
// потребителю дедуплицировать сообщения и выстраивать причинно-следственные
// связи (correlation_id / causation_id).
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
	// CausationID — идентификатор сообщения-причины. Допускается пустым только
	// у корневого сообщения, у которого нет сообщения-причины.
	CausationID string `json:"causation_id,omitempty"`
	// WorkflowID — идентификатор исполнения workflow, если сообщение рождено
	// внутри durable-исполнения. Опционален: синхронные команды без workflow
	// его не несут.
	WorkflowID string `json:"workflow_id,omitempty"`
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
		return fmt.Errorf("contracts: некорректные или отсутствующие обязательные поля envelope: %s", strings.Join(invalid, ", "))
	}
	return nil
}
