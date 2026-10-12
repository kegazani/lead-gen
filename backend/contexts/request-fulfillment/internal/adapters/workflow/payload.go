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
	RequestID        string
	TenantID         string
	CorrelationID    string
	LeadTarget       int
	ExecutionPurpose string
	// AttemptID/Generation/CommandID — опциональная identity принятой v2-попытки.
	// Отсутствующие поля в v1 и legacy histories остаются неизвестными и не
	// восполняются из request_id или snapshot.
	AttemptID  string
	Generation int64
	CommandID  string

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

	// CompensationMaxAttempts и CompensationScheduleToClose ограничивают
	// терминальную компенсацию одновременно числом запусков и временем.
	CompensationMaxAttempts     int
	CompensationScheduleToClose time.Duration
}

// TerminalIntent — безопасный marker в Temporal history. Исходная причина
// сохраняется до попытки доменного перехода; PII в структуре нет.
type TerminalIntent struct {
	Version        int
	WorkflowID     string
	RunID          string
	RequestID      string
	TenantID       string
	AttemptID      string
	Generation     int64
	CommandID      string
	ReasonCode     string
	OriginalReason string
	Checksum       string
}

// ApprovalDecision — сигнал утверждения результата перед выпуском (человеческая
// пауза).
type ApprovalDecision struct {
	Approved bool
	Actor    string
}

// DeliveryOutcome — сигнал исхода доставки одного письма заявки (capability
// request-delivery-conclusion). Несёт только идентификатор сообщения и признак
// доставки: текста письма и PII здесь нет. Дедупликация — по MessageID в графе
// исполнения.
type DeliveryOutcome struct {
	// MessageID — идентификатор сообщения message-delivery (ключ дедупликации).
	MessageID string
	// Delivered — true, если письмо доставлено; false — исход недоставки.
	Delivered bool
}

// Status — наблюдаемое состояние workflow (ответ на query status).
// Поля — только фаза и счётчики: содержание оффера и текст писем в состояние не
// попадают.
type Status struct {
	SearchRound      int
	SearchRoundLimit int
	StepLimitSeconds int
	SourcesKnown     bool
	FromPool         int
	Crawled          int
	RequestID        string
	Phase            string
	Found            int
	Drafted          int
	// Target — явно заданная цель заявки по числу компаний (0 — цель не задана):
	// наблюдаемое значение, по которому экран показывает цель рядом с найденным и
	// недобором. Значение по умолчанию здесь не подставляется — оно не цель заявки.
	Target int
	// NoLeads — отдельный наблюдаемый признак исхода «0 лидов»: заявка не может
	// стать fulfilled молча, без явного одобрения оператора.
	NoLeads bool
	// Shortfall — число компаний, которых не хватает до цели заявки
	// (`max(0, цель − уникальные найденные)`). nil означает, что явная цель не
	// задана: недобор не рассчитывается и не показывается выдуманным нулём.
	Shortfall *int
	// PartialReason — безопасная причина неполного поиска, если Firecrawl этап
	// завершился частично.
	PartialReason string
	// Handed — число писем, переданных в очередь утверждения outreach-orchestration.
	Handed int
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
	// Reason — текст сообщения оператору при сбое (FailRequest). Для отмены
	// (CancelRequest) используется ReasonCode как читаемая причина.
	Reason string
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
	Origins       []string
	SourcesKnown  bool
	Found         int
	LeadIDs       []string
	PartialReason string
}

// draftInput — вход activity шага генерации черновиков писем. Только
// идентификаторы: текст оффера activity читает из заявки сама.
type draftInput struct {
	RequestID string
	TenantID  string
	LeadIDs   []string
	Editorial bool
}

// draftResult — результат шага генерации черновиков: сколько писем сохранено,
// сколько лидов пропущено и под какие лиды есть черновик. Текста писем в payload
// нет: он лежит в хранилище черновиков.
type draftResult struct {
	Drafted int
	Failed  int
	LeadIDs []string
}

// handoffInput — вход activity шага передачи черновиков писем. Только
// идентификаторы: черновики activity читает из хранилища сама, а текст писем в
// payload не поднимается.
type handoffInput struct {
	RequestID string
	TenantID  string
	Actor     string
	Name      string
}

// handoffOutcome — результат шага передачи: сколько писем передано в очередь
// утверждения, сколько пропущено, был ли это повтор, а также идентификатор
// кампании и число реально переданных писем (для ожидания доставки). Носителей
// текста нет.
type handoffOutcome struct {
	Handed     int
	Skipped    int
	Repeated   bool
	CampaignID string
	Expected   int
}
