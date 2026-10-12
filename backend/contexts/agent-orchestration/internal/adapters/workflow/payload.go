package workflowadapter

import "time"

// Params — вход workflow запуска агента. Только идентификаторы и политика
// исполнения (числа и длительности): содержания чужих артефактов и персональных
// данных в payload нет.
type Params struct {
	RunID           string
	TenantID        string
	MissionID       string
	CompanyID       string
	PipelineID      string
	PipelineVersion int
	Criteria        string
	CorrelationID   string

	// Policy — таймауты и сроки исполнения. Нулевые поля заполняются значениями по
	// умолчанию в workflow.
	Policy ExecutionPolicy
}

// ExecutionPolicy — конфигурация графа исполнения запуска агента. Несёт только
// числа и длительности (не PII, не секреты), поэтому передаётся во вход workflow и
// делает опции воспроизводимыми при реплее.
type ExecutionPolicy struct {
	// ApprovalTTL — срок ожидания решения оператора по черновику. По истечении
	// ожидание прерывается: решение можно подать командой позже.
	ApprovalTTL time.Duration

	// ExecutionTimeout — предельное время исполнения запуска.
	ExecutionTimeout time.Duration

	// ActivityStartToClose — StartToCloseTimeout шага.
	ActivityStartToClose time.Duration

	// ActivityMaxAttempts — число попыток activity шага.
	ActivityMaxAttempts int
}

// Status — наблюдаемое состояние workflow (ответ на query status). Поля — только
// фаза, текущий шаг и курсор: содержания артефактов здесь нет.
type Status struct {
	RunID       string
	Phase       string
	CurrentStep string
	Cursor      int
}

// stepInput — вход activity прогона шага.
type stepInput struct {
	RunID         string
	TenantID      string
	CorrelationID string
	CausationID   string
}

// stepOutcome — результат activity прогона шага: наблюдаемое состояние запуска
// после шага. Содержания исхода, кроме кода, здесь нет.
type stepOutcome struct {
	StepID           string
	BlockRef         string
	RunStatus        string
	Terminal         bool
	AwaitingApproval bool
	Blocked          bool
	Outcome          string
}

// runInput — вход activity чтения состояния запуска.
type runInput struct {
	RunID    string
	TenantID string
}

// runState — наблюдаемое состояние запуска из хранилища.
type runState struct {
	Status           string
	CurrentStep      string
	Cursor           int
	Terminal         bool
	AwaitingApproval bool
	Blocked          bool
}

// ApprovalDecision — сигнал решения оператора бегущему workflow.
type ApprovalDecision struct {
	Approved     bool
	DraftRef     string
	DraftVersion int
	Actor        string
}

// StopSignal — сигнал остановки запуска бегущему workflow.
type StopSignal struct {
	Actor  string
	Reason string
}
