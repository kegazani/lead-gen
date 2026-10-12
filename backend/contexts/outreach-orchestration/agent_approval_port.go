package outreachorchestration

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/app/command"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/query"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/campaigncommand"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
)

// Точка подключения межконтекстных контрактов утверждения агента. Владелец
// (outreach-orchestration) реализует интерфейсы контрактов, а точка сборки (cmd)
// связывает их с потребителем (agent-orchestration) выбранным топологией
// транспортом: прямым вызовом при co-location или сетевым клиентом при разделении
// сервисов. Потребитель держит интерфейс контракта и не видит пакеты владельца.
//
// Эти методы — тонкий перевод типов контракта в прикладные команды/запросы среза.
// Несобранный срез или отсутствующий сценарий дают честную недоступность контракта,
// а не выдуманный приём или решение.

// SubmitForApproval реализует campaigncommand.Port: подаёт черновик агента в
// очередь утверждения владельца.
func (s *Service) SubmitForApproval(ctx context.Context, in campaigncommand.Submission) (campaigncommand.Result, error) {
	if s == nil || s.slice == nil || s.slice.SubmitApproval == nil {
		return campaigncommand.Result{}, campaigncommand.ErrUnavailable
	}
	result, err := s.slice.SubmitApproval.Handle(ctx, command.SubmitForApproval{
		TenantID:     domain.TenantID(in.TenantID),
		RunID:        in.RunID,
		MissionID:    in.MissionID,
		CompanyID:    in.CompanyID,
		DraftRef:     domain.DraftID(in.DraftRef),
		DraftVersion: in.DraftVersion,
		MaterialRef:  domain.MaterialID(in.MaterialRef),
		RecipientRef: domain.RecipientID(in.RecipientRef),
	})
	if err != nil {
		return campaigncommand.Result{}, submitApprovalContractError(err)
	}
	return campaigncommand.Result{
		CampaignID:   result.CampaignID.String(),
		DraftRef:     result.DraftRef.String(),
		DraftVersion: result.DraftVersion,
		Repeated:     result.Repeated,
	}, nil
}

// ApprovedAction реализует draftdecision.Source: читает утверждённое действие по
// черновику для шага отправки.
func (s *Service) ApprovedAction(ctx context.Context, in draftdecision.Lookup) (approvedaction.ApprovedAction, error) {
	if s == nil || s.slice == nil || s.slice.ApprovedAction == nil {
		return approvedaction.ApprovedAction{}, draftdecision.ErrUnavailable
	}
	action, err := s.slice.ApprovedAction.Handle(ctx, query.ApprovedActionQuery{
		TenantID:     domain.TenantID(in.TenantID),
		DraftRef:     domain.DraftID(in.DraftRef),
		DraftVersion: in.DraftVersion,
	})
	if err != nil {
		return approvedaction.ApprovedAction{}, approvedActionContractError(err)
	}
	return action, nil
}

// submitApprovalContractError переводит ошибку сценария в исход контракта
// campaign-command: доменный отказ различим, прочее (сбой хранилища, транзакции) —
// недоступность. Недоступность не выдаётся за успешную подачу.
func submitApprovalContractError(err error) error {
	if errors.Is(err, command.ErrApprovalRejected) {
		return fmt.Errorf("%w: %v", campaigncommand.ErrRejected, err)
	}
	return fmt.Errorf("%w: %v", campaigncommand.ErrUnavailable, err)
}

// approvedActionContractError переводит ошибку запроса чтения в исход контракта
// draftdecision. Исходы различаются: «решение не принято», «отклонено»,
// «черновик/решение не найдены» и «источник недоступен» не смешиваются.
func approvedActionContractError(err error) error {
	switch {
	case errors.Is(err, query.ErrDraftDecisionPending):
		return fmt.Errorf("%w: %v", draftdecision.ErrPending, err)
	case errors.Is(err, query.ErrDraftDecisionRejected):
		return fmt.Errorf("%w: %v", draftdecision.ErrRejected, err)
	case errors.Is(err, query.ErrDraftDecisionNotFound), errors.Is(err, query.ErrTenantRequired):
		return fmt.Errorf("%w: %v", draftdecision.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", draftdecision.ErrUnavailable, err)
	}
}

// Проверка соответствия интерфейсам опубликованных контрактов на этапе компиляции.
var (
	_ campaigncommand.Port = (*Service)(nil)
	_ draftdecision.Source = (*Service)(nil)
)
