package query

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// SequenceHistory — запрос истории шагов по лиду. Область задаёт вызывающий:
// репозиторий не перечисляет кампании, поэтому CampaignIDs — это кампании, в
// которых искать историю.
type SequenceHistory struct {
	CampaignIDs []domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
	Lead        domain.LeadID
}

// SequenceHistoryEntry — запись журнала: шаг, время, результат и утвердивший
// оператор (spec outreach-sequences, «Журнал выполнения»).
type SequenceHistoryEntry struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	SequenceID  domain.SequenceID
	Lead        domain.LeadID
	Step        int
	Channel     domain.ActionChannel
	At          time.Time
	Result      domain.StepOutcome
	Actor       domain.ActorID
	Reason      string
}

// SequenceHistoryHandler — use case чтения истории шагов по лиду.
type SequenceHistoryHandler struct {
	campaigns ports.CampaignRepository
	access    ports.AccessDecision
}

// NewSequenceHistoryHandler собирает запрос из порта репозитория кампаний.
func NewSequenceHistoryHandler(campaigns ports.CampaignRepository, access ...ports.AccessDecision) *SequenceHistoryHandler {
	return &SequenceHistoryHandler{campaigns: campaigns, access: optionalAccess(access)}
}

// Handle возвращает все шаги всех цепочек лида с результатами.
func (h *SequenceHistoryHandler) Handle(ctx context.Context, q SequenceHistory) ([]SequenceHistoryEntry, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return nil, err
	}

	var entries []SequenceHistoryEntry
	for _, id := range q.CampaignIDs {
		campaign, err := h.campaigns.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
		}
		if campaign.TenantID() != q.TenantID {
			continue
		}
		for _, entry := range campaign.JournalForLead(q.Lead) {
			entries = append(entries, SequenceHistoryEntry{
				CampaignID:  entry.Campaign,
				ExecutionID: entry.Execution,
				Lead:        entry.Lead,
				Step:        entry.Step,
				Channel:     entry.Action,
				At:          entry.At,
				Result:      entry.Result,
				Actor:       entry.Actor,
				Reason:      entry.Reason,
			})
		}
	}
	return entries, nil
}

// SequenceState — запрос состояния цепочек кампаний: участия и объём
// неутверждённых черновиков.
type SequenceState struct {
	CampaignIDs []domain.CampaignID
	TenantID    domain.TenantID
	Actor       domain.ActorID
}

// SequenceExecutionState — состояние участия лида.
type SequenceExecutionState struct {
	CampaignID  domain.CampaignID
	ExecutionID domain.ExecutionID
	SequenceID  domain.SequenceID
	Lead        domain.LeadID
	State       domain.ExecutionState
	Step        int
	EligibleAt  time.Time
	DraftID     domain.DraftID
}

// SequenceStateItem — состояние цепочек одной кампании.
type SequenceStateItem struct {
	CampaignID      domain.CampaignID
	Executions      []SequenceExecutionState
	PendingDrafts   int
	TotalExecutions int
}

// SequenceStateHandler — use case чтения состояния цепочек.
type SequenceStateHandler struct {
	campaigns ports.CampaignRepository
	access    ports.AccessDecision
}

// NewSequenceStateHandler собирает запрос состояния цепочек.
func NewSequenceStateHandler(campaigns ports.CampaignRepository, access ...ports.AccessDecision) *SequenceStateHandler {
	return &SequenceStateHandler{campaigns: campaigns, access: optionalAccess(access)}
}

// Handle возвращает участия и объём неутверждённых черновиков по кампаниям.
// Объём неутверждённых черновиков — отдельный показатель простоя.
func (h *SequenceStateHandler) Handle(ctx context.Context, q SequenceState) ([]SequenceStateItem, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return nil, err
	}

	var items []SequenceStateItem
	for _, id := range q.CampaignIDs {
		campaign, err := h.campaigns.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
		}
		if campaign.TenantID() != q.TenantID {
			continue
		}
		executions := campaign.Executions()
		item := SequenceStateItem{
			CampaignID:      campaign.ID(),
			Executions:      make([]SequenceExecutionState, 0, len(executions)),
			PendingDrafts:   campaign.PendingSequenceDrafts(),
			TotalExecutions: len(executions),
		}
		for _, execution := range executions {
			item.Executions = append(item.Executions, SequenceExecutionState{
				CampaignID:  campaign.ID(),
				ExecutionID: execution.ID(),
				SequenceID:  execution.SequenceID(),
				Lead:        execution.Lead(),
				State:       execution.State(),
				Step:        execution.StepIndex(),
				EligibleAt:  execution.EligibleAt(),
				DraftID:     execution.DraftID(),
			})
		}
		items = append(items, item)
	}
	return items, nil
}
