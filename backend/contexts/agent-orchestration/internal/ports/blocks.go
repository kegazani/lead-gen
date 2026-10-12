package ports

import (
	"context"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
)

// StepInput — вход исполнения одного шага агентом. Несёт только идентификаторы и
// ссылку на артефакт предыдущего шага: содержания чужих артефактов нет.
type StepInput struct {
	RunID       string
	StepRunID   string
	StepID      string
	TenantID    string
	MissionID   string
	CompanyID   string
	Criteria    string
	BlockRef    domain.BlockRef
	Attempt     int
	ArtifactRef string
}

// StepResult — исход исполнения шага по блоку.
type StepResult struct {
	// Outcome — исход: успех, отказ или явная недоступность. Недоступность не
	// подменяется успехом (AGENTS.md §3).
	Outcome domain.BlockOutcome
	// ArtifactRef — непрозрачная ссылка на артефакт шага (например, найденную
	// компанию). Пустая, если артефакта нет.
	ArtifactRef string
	// DraftRef — ссылка на черновик, произведённый шагом (для блока утверждения).
	DraftRef domain.DraftRef
	// DraftVersion — версия черновика.
	DraftVersion int
	// AwaitApproval — шаг является воротами утверждения: после успеха запуск
	// переходит в ожидание решения оператора по произведённому черновику.
	AwaitApproval bool
}

// BlockExecutor — исполнение блока по контракту владельца. Реализуется адаптером
// конкретного блока (например, поверх опубликованного контракта lead-search или
// email-generation). Отсутствие исполнителя для зарегистрированного блока означает
// честную недоступность: результат не подменяется фиктивным (AGENTS.md §3).
type BlockExecutor interface {
	// Ref возвращает ссылку блока, который исполняет адаптер.
	Ref() domain.BlockRef

	// Execute выполняет шаг. Отказ владельца и сбой транспорта возвращаются
	// ошибкой; явная недоступность провайдера — исходом OutcomeUnavailable.
	Execute(ctx context.Context, in StepInput) (StepResult, error)
}
