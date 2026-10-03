// Package workflowadapter — durable-исполнение фулфилмента заявки на Temporal.
//
// Workflow «фулфилмент заявки» живёт в owning context request-fulfillment и
// двигает заявку по статусной машине, вызывая activities. Activities — тонкие
// адаптеры к application services контекста: доменной логики в них нет,
// допустимость перехода решает aggregate root, инварианты — прикладной слой.
//
// Раскладка пакета — adapters, а не app: workflow-код импортирует SDK Temporal
// (внешний модуль), а внешние модули в контексте допустимы только в adapters
// (tools/importlint). При этом владение графом исполнения остаётся за контекстом,
// а не за платформенным пакетом.
//
// Payload-гигиена: аргументы workflow и activities несут только идентификаторы и
// коды (см. platform/workflow.Rule).
package workflowadapter

import (
	"time"
)

// Params — вход workflow фулфилмента.

// Params — вход workflow фулфилмента. Только идентификаторы и политики исполнения
// (числа и длительности): оффер и PII в payload не поднимаются.
type Params struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	LeadTarget    int

	// Policy — таймауты и сроки исполнения. Нулевые поля заполняются
	// осмысленными значениями по умолчанию в workflow.
	Policy ExecutionPolicy
}

// ExecutionPolicy — конфигурация графа исполнения заявки: срок паузы утверждения
// и таймауты activities. Несёт только числа и длительности (не PII, не секреты),
// поэтому передаётся во вход workflow и делает опции воспроизводимыми при
// реплее: тот же вход — те же опции.
type ExecutionPolicy struct {
	// ApprovalTTL — срок паузы утверждения результата. По истечении заявка
	// переводится в cancelled.
	ApprovalTTL time.Duration

	// ExecutionTimeout — предельное время исполнения заявки. Согласован со сроком
	// паузы: не меньше ApprovalTTL, чтобы таймер паузы успел сработать.
	ExecutionTimeout time.Duration

	// TransitionStartToClose — StartToCloseTimeout коротких activities-переходов.
	TransitionStartToClose time.Duration

	// TransitionMaxAttempts — число попыток коротких transitions.
	TransitionMaxAttempts int

	// LongStartToClose — StartToCloseTimeout длинных activities (поиск, письма).
	LongStartToClose time.Duration

	// LongHeartbeat — HeartbeatTimeout контроля жизни длинной activity.
	LongHeartbeat time.Duration

	// LongMaxAttempts — число попыток длинных внешних шагов; ограничено, чтобы
	// таймаут не приводил к каскаду повторных краулов и LLM-вызовов.
	LongMaxAttempts int
}

// ApprovalDecision — сигнал утверждения результата перед выпуском (человеческая
// пауза).
type ApprovalDecision struct {
	Approved bool
	Actor    string
}

// Status — наблюдаемое состояние workflow (ответ на query status).
// Поля — только фаза и счётчики: содержание оффера и текст писем в состояние не
// попадают.
type Status struct {
	RequestID string
	Phase     string
	Found     int
	Drafted   int
	// NoLeads — отдельный наблюдаемый признак исхода «0 лидов»: заявка не может
	// стать fulfilled молча, без явного одобрения оператора.
	NoLeads bool
}

// transitionInput — вход activity-перехода статуса. Только идентификаторы и
// короткий код причины.
type transitionInput struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	CausationID   string
	Actor         string
	ReasonCode    string
}

// searchInput — вход activity шага поиска.
type searchInput struct {
	RequestID string
	TenantID  string
	Target    int
	Round     int
}

// searchResult — результат шага поиска: сколько лидов найдено и идентификаторы
// найденных компаний. Только идентификаторы: карточек и PII в payload нет.
type searchResult struct {
	Found   int
	LeadIDs []string
}

// draftInput — вход activity шага генерации черновиков писем. Только
// идентификаторы: текст оффера activity читает из заявки сама.
type draftInput struct {
	RequestID string
	TenantID  string
	LeadIDs   []string
}

// draftResult — результат шага генерации черновиков: сколько писем сохранено,
// сколько лидов пропущено и под какие лиды есть черновик. Текста писем в payload
// нет: он лежит в хранилище черновиков.
type draftResult struct {
	Drafted int
	Failed  int
	LeadIDs []string
}
