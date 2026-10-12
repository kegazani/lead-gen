package blocks

import (
	"context"
	"errors"
	"strings"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	"lead-gen/backend/contexts/agent-orchestration/internal/ports"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

// SendBlockRef — ссылка блока отправки письма.
const SendBlockRef domain.BlockRef = "send"

// SendExecutor — исполнитель блока send поверх опубликованного контракта
// approved-action (владелец — outreach-orchestration, исполняет message-delivery).
// Блок публикует утверждённое оператором действие на исполнение и не принимает
// решения о допустимости отправки: санкцию несёт решение оператора, привязанное к
// точной версии черновика (policy irreversible-step-approval).
//
// Содержимое письма и адрес получателя через контракт не идут: action несёт только
// непрозрачные ссылки, а адрес разрешает message-delivery.
type SendExecutor struct {
	publisher approvedaction.Publisher
	source    ports.ApprovedActionSource
}

// NewSendExecutor собирает исполнитель блока отправки. nil-издатель или
// nil-источник допустимы: шаг честно сообщает недоступность и не выдаёт отправку
// за выполненную.
func NewSendExecutor(publisher approvedaction.Publisher, source ports.ApprovedActionSource) *SendExecutor {
	return &SendExecutor{publisher: publisher, source: source}
}

// Ref реализует ports.BlockExecutor.
func (e *SendExecutor) Ref() domain.BlockRef { return SendBlockRef }

// Execute реализует ports.BlockExecutor. Шаг возвращает ссылку черновика и его
// версию, совпадающие с версией, к которой привязано решение оператора: агрегат
// откажет в отправке, если черновик изменился (domain, ErrDraftChanged).
func (e *SendExecutor) Execute(ctx context.Context, in ports.StepInput) (ports.StepResult, error) {
	materialRef := strings.TrimSpace(in.ArtifactRef)
	if materialRef == "" {
		return ports.StepResult{Outcome: domain.Unavailable("approved-action: черновик утверждённого действия не задан")}, nil
	}
	if e.publisher == nil || e.source == nil {
		return ports.StepResult{Outcome: domain.Unavailable("approved-action: контракт отправки не подключён")}, nil
	}
	draftRef, version := splitMaterialRef(materialRef)
	action, err := e.source.ApprovedAction(ctx, ports.ApprovedActionLookup{
		TenantID:     in.TenantID,
		DraftRef:     string(draftRef),
		DraftVersion: version,
	})
	if err != nil {
		switch {
		case errors.Is(err, ports.ErrApprovedActionPending), errors.Is(err, ports.ErrApprovedActionRejected):
			return ports.StepResult{Outcome: domain.Failed("approved-action: отправка не санкционирована решением оператора")}, nil
		case errors.Is(err, ports.ErrApprovedActionUnavailable):
			return ports.StepResult{Outcome: domain.Unavailable("approved-action: источник решения недоступен")}, nil
		default:
			return ports.StepResult{}, err
		}
	}
	if err := e.publisher.Publish(ctx, action); err != nil {
		return ports.StepResult{}, err
	}
	return ports.StepResult{
		Outcome:      domain.Succeeded(),
		ArtifactRef:  materialRef,
		DraftRef:     draftRef,
		DraftVersion: version,
	}, nil
}

// Проверка соответствия порту исполнителя блока.
var _ ports.BlockExecutor = (*SendExecutor)(nil)
