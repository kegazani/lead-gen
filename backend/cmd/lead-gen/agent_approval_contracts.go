// Проводка межконтекстных контрактов утверждения агента (change
// intelligent-lead-operations, задача 8.3). Блоки await-approval и send агента
// читают владельца outreach-orchestration по опубликованным контрактам
// campaign-command (подача черновика) и draftdecision (чтение решения).
//
// Точка сборки связывает их по топологии: при co-location — ленивым in-proc
// мостом поверх собранного *outreachorchestration.Service, при разделении сервисов
// — сетевым клиентом того же контракта. Иначе порт не задаётся, и блок честно
// недоступен, а не подменяется выдуманным результатом (AGENTS.md §3).
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	outreachorchestration "lead-gen/backend/contexts/outreach-orchestration"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/outreach-orchestration/approvedaction"
	"lead-gen/contracts/outreach-orchestration/campaigncommand"
	campaigncommandhttpport "lead-gen/contracts/outreach-orchestration/campaigncommand/httpport"
	campaigncommandinproc "lead-gen/contracts/outreach-orchestration/campaigncommand/inproc"
	"lead-gen/contracts/outreach-orchestration/draftdecision"
	draftdecisionhttpport "lead-gen/contracts/outreach-orchestration/draftdecision/httpport"
	draftdecisioninproc "lead-gen/contracts/outreach-orchestration/draftdecision/inproc"
)

// campaignCommandSubmitter связывает опубликованный контракт campaign-command
// (владелец — outreach-orchestration) с портом блока await-approval: переводит
// форму подачи и её исходы. Доменный отказ владельца отличим от недоступности;
// прочее (сбой транспорта) не выдаётся за успешный приём.
type campaignCommandSubmitter struct {
	port campaigncommand.Port
}

// newCampaignCommandSubmitter собирает мост поверх опубликованного контракта.
func newCampaignCommandSubmitter(port campaigncommand.Port) *campaignCommandSubmitter {
	return &campaignCommandSubmitter{port: port}
}

// Проверка соответствия порту блока await-approval на этапе компиляции.
var _ agentorchestration.ApprovalSubmitter = (*campaignCommandSubmitter)(nil)

// SubmitForApproval реализует agentorchestration.ApprovalSubmitter.
func (s *campaignCommandSubmitter) SubmitForApproval(ctx context.Context, in agentorchestration.ApprovalSubmission) error {
	if s == nil || s.port == nil {
		return agentorchestration.ErrApprovalSubmitUnavailable
	}
	_, err := s.port.SubmitForApproval(ctx, campaigncommand.Submission{
		TenantID:     in.TenantID,
		RunID:        in.RunID,
		MissionID:    in.MissionID,
		CompanyID:    in.CompanyID,
		DraftRef:     in.DraftRef,
		DraftVersion: in.DraftVersion,
		MaterialRef:  in.MaterialRef,
	})
	if err != nil {
		switch {
		case errors.Is(err, campaigncommand.ErrRejected):
			return fmt.Errorf("%w: %v", agentorchestration.ErrApprovalSubmitRejected, err)
		default:
			return fmt.Errorf("%w: %v", agentorchestration.ErrApprovalSubmitUnavailable, err)
		}
	}
	return nil
}

// approvedActionSourceAdapter связывает read-контракт draftdecision (владелец —
// outreach-orchestration) с портом блока send: переводит запрос решения и его
// исходы. «Решение не принято» и «отклонено» отличимы от недоступности источника;
// положительное решение не выдумывается.
type approvedActionSourceAdapter struct {
	source draftdecision.Source
}

// newApprovedActionSource собирает мост поверх опубликованного контракта.
func newApprovedActionSource(source draftdecision.Source) *approvedActionSourceAdapter {
	return &approvedActionSourceAdapter{source: source}
}

// Проверка соответствия порту блока send на этапе компиляции.
var _ agentorchestration.ApprovedActionSource = (*approvedActionSourceAdapter)(nil)

// ApprovedAction реализует agentorchestration.ApprovedActionSource.
func (s *approvedActionSourceAdapter) ApprovedAction(ctx context.Context, in agentorchestration.ApprovedActionLookup) (approvedaction.ApprovedAction, error) {
	if s == nil || s.source == nil {
		return approvedaction.ApprovedAction{}, agentorchestration.ErrApprovedActionUnavailable
	}
	action, err := s.source.ApprovedAction(ctx, draftdecision.Lookup{
		TenantID:     in.TenantID,
		DraftRef:     in.DraftRef,
		DraftVersion: in.DraftVersion,
	})
	if err != nil {
		switch {
		case errors.Is(err, draftdecision.ErrPending):
			return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", agentorchestration.ErrApprovedActionPending, err)
		case errors.Is(err, draftdecision.ErrRejected):
			return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", agentorchestration.ErrApprovedActionRejected, err)
		case errors.Is(err, draftdecision.ErrNotFound):
			// Решения по точной версии черновика нет: это «ещё не санкционировано»,
			// а не «источник недоступен». Отправка без решения не производится.
			return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", agentorchestration.ErrApprovedActionPending, err)
		default:
			return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", agentorchestration.ErrApprovedActionUnavailable, err)
		}
	}
	return action, nil
}

// agentApprovalCoLocated — ленивый co-located мост к владельцу
// outreach-orchestration сразу для двух его контрактов: подача черновика
// (campaign-command) и чтение решения (draftdecision). Владелец собирается при
// первом обращении, а не в момент сборки агента: иначе сборка агента потянула бы
// сборку владельца и замкнула порядок инициализации. Один экземпляр обслуживает
// оба порта, поэтому владелец собирается однократно.
type agentApprovalCoLocated struct {
	buildOnce func(selectedService) (composition.Service, error)
	sel       selectedService

	once     sync.Once
	submit   campaigncommand.Port
	decision draftdecision.Source
	err      error
}

// resolve собирает контракты владельца по требованию и связывает их in-proc
// адаптерами.
func (p *agentApprovalCoLocated) resolve() error {
	p.once.Do(func() {
		service, err := p.buildOnce(p.sel)
		if err != nil {
			p.err = fmt.Errorf("outreach-orchestration: сборка владельца утверждения: %w", err)
			return
		}
		owner, ok := service.(*outreachorchestration.Service)
		if !ok {
			p.err = fmt.Errorf("outreach-orchestration: владелец собран не тем типом сервиса: %T", service)
			return
		}
		submit, err := campaigncommandinproc.Bind(owner)
		if err != nil {
			p.err = fmt.Errorf("outreach-orchestration: in-proc подача черновика: %w", err)
			return
		}
		decision, err := draftdecisioninproc.Bind(owner)
		if err != nil {
			p.err = fmt.Errorf("outreach-orchestration: in-proc чтение решения: %w", err)
			return
		}
		p.submit = submit
		p.decision = decision
	})
	return p.err
}

// SubmitForApproval реализует agentorchestration.ApprovalSubmitter.
func (p *agentApprovalCoLocated) SubmitForApproval(ctx context.Context, in agentorchestration.ApprovalSubmission) error {
	if err := p.resolve(); err != nil {
		return fmt.Errorf("%w: %v", agentorchestration.ErrApprovalSubmitUnavailable, err)
	}
	return newCampaignCommandSubmitter(p.submit).SubmitForApproval(ctx, in)
}

// ApprovedAction реализует agentorchestration.ApprovedActionSource.
func (p *agentApprovalCoLocated) ApprovedAction(ctx context.Context, in agentorchestration.ApprovedActionLookup) (approvedaction.ApprovedAction, error) {
	if err := p.resolve(); err != nil {
		return approvedaction.ApprovedAction{}, fmt.Errorf("%w: %v", agentorchestration.ErrApprovedActionUnavailable, err)
	}
	return newApprovedActionSource(p.decision).ApprovedAction(ctx, in)
}

// Проверка соответствия портам блоков на этапе компиляции.
var (
	_ agentorchestration.ApprovalSubmitter    = (*agentApprovalCoLocated)(nil)
	_ agentorchestration.ApprovedActionSource = (*agentApprovalCoLocated)(nil)
)

// assembleAgentApprovalContracts связывает порты блока await-approval (подача
// черновика, контракт campaign-command) и блока send (источник утверждённого
// действия, read-контракт draftdecision) с владельцем outreach-orchestration.
//
// Выбор транспорта — по топологии, как у иных владельческих контрактов: задан
// адрес — сетевой клиент контракта; иначе владелец выбран — ленивый co-located
// мост; иначе порт не задан, и блок честно недоступен (AGENTS.md §3).
func assembleAgentApprovalContracts(
	selected []selectedService,
	opts *buildOptions,
	buildOnce func(selectedService) (composition.Service, error),
	logger observ.Logger,
) {
	sel, ownerSelected := findSelected(selected, outreachorchestration.ServiceID)

	var coLocated *agentApprovalCoLocated
	coLocatedFor := func() *agentApprovalCoLocated {
		if coLocated == nil {
			coLocated = &agentApprovalCoLocated{buildOnce: buildOnce, sel: sel}
		}
		return coLocated
	}

	// Блок await-approval: подача черновика в очередь утверждения (campaign-command).
	switch {
	case strings.TrimSpace(opts.campaignCommandURL) != "":
		client, err := campaigncommandhttpport.New(opts.campaignCommandURL)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент campaign-command", err, observ.Fields{}, "addr", opts.campaignCommandURL)
			break
		}
		opts.approvalSubmitter = newCampaignCommandSubmitter(client)
		logger.Info("lead-gen: блок await-approval подаёт черновик по сети (campaign-command)", observ.Fields{}, "addr", opts.campaignCommandURL)
	case ownerSelected:
		opts.approvalSubmitter = coLocatedFor()
		logger.Info("lead-gen: блок await-approval подаёт черновик владельцу outreach-orchestration (co-location)", observ.Fields{})
	default:
		logger.Warn("lead-gen: контракт campaign-command не подключён — блок await-approval честно недоступен", observ.Fields{})
	}

	// Блок send: чтение утверждённого действия по черновику (draftdecision).
	switch {
	case strings.TrimSpace(opts.draftDecisionURL) != "":
		client, err := draftdecisionhttpport.New(opts.draftDecisionURL)
		if err != nil {
			logger.Error("lead-gen: сетевой клиент draftdecision", err, observ.Fields{}, "addr", opts.draftDecisionURL)
			break
		}
		opts.approvedActionSource = newApprovedActionSource(client)
		logger.Info("lead-gen: блок send читает решение по сети (draftdecision)", observ.Fields{}, "addr", opts.draftDecisionURL)
	case ownerSelected:
		opts.approvedActionSource = coLocatedFor()
		logger.Info("lead-gen: блок send читает решение у владельца outreach-orchestration (co-location)", observ.Fields{})
	default:
		logger.Warn("lead-gen: read-контракт draftdecision не подключён — блок send честно недоступен", observ.Fields{})
	}
}
