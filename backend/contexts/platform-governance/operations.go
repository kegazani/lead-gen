package platformgovernance

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/contracts/agent-orchestration/agentrunstate"
)

// Источник данных экрана операций (capability operations-cockpit).
//
// Экран композирует данные разных владельцев (design.md, Решение 8): цифры и
// агрегаты процесса — от аналитики, лента и подробности агентов — от
// agent-orchestration, процессы Temporal — отдельным техническим примитивом
// (WorkflowReader). Здесь собирается источник контура агентов и аналитики поверх
// опубликованных read-поверхностей владельцев.
//
// Недоступность владельца — честная ошибка, которую экран показывает как
// «недоступно», а не нули и не пустой успех (AGENTS.md §3, design.md Решение 10).

// ErrOperationsUnavailable — источник экрана операций не подключён или отказал.
// Экран отвечает честной недоступностью, а не выдуманными значениями.
var ErrOperationsUnavailable = errors.New("platform-governance: источник операций недоступен")

// AgentRunQuery — фильтр ленты агентов: тенант, клиент, категория и предел строк.
type AgentRunQuery struct {
	TenantID string
	ClientID string
	Category string
	Limit    int
}

// AgentRunSummary — строка ленты агентов: обезличенные идентификаторы, код
// состояния и текущий шаг, время обновления. Название компании берётся из зелёной
// зоны; персональных данных и содержимого красной зоны здесь нет.
type AgentRunSummary struct {
	RunID       string
	TenantID    string
	ClientID    string
	CompanyID   string
	Company     string
	State       string
	CurrentStep string
	UpdatedAt   time.Time
}

// AgentRunFeed — лента запусков агентов по тенанту и фильтру. Владелец —
// agent-orchestration.
//
// Публикуемый контракт agent-run-state сейчас описывает чтение одного запуска
// (Reader.RunState); перечень запусков (лента) — требуемое расширение
// read-поверхности владельца. До его публикации поле OperationsInput.Feed остаётся
// nil, и лента честно недоступна, а не подменяется пустым успехом.
type AgentRunFeed interface {
	Runs(ctx context.Context, query AgentRunQuery) ([]AgentRunSummary, error)
}

// OperationsAggregates — агрегаты процесса по клиентам (владелец analytics): цифры
// процесса и агрегаты полноэкранного дашборда. Только обезличенные счётчики, без
// персональных данных и содержимого красной зоны (design.md Решение 11).
type OperationsAggregates interface {
	Numbers(ctx context.Context, window time.Duration) (httpapi.ClientNumbersResult, error)
	Clients(ctx context.Context, window time.Duration) ([]httpapi.ClientAggregate, error)
}

// OperationsInput — read-поверхности владельцев, из которых собирается источник
// экрана операций. Незаданное поле означает, что соответствующий владелец не
// подключён: его раздел экрана честно «недоступен», остальные разделы работают.
type OperationsInput struct {
	// Feed — лента запусков агентов (владелец agent-orchestration).
	Feed AgentRunFeed
	// RunState — состояние одного запуска: опубликованный контракт agent-run-state
	// (владелец agent-orchestration). In-proc — через agentrunstate.InProc.
	RunState agentrunstate.Reader
	// Aggregates — цифры процесса и агрегаты по клиентам (владелец analytics).
	Aggregates OperationsAggregates
}

// NewOperationsSource собирает источник экрана операций поверх read-поверхностей
// владельцев. Источник всегда не-nil: незаданная поверхность даёт честную ошибку
// (ErrOperationsUnavailable) только в своём разделе, не ломая остальной экран
// (спека operations-cockpit, требование расширяемых панелей).
func NewOperationsSource(in OperationsInput) httpapi.OperationsData {
	return &operationsSource{feed: in.Feed, runState: in.RunState, aggregates: in.Aggregates}
}

// operationsSource — реализация httpapi.OperationsData поверх владельцев.
type operationsSource struct {
	feed       AgentRunFeed
	runState   agentrunstate.Reader
	aggregates OperationsAggregates
}

// Numbers возвращает цифры процесса по клиентам (владелец analytics).
func (s *operationsSource) Numbers(ctx context.Context, window time.Duration) (httpapi.ClientNumbersResult, error) {
	if s.aggregates == nil {
		return httpapi.ClientNumbersResult{}, ErrOperationsUnavailable
	}
	return s.aggregates.Numbers(ctx, window)
}

// Clients возвращает агрегаты по клиентам для полноэкранного дашборда (владелец
// analytics).
func (s *operationsSource) Clients(ctx context.Context, window time.Duration) ([]httpapi.ClientAggregate, error) {
	if s.aggregates == nil {
		return nil, ErrOperationsUnavailable
	}
	return s.aggregates.Clients(ctx, window)
}

// Agents возвращает ленту запусков агентов (владелец agent-orchestration).
func (s *operationsSource) Agents(ctx context.Context, filter httpapi.AgentFilter) ([]httpapi.AgentRow, error) {
	if s.feed == nil {
		return nil, ErrOperationsUnavailable
	}
	runs, err := s.feed.Runs(ctx, AgentRunQuery{
		TenantID: filter.TenantID,
		ClientID: filter.ClientID,
		Category: filter.Category,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	rows := make([]httpapi.AgentRow, 0, len(runs))
	for _, run := range runs {
		rows = append(rows, httpapi.AgentRow{
			RunID:     run.RunID,
			ClientID:  firstNonEmpty(run.ClientID, run.TenantID),
			Company:   firstNonEmpty(run.Company, run.CompanyID),
			State:     run.State,
			Step:      run.CurrentStep,
			UpdatedAt: run.UpdatedAt,
		})
	}
	return rows, nil
}

// AgentDetail читает подробности запуска из опубликованного контракта agent-run-state
// и переводит его в представление экрана. Отказ шага отображается отказом, а не
// успехом; недоступность шага — отдельным честным исходом.
func (s *operationsSource) AgentDetail(ctx context.Context, tenantID, runID string) (httpapi.AgentDetail, error) {
	if s.runState == nil {
		return httpapi.AgentDetail{}, ErrOperationsUnavailable
	}
	request := agentrunstate.Request{RunID: runID, TenantID: tenantID}
	if err := request.Validate(); err != nil {
		return httpapi.AgentDetail{}, err
	}
	state, err := s.runState.RunState(ctx, request)
	if err != nil {
		return httpapi.AgentDetail{}, err
	}
	return detailFromRunState(state), nil
}

// Коды исхода шага. Владелец отдаёт исходы succeeded/failed/unavailable (контракт
// agent-run-state); представление экрана работает с нейтральными кодами
// platform/workflow. Отказ и недоступность не маскируются под успех.
const (
	ownerOutcomeSucceeded   = "succeeded"
	ownerOutcomeFailed      = "failed"
	ownerOutcomeUnavailable = "unavailable"

	cockpitOutcomeCompleted   = "completed"
	cockpitOutcomeFailed      = "failed"
	cockpitOutcomeUnavailable = "unavailable"
	cockpitStepKindActivity   = "activity"
)

// detailFromRunState переводит состояние запуска контракта agent-run-state в
// представление подробностей. События строятся только по шагам с терминальным
// исходом: незавершённый шаг события не имеет. Время шага контрактом сейчас не
// отдаётся (требуемое расширение, см. отчёт): событие без времени, а не выдуманная
// метка. Связанные задания контрактом не отдаются: список пуст, и экран честно
// сообщает «связанных заданий нет», а не выдумывает их.
func detailFromRunState(state agentrunstate.RunState) httpapi.AgentDetail {
	detail := httpapi.AgentDetail{
		RunID:    state.RunID,
		ClientID: state.TenantID,
		Company:  state.CompanyID,
		State:    state.Status,
	}
	reasons := map[string]int{}
	for _, step := range state.Steps {
		outcome := strings.TrimSpace(step.Outcome)
		if outcome == "" {
			continue
		}
		detail.Events = append(detail.Events, httpapi.AgentEvent{
			Step:    firstNonEmpty(step.StepID, step.BlockRef),
			Kind:    cockpitStepKindActivity,
			Outcome: cockpitOutcome(outcome),
			Reason:  strings.TrimSpace(step.Reason),
		})
		if outcome != ownerOutcomeSucceeded {
			if code := strings.TrimSpace(step.Reason); code != "" {
				reasons[code]++
			}
		}
	}
	detail.Reasons = sortedReasons(reasons)
	return detail
}

// cockpitOutcome переводит исход владельца в код исхода представления. Отказ и
// недоступность не маскируются под успех.
func cockpitOutcome(outcome string) string {
	switch outcome {
	case ownerOutcomeSucceeded:
		return cockpitOutcomeCompleted
	case ownerOutcomeFailed:
		return cockpitOutcomeFailed
	case ownerOutcomeUnavailable:
		return cockpitOutcomeUnavailable
	default:
		return outcome
	}
}

// sortedReasons упорядочивает нормализованные причины отказов по убыванию числа,
// затем по коду: вывод детерминирован.
func sortedReasons(reasons map[string]int) []httpapi.AgentReason {
	if len(reasons) == 0 {
		return nil
	}
	out := make([]httpapi.AgentReason, 0, len(reasons))
	for code, count := range reasons {
		out = append(out, httpapi.AgentReason{Code: code, Count: count})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// firstNonEmpty возвращает первое непустое значение.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
