// Package agentcommand — опубликованный контракт agent-command контекста
// agent-orchestration: управляющие команды агенту компании (запуск, остановка,
// повтор шага, решение оператора по запуску в ожидании утверждения).
//
// Владелец контракта — agent-orchestration (architecture/domain-catalog.yaml):
// он владеет супервизором и состоянием запуска агента. Контракт несёт только
// идентификаторы (задание, компания, тенант, запуск) и короткие коды: содержания
// писем, карточек и персональных данных здесь нет (payload-гигиена,
// data-governance).
//
// Зависимости — только стандартная библиотека. Владелец реализует Port, а
// потребитель (операторская консоль, расписание) зависит от контракта, не видя
// реализации.
//
// Транспорт — sync/http-json (команда): при co-location вызов прямой (InProc),
// при разделении сервисов — сетевой клиент; выбирает точка сборки по топологии.
package agentcommand

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ContractName — имя публикуемого контракта. Совпадает с объявленным в каталоге
// доменной архитектуры.
const ContractName = "agent-command"

// SchemaVersion — текущая версия схемы контракта (semver). Версия `1.1.0`
// аддитивна: добавлены действия решения `approve`/`reject` и поля `draft_ref`,
// `draft_version`; существующие действия и поля не изменены.
const SchemaVersion = "1.1.0"

// Action — управляющее действие над агентом компании.
type Action string

const (
	// ActionStart — запустить агента для компании в контексте задания.
	ActionStart Action = "start"
	// ActionStop — остановить агента (терминальное состояние «остановлен»).
	ActionStop Action = "stop"
	// ActionRetry — повторить текущий неуспешный шаг в пределах числа попыток.
	ActionRetry Action = "retry"
	// ActionApprove — утвердить запуск, стоящий в ожидании решения, для точной
	// версии ожидающего черновика и возобновить исполнение.
	ActionApprove Action = "approve"
	// ActionReject — отклонить черновик запуска, стоящего в ожидании решения:
	// отправка не выполняется, запуск требует вмешательства.
	ActionReject Action = "reject"
)

// ErrRejected — команда отклонена по доменной причине владельца (некорректные
// поля, неизвестное действие, недоступный переход). Потребитель различает этот
// отказ от сбоя транспорта.
var ErrRejected = errors.New("agentcommand: команда отклонена владельцем")

// ErrUnavailable — владелец недоступен: контракт не подключён, сбой транспорта
// или рантайма исполнения. Потребитель не выдаёт это за принятую команду.
var ErrUnavailable = errors.New("agentcommand: владелец команд недоступен")

// Command — управляющая команда агенту. Набор полей зависит от действия; общие
// обязательные — RunID (кроме start), TenantID и Actor.
type Command struct {
	// Action — действие над агентом.
	Action Action
	// RunID — идентификатор запуска агента. Обязателен для stop и retry; для
	// start владелец сам порождает идентификатор, если поле пусто.
	RunID string
	// TenantID — тенант-владелец агента. Обязателен.
	TenantID string
	// MissionID — задание (поисковый контур), в котором обрабатывается компания.
	// Обязателен для start.
	MissionID string
	// CompanyID — обрабатываемая компания. Обязателен для start.
	CompanyID string
	// PipelineID — пайплайн, под который порождается агент. Обязателен для start.
	PipelineID string
	// PipelineVersion — версия пайплайна. Ноль означает последнюю опубликованную.
	PipelineVersion int
	// Criteria — краткое описание задания (цель поиска) для блоков агента. Не
	// несёт персональных данных.
	Criteria string
	// Actor — инициатор команды (оператор или расписание). Может быть пустым для
	// машинной команды.
	Actor string
	// DraftRef — непрозрачная ссылка на ожидающий черновик. Обязательна для
	// решений approve/reject: решение адресуется точной версии черновика.
	DraftRef string
	// DraftVersion — точная версия черновика, к которой относится решение.
	// Обязательна и положительна для approve/reject.
	DraftVersion int
	// Reason — нормализованная причина остановки или отказа. Обязательна для
	// stop и reject.
	Reason string
	// CorrelationID — сквозной идентификатор сценария.
	CorrelationID string
	// CausationID — идентификатор команды или события, вызвавшего эту команду.
	CausationID string
}

// Validate проверяет обязательные поля команды и называет все нарушения разом.
func (c Command) Validate() error {
	var invalid []string
	switch c.Action {
	case ActionStart:
		if strings.TrimSpace(c.MissionID) == "" {
			invalid = append(invalid, "mission_id")
		}
		if strings.TrimSpace(c.CompanyID) == "" {
			invalid = append(invalid, "company_id")
		}
		if strings.TrimSpace(c.PipelineID) == "" {
			invalid = append(invalid, "pipeline_id")
		}
	case ActionStop:
		if strings.TrimSpace(c.RunID) == "" {
			invalid = append(invalid, "run_id")
		}
		if strings.TrimSpace(c.Reason) == "" {
			invalid = append(invalid, "reason")
		}
	case ActionRetry:
		if strings.TrimSpace(c.RunID) == "" {
			invalid = append(invalid, "run_id")
		}
	case ActionApprove:
		if strings.TrimSpace(c.RunID) == "" {
			invalid = append(invalid, "run_id")
		}
		if strings.TrimSpace(c.DraftRef) == "" {
			invalid = append(invalid, "draft_ref")
		}
		if c.DraftVersion <= 0 {
			invalid = append(invalid, "draft_version")
		}
	case ActionReject:
		if strings.TrimSpace(c.RunID) == "" {
			invalid = append(invalid, "run_id")
		}
		if strings.TrimSpace(c.DraftRef) == "" {
			invalid = append(invalid, "draft_ref")
		}
		if c.DraftVersion <= 0 {
			invalid = append(invalid, "draft_version")
		}
		if strings.TrimSpace(c.Reason) == "" {
			invalid = append(invalid, "reason")
		}
	case "":
		invalid = append(invalid, "action")
	default:
		invalid = append(invalid, "action")
	}
	if strings.TrimSpace(c.TenantID) == "" {
		invalid = append(invalid, "tenant_id")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("%w: некорректные поля: %s", ErrRejected, strings.Join(invalid, ", "))
	}
	return nil
}

// Result — наблюдаемый исход команды.
type Result struct {
	// RunID — идентификатор запуска, к которому относится команда.
	RunID string
	// Status — состояние запуска после команды (код статуса владельца).
	Status string
	// Accepted — команда принята владельцем.
	Accepted bool
	// Repeated — признак идемпотентного повтора: команда с тем же эффектом уже
	// была применена, второй эффект не создавался.
	Repeated bool
}

// Port — команда владельцу. ErrRejected — доменный отказ, ErrUnavailable — сбой
// транспорта или рантайма.
type Port interface {
	Command(ctx context.Context, command Command) (Result, error)
}

// InProcHandler — функция прямого вызова владельца для co-location и тестов.
type InProcHandler struct {
	CommandFunc func(ctx context.Context, command Command) (Result, error)
}

// Command реализует Port. Незаданная функция честно сообщает о недоступности
// владельца, а не выдумывает принятую команду.
func (h InProcHandler) Command(ctx context.Context, command Command) (Result, error) {
	if h.CommandFunc == nil {
		return Result{}, ErrUnavailable
	}
	return h.CommandFunc(ctx, command)
}

// InProc заворачивает обработчик в Port для прямого вызова в одном процессе.
func InProc(handler InProcHandler) Port { return handler }
