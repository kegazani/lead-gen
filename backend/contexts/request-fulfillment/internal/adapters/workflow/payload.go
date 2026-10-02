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

// Params — вход workflow фулфилмента. Только идентификаторы.
type Params struct {
	RequestID     string
	TenantID      string
	CorrelationID string
	LeadTarget    int
}

// ApprovalDecision — сигнал утверждения результата перед выпуском (человеческая
// пауза).
type ApprovalDecision struct {
	Approved bool
	Actor    string
}

// Status — наблюдаемое состояние workflow (ответ на query status).
type Status struct {
	RequestID string
	Phase     string
	Found     int
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

// searchInput — вход activity шага поиска (заглушка).
type searchInput struct {
	RequestID string
	TenantID  string
	Target    int
	Round     int
}

// searchResult — результат шага поиска: сколько лидов найдено.
type searchResult struct {
	Found int
}
