package blocks

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
)

// AwaitApprovalBlockRef — ссылка блока ожидания утверждения.
const AwaitApprovalBlockRef domain.BlockRef = "await-approval"

// AwaitApprovalExecutor — исполнитель блока await-approval: подаёт произведённый
// предыдущим шагом черновик в очередь утверждения владельца (design.md, Реш. 6) и
// останавливает запуск в ожидании решения оператора по точной версии черновика.
//
// Блок не отправляет письмо и не выдаёт отсутствие решения за успех: он только
// подаёт черновик и переводит запуск в ожидание; отправку исполняет шаг send и
// только по положительному решению (policy irreversible-step-approval).
type AwaitApprovalExecutor struct {
	contract ports.ApprovalSubmitter
}

// NewAwaitApprovalExecutor собирает исполнитель блока ожидания утверждения.
// nil-контракт допустим: шаг честно сообщает недоступность.
func NewAwaitApprovalExecutor(contract ports.ApprovalSubmitter) *AwaitApprovalExecutor {
	return &AwaitApprovalExecutor{contract: contract}
}

// Ref реализует ports.BlockExecutor.
func (e *AwaitApprovalExecutor) Ref() domain.BlockRef { return AwaitApprovalBlockRef }

// Execute реализует ports.BlockExecutor.
func (e *AwaitApprovalExecutor) Execute(ctx context.Context, in ports.StepInput) (ports.StepResult, error) {
	materialRef := strings.TrimSpace(in.ArtifactRef)
	if materialRef == "" {
		return ports.StepResult{Outcome: domain.Unavailable("await-approval: черновик не произведён предыдущим шагом")}, nil
	}
	if e.contract == nil {
		return ports.StepResult{Outcome: domain.Unavailable("campaign-command: контракт утверждения не подключён")}, nil
	}
	draftRef, version := splitMaterialRef(materialRef)
	if err := e.contract.SubmitForApproval(ctx, ports.ApprovalSubmission{
		TenantID:     in.TenantID,
		RunID:        in.RunID,
		MissionID:    in.MissionID,
		CompanyID:    in.CompanyID,
		DraftRef:     string(draftRef),
		DraftVersion: version,
		MaterialRef:  materialRef,
	}); err != nil {
		switch {
		case errors.Is(err, ports.ErrApprovalSubmitRejected):
			return ports.StepResult{Outcome: domain.Failed("campaign-command: черновик отклонён владельцем")}, nil
		case errors.Is(err, ports.ErrApprovalSubmitUnavailable):
			return ports.StepResult{Outcome: domain.Unavailable("campaign-command: владелец утверждения недоступен")}, nil
		default:
			return ports.StepResult{}, err
		}
	}
	return ports.StepResult{
		Outcome:       domain.Succeeded(),
		ArtifactRef:   materialRef,
		DraftRef:      draftRef,
		DraftVersion:  version,
		AwaitApproval: true,
	}, nil
}

// Проверка соответствия порту исполнителя блока.
var _ ports.BlockExecutor = (*AwaitApprovalExecutor)(nil)
