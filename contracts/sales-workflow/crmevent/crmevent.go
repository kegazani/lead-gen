// Package crmevent объявляет опубликованный контракт crm-event контекста
// sales-workflow: наблюдаемое событие сделки во внешней CRM.
//
// Контракт описан на языке границы: он приходит в контекст от внешней CRM (через
// integration-platform, ACL, contract provider-port) и несёт минимальные
// идентификаторы и ссылки — без персональных данных и внутренних типов владельца.
// Зависимости — стандартная библиотека и общий пакет envelope.
//
// Транспорт — sync/http-json (команда приёма, роль ingest): при co-location
// вызов прямой, при разделении сервисов — сетевой; выбирает точка сборки по
// топологии. Контракт служит входом политик conflict-resolution и
// confirmed-attribution: событие с подтверждением результата несёт доказательство
// атрибуции.
package crmevent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/contracts/sales-workflow/envelope"
)

// ContractName — имя контракта.
const ContractName = "crm-event"

// SchemaVersion — текущая версия схемы. Изменяется по правилам semver из пакета
// envelope.
const SchemaVersion = "1.0.0"

// Имена этапов, которые внешняя CRM наблюдает в сделке. Значения совпадают с
// доменным VO deal-stage владельца. Набор закрыт.
const (
	StageLead        = "lead"
	StageQualified   = "qualified"
	StageProposal    = "proposal"
	StageNegotiation = "negotiation"
	StageWon         = "won"
	StageLost        = "lost"
)

var allowedStages = map[string]struct{}{
	StageLead: {}, StageQualified: {}, StageProposal: {},
	StageNegotiation: {}, StageWon: {}, StageLost: {},
}

// IsKnownStage сообщает, что имя этапа входит в закрытый набор контракта.
func IsKnownStage(stage string) bool {
	_, ok := allowedStages[strings.ToLower(strings.TrimSpace(stage))]
	return ok
}

// ErrRejected — событие отвергнуто владельцем по доменной причине (например,
// неполное доказательство атрибуции). Потребитель различает этот отказ от сбоя
// транспорта.
var ErrRejected = errors.New("crmevent: событие отвергнуто владельцем")

// ErrUnavailable — владелец недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за успех.
var ErrUnavailable = errors.New("crmevent: владелец недоступен")

// CRMEvent — наблюдаемое событие сделки во внешней CRM.
type CRMEvent struct {
	envelope.Envelope

	// DealID — идентификатор сделки в контексте.
	DealID string `json:"deal_id"`
	// CompanyRef — непрозрачная ссылка на карточку компании.
	CompanyRef string `json:"company_ref"`
	// ExternalRef — идентификатор записи сделки во внешней CRM (идемпотентная
	// внешняя ссылка).
	ExternalRef string `json:"external_ref"`
	// ExternalRevision — ревизия записи во внешней CRM (>= 1).
	ExternalRevision int64 `json:"external_revision"`
	// Stage — наблюдаемый внешний этап сделки.
	Stage string `json:"stage"`
	// ResultConfirmed — событие подтверждает результат сделки. Тогда обязательны
	// ActionRef и EvidenceRef.
	ResultConfirmed bool `json:"result_confirmed,omitempty"`
	// ActionRef — исходящее действие, чей результат подтверждается.
	ActionRef string `json:"action_ref,omitempty"`
	// EvidenceRef — проверяемое основание связи результата с действием.
	EvidenceRef string `json:"evidence_ref,omitempty"`
	// Source — актор-источник события (необязательно).
	Source string `json:"source,omitempty"`
}

// NewCRMEvent возвращает событие с проставленными именем и версией схемы.
func NewCRMEvent(env envelope.Envelope) CRMEvent {
	env.Name = ContractName
	env.SchemaVersion = SchemaVersion
	return CRMEvent{Envelope: env}
}

// Validate проверяет конверт и обязательные поля события, собирая все нарушения.
// Признак подтверждения результата требует доказательства атрибуции.
func (e CRMEvent) Validate() error {
	if err := e.Envelope.Validate(); err != nil {
		return err
	}
	var invalid []string
	if strings.TrimSpace(e.DealID) == "" {
		invalid = append(invalid, "deal_id")
	}
	if strings.TrimSpace(e.ExternalRef) == "" {
		invalid = append(invalid, "external_ref")
	}
	if e.ExternalRevision < 1 {
		invalid = append(invalid, "external_revision")
	}
	if !IsKnownStage(e.Stage) {
		invalid = append(invalid, "stage")
	}
	if e.ResultConfirmed {
		if strings.TrimSpace(e.ActionRef) == "" {
			invalid = append(invalid, "action_ref")
		}
		if strings.TrimSpace(e.EvidenceRef) == "" {
			invalid = append(invalid, "evidence_ref")
		}
	}
	if e.Name != ContractName {
		invalid = append(invalid, "name")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("crmevent: crm-event: некорректные или отсутствующие обязательные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// IngestResult — исход приёма события владельцем.
type IngestResult struct {
	// Applied — применено ли событие к сделке. false означает, что событие
	// устарело или конфликтует.
	Applied bool `json:"applied"`
	// SyncStatus — состояние синхронизации после приёма.
	SyncStatus string `json:"sync_status"`
	// Stage — этап сделки после приёма (пустой, если событие не применено).
	Stage string `json:"stage,omitempty"`
	// Version — версия сделки после приёма.
	Version int `json:"version,omitempty"`
}

// Handler — приём события владельцем контракта. Реализация применяет событие к
// сделке и синхронизации; ErrRejected — доменный отказ, ErrUnavailable — сбой
// транспорта или отсутствие контракта.
type Handler interface {
	Ingest(ctx context.Context, event CRMEvent) (IngestResult, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	// IngestFunc реализует приём события.
	IngestFunc func(ctx context.Context, event CRMEvent) (IngestResult, error)
}

// Ingest реализует Handler. Незаданная функция честно сообщает о недоступности
// владельца.
func (h InProcHandler) Ingest(ctx context.Context, event CRMEvent) (IngestResult, error) {
	if h.IngestFunc == nil {
		return IngestResult{}, ErrUnavailable
	}
	return h.IngestFunc(ctx, event)
}

// InProc заворачивает обработчик в Handler для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Handler { return handler }
