package ports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/platform/jobs"
)

// ExportFormat — формат файла выгрузки отчёта. Формат фиксируется на момент
// постановки задания: длительная операция не переспрашивает оператора.
type ExportFormat string

const (
	// ExportCSV — выгрузка таблицей CSV с BOM.
	ExportCSV ExportFormat = "csv"
	// ExportXLSX — выгрузка книгой XLSX (Office Open XML).
	ExportXLSX ExportFormat = "xlsx"
)

// Valid сообщает, что формат известен.
func (f ExportFormat) Valid() bool {
	switch f {
	case ExportCSV, ExportXLSX:
		return true
	default:
		return false
	}
}

// ReportExportRequest — запрос выгрузки отчёта, поставленный в очередь длительной
// операции. Срез (тенант, период, измерения) фиксируется на момент постановки, а не
// пересчитывается по запросу клиента: результат задания не должен зависеть от
// состояния фильтра в браузере.
type ReportExportRequest struct {
	// TenantID — тенант среза.
	TenantID string
	// Role — роль клиента, запросившего выгрузку.
	Role string
	// From и To — границы периода; нулевое время означает отсутствие границы.
	From time.Time
	To   time.Time
	// Segment, Source, CampaignID — измерения среза.
	Segment    string
	Source     string
	CampaignID string
	// Format — формат файла.
	Format ExportFormat
}

// Validate проверяет, что запрос выгрузки пригоден к постановке.
func (r ReportExportRequest) Validate() error {
	if !r.Format.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidExportFormat, r.Format)
	}
	return nil
}

// Максимальное число попыток задания выгрузки и предел страницы DLQ. Порог
// задаётся здесь, чтобы раннер роли worker (согласование осиротевших заданий) и
// экран DLQ применяли один и тот же рубеж исчерпания попыток.
const (
	// ExportJobMaxAttempts — предел попыток прерванного задания: при исчерпании
	// оно завершается отказом и становится видно в DLQ.
	ExportJobMaxAttempts = 2
	// ExportJobDLQLimit — предел числа строк очереди исчерпавших попытки задач.
	ExportJobDLQLimit = 50
)

// ReportExport — операционная запись задания выгрузки: то, что наблюдает оператор.
// Тело готового файла сюда не входит: ссылка на результат отдаётся отдельно.
type ReportExport struct {
	// ID — идентификатор задания в модели platform/jobs.
	ID string
	// Format — формат файла выгрузки.
	Format ExportFormat
	// Status — состояние задания (queued/running/done/failed/cancelled).
	Status jobs.Status
	// Error — нормализованная бизнес-причина отказа (пусто, если задание не failed).
	// Технические детали сюда не попадают: они уходят только в лог.
	Error string
	// ErrorCode — стабильный машинный код отказа.
	ErrorCode string
	// ResultAvailable — результат сформирован, ссылка на файл действует.
	ResultAvailable bool
	// Progress — доля выполнения задания в процентах, 0..100. 100 у завершённого.
	Progress int
	// Step — текущий шаг обработки в формулировке для оператора (пусто, если шаг
	// не сообщён).
	Step string
	// StartedAt — момент взятия задания в обработку; нулевое, пока задание в
	// очереди.
	StartedAt time.Time
	// CreatedAt — момент постановки.
	CreatedAt time.Time
	// UpdatedAt — момент последнего изменения состояния.
	UpdatedAt time.Time
}

// ReportExportResult — готовый файл выгрузки: тело с типом и именем, который
// отдаётся по ссылке на результат.
type ReportExportResult struct {
	// ContentType — MIME-тип файла.
	ContentType string
	// Filename — имя файла для скачивания.
	Filename string
	// Body — тело файла.
	Body []byte
}

// ReportExportStore — хранилище заданий выгрузки отчёта. Реализует общий интерфейс
// jobs.Store (раннер worker: взять queued, обновить статус, запросить отмену) и
// расширения примитива: отчёт о ходе (jobs.ProgressStore), список заданий
// (jobs.JobLister) и явный повтор упавшего (jobs.JobRetrier). Последние нужны
// модели «команда → идентификатор → прогресс → результат» и видимости DLQ.
// Дальше — операции длительной операции экспорта: постановка, запись готового
// файла и чтение статуса/результата.
type ReportExportStore interface {
	jobs.Store
	jobs.ProgressStore
	jobs.JobLister
	jobs.JobRetrier

	// CreateReportExport ставит задание выгрузки с зафиксированным запросом и
	// возвращает его операционную запись. Идентификатор генерирует хранилище.
	CreateReportExport(ctx context.Context, req ReportExportRequest) (ReportExport, error)
	// RecordExportResult сохраняет готовый файл задания, не меняя статус.
	// Отсутствующее задание — ErrReportExportNotFound.
	RecordExportResult(ctx context.Context, id string, result ReportExportResult) error
	// ExportByID возвращает операционную запись задания. Отсутствующее —
	// ErrReportExportNotFound.
	ExportByID(ctx context.Context, id string) (ReportExport, error)
	// ExportResult возвращает готовый файл задания. Отсутствующее задание —
	// ErrReportExportNotFound, несформированный результат — ErrReportExportNoResult.
	ExportResult(ctx context.Context, id string) (ReportExportResult, error)
}

var (
	// ErrReportExportNotFound — задания выгрузки с таким идентификатором нет.
	ErrReportExportNotFound = errors.New("ports: задание выгрузки не найдено")
	// ErrReportExportNoResult — результат задания ещё не сформирован.
	ErrReportExportNoResult = errors.New("ports: результат выгрузки ещё не сформирован")
	// ErrInvalidExportFormat — неизвестный формат выгрузки.
	ErrInvalidExportFormat = errors.New("ports: неизвестный формат выгрузки")
)

// ReportExportPayload — wire-формат непрозрачного Payload задания выгрузки. Формат
// фиксирован здесь, чтобы кодирование (адаптеры хранилища) и декодирование
// (прикладной слой) не расходились. Границы периода необязательны: отсутствие
// поля означает отсутствие границы.
type ReportExportPayload struct {
	// TenantID — тенант среза.
	TenantID string `json:"tenantId,omitempty"`
	// Role — роль клиента, запросившего выгрузку.
	Role string `json:"role,omitempty"`
	// From — начало периода.
	From time.Time `json:"from,omitempty"`
	// To — конец периода.
	To time.Time `json:"to,omitempty"`
	// Segment — измерение сегмента.
	Segment string `json:"segment,omitempty"`
	// Source — измерение источника.
	Source string `json:"source,omitempty"`
	// CampaignID — измерение кампании.
	CampaignID string `json:"campaignId,omitempty"`
	// Format — формат файла.
	Format ExportFormat `json:"format"`
}

// Request восстанавливает запрос выгрузки из wire-формата.
func (p ReportExportPayload) Request() ReportExportRequest {
	return ReportExportRequest{
		TenantID:   p.TenantID,
		Role:       p.Role,
		From:       p.From,
		To:         p.To,
		Segment:    p.Segment,
		Source:     p.Source,
		CampaignID: p.CampaignID,
		Format:     p.Format,
	}
}

// ReportExportPayloadFromRequest кодирует запрос выгрузки в wire-формат.
func ReportExportPayloadFromRequest(req ReportExportRequest) ReportExportPayload {
	return ReportExportPayload{
		TenantID:   req.TenantID,
		Role:       req.Role,
		From:       req.From,
		To:         req.To,
		Segment:    req.Segment,
		Source:     req.Source,
		CampaignID: req.CampaignID,
		Format:     req.Format,
	}
}

// EncodeReportExportPayload кодирует запрос выгрузки в непрозрачный Payload задания.
func EncodeReportExportPayload(p ReportExportPayload) ([]byte, error) {
	return json.Marshal(p)
}

// DecodeReportExportPayload извлекает запрос выгрузки из Payload задания.
func DecodeReportExportPayload(payload []byte) (ReportExportPayload, error) {
	var p ReportExportPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return ReportExportPayload{}, err
	}
	return p, nil
}
