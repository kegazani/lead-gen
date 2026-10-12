// Package agentrunstate — опубликованный контракт agent-run-state контекста
// agent-orchestration: чтение наблюдаемого состояния запуска агента компании
// (статус, текущий шаг, шаги с исходами) и списка/ленты запусков для консоли
// операций.
//
// Владелец контракта — agent-orchestration: он владеет агрегатом agent-run.
// Контракт несёт только идентификаторы, коды статусов и нормализованные причины;
// содержания чужих артефактов и персональных данных здесь нет.
//
// Зависимости — только стандартная библиотека.
//
// Транспорт — sync/http-json (query): при co-location вызов прямой (InProc), при
// разделении сервисов — сетевой клиент (httpport). Форма wire — snake_case
// (json-теги ниже), одинаковая у владельца и клиента.
package agentrunstate

import (
	"context"
	"errors"
	"strings"
	"time"
)

// ContractName — имя публикуемого контракта.
const ContractName = "agent-run-state"

// SchemaVersion — текущая версия схемы контракта (semver). Версия 1.1.0 добавила
// чтение списка/ленты запусков (ListRequest/RunSummary/Lister) и явную форму wire;
// поля существующего чтения состояния не удалялись (аддитивно, MINOR).
const SchemaVersion = "1.1.0"

// ErrNotFound — запуск агента не найден в контексте тенанта.
var ErrNotFound = errors.New("agentrunstate: запуск агента не найден")

// ErrUnavailable — владелец состояния недоступен: контракт не подключён или сбой
// транспорта. Потребитель не выдаёт это за пустое состояние.
var ErrUnavailable = errors.New("agentrunstate: владелец состояния недоступен")

// ErrRejected — запрос отклонён по доменной причине владельца (некорректные поля).
// Потребитель различает этот отказ от сбоя транспорта.
var ErrRejected = errors.New("agentrunstate: запрос отклонён владельцем")

// ErrForbidden — владелец отказал в доступе к состоянию/ленте. Сокрытие данных в
// интерфейсе проверку не заменяет: отказ приходит от владельца, а не выводится
// потребителем.
var ErrForbidden = errors.New("agentrunstate: доступ к состоянию запусков запрещён")

// StepState — наблюдаемое состояние одного шага запуска.
type StepState struct {
	// StepID — идентификатор шага в версии пайплайна.
	StepID string `json:"step_id"`
	// BlockRef — ссылка на блок обработки.
	BlockRef string `json:"block_ref"`
	// Order — порядковый номер шага в версии пайплайна.
	Order int `json:"order"`
	// Status — статус шага (код владельца).
	Status string `json:"status"`
	// Attempts — число начатых попыток шага.
	Attempts int `json:"attempts"`
	// Outcome — исход шага: succeeded, failed или unavailable.
	Outcome string `json:"outcome"`
	// Reason — нормализованная причина отказа или недоступности (без секретов).
	Reason string `json:"reason,omitempty"`
}

// RunState — наблюдаемое состояние запуска агента.
type RunState struct {
	RunID           string `json:"run_id"`
	TenantID        string `json:"tenant_id"`
	MissionID       string `json:"mission_id"`
	CompanyID       string `json:"company_id"`
	PipelineID      string `json:"pipeline_id"`
	PipelineVersion int    `json:"pipeline_version"`
	// Status — состояние запуска (код статуса владельца).
	Status string `json:"status"`
	// CurrentStep — шаг, на котором стоит запуск; пусто — запуск терминален.
	CurrentStep string `json:"current_step,omitempty"`
	// Cursor — индекс текущего шага в версии пайплайна.
	Cursor int `json:"cursor"`
	// Steps — состояния шагов в порядке пайплайна.
	Steps []StepState `json:"steps"`
	// AwaitingApproval — запуск ожидает решения оператора по черновику.
	AwaitingApproval bool `json:"awaiting_approval"`
	// DraftRef — непрозрачная ссылка на черновик, ожидающий решения.
	DraftRef string `json:"draft_ref,omitempty"`
	// DraftVersion — версия черновика, к которой относится ожидаемое решение.
	DraftVersion int `json:"draft_version,omitempty"`
}

// RunSummary — сводка запуска для списка/ленты консоли операций: состояния и
// идентификаторы без содержания чужих артефактов.
type RunSummary struct {
	RunID           string `json:"run_id"`
	TenantID        string `json:"tenant_id"`
	MissionID       string `json:"mission_id"`
	CompanyID       string `json:"company_id"`
	PipelineID      string `json:"pipeline_id"`
	PipelineVersion int    `json:"pipeline_version"`
	// Status — состояние запуска (код статуса владельца).
	Status string `json:"status"`
	// CurrentStep — идентификатор текущего шага; пусто — запуск терминален.
	CurrentStep string `json:"current_step,omitempty"`
	// CurrentBlock — ссылка на блок текущего шага; пусто — запуск терминален.
	CurrentBlock string `json:"current_block,omitempty"`
	// Cursor — индекс текущего шага в версии пайплайна.
	Cursor int `json:"cursor"`
	// AwaitingApproval — запуск ожидает решения оператора по черновику.
	AwaitingApproval bool `json:"awaiting_approval"`
	// CreatedAt — время порождения запуска.
	CreatedAt time.Time `json:"created_at"`
	// UpdatedAt — время последнего изменения запуска.
	UpdatedAt time.Time `json:"updated_at"`
}

// ListResult — форма ответа списка/ленты: сводки и их число. Форма wire задана
// здесь и используется и владельцем (HTTP-поверхность), и клиентом (httpport).
type ListResult struct {
	Runs  []RunSummary `json:"runs"`
	Count int          `json:"count"`
}

// Request — запрос чтения состояния запуска.
type Request struct {
	RunID    string
	TenantID string
	// Actor — инициатор чтения; для сетевой поверхности владелец проверяет по нему
	// право. В co-location (InProc) не используется.
	Actor string
}

// Validate проверяет обязательные поля запроса.
func (r Request) Validate() error {
	var invalid []string
	if strings.TrimSpace(r.RunID) == "" {
		invalid = append(invalid, "run_id")
	}
	if strings.TrimSpace(r.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if len(invalid) > 0 {
		return errors.New("agentrunstate: некорректные поля: " + strings.Join(invalid, ", "))
	}
	return nil
}

// ListRequest — запрос списка (ленты) запусков тенанта. Обязателен тенант: запуск
// одного тенанта не виден другому.
type ListRequest struct {
	TenantID string
	// Status — фильтр по статусу запуска (код владельца). Пусто — все статусы.
	Status string
	// Limit — предел числа строк; <= 0 — предел владельца по умолчанию.
	Limit int
	// Actor — инициатор чтения; для сетевой поверхности владелец проверяет по нему
	// право. В co-location (InProc) не используется.
	Actor string
}

// Validate проверяет обязательные поля запроса списка.
func (r ListRequest) Validate() error {
	var invalid []string
	if strings.TrimSpace(r.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if r.Limit < 0 {
		invalid = append(invalid, "limit")
	}
	if len(invalid) > 0 {
		return errors.New("agentrunstate: некорректные поля: " + strings.Join(invalid, ", "))
	}
	return nil
}

// Reader — чтение состояния запуска агента. ErrNotFound — запуск отсутствует,
// ErrUnavailable — сбой транспорта или отсутствие контракта.
type Reader interface {
	RunState(ctx context.Context, request Request) (RunState, error)
}

// Lister — чтение списка (ленты) запусков тенанта. Пустой список — корректный
// результат; недоступность владельца возвращается ошибкой, а не пустым списком.
type Lister interface {
	List(ctx context.Context, request ListRequest) ([]RunSummary, error)
}

// ReadModel — полная read-поверхность контракта: состояние запуска и лента.
type ReadModel interface {
	Reader
	Lister
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
// Незаданная функция честно сообщает о недоступности владельца, а не выдумывает
// результат.
type InProcHandler struct {
	RunStateFunc func(ctx context.Context, request Request) (RunState, error)
	ListFunc     func(ctx context.Context, request ListRequest) ([]RunSummary, error)
}

// RunState реализует Reader.
func (h InProcHandler) RunState(ctx context.Context, request Request) (RunState, error) {
	if h.RunStateFunc == nil {
		return RunState{}, ErrUnavailable
	}
	return h.RunStateFunc(ctx, request)
}

// List реализует Lister.
func (h InProcHandler) List(ctx context.Context, request ListRequest) ([]RunSummary, error) {
	if h.ListFunc == nil {
		return nil, ErrUnavailable
	}
	return h.ListFunc(ctx, request)
}

// InProc заворачивает обработчик в ReadModel для прямого вызова в одном процессе.
func InProc(handler InProcHandler) ReadModel { return handler }
