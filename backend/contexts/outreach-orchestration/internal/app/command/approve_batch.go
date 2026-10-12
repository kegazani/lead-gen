package command

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// BatchApproveItem — позиция команды пакетного утверждения: черновик и его
// точная версия, полученные оператором вместе с digest состава.
type BatchApproveItem struct {
	DraftID domain.DraftID
	Version int
}

// ApproveBatch — команда пакетного утверждения по digest состава.
//
// Команда адресует шард парой (ключ однородности, digest состава) и несёт состав
// с точными версиями черновиков, показанный оператору. Без digest или при его
// несовпадении с пересчитанным по текущему состоянию решение не принимается —
// сценарий «решение без просмотра» отвергается (design.md, Решение 3).
//
// Состав не является источником истины: агрегат пересчитывает digest из текущего
// состояния и сам проверяет однородность, версии и признаки риска.
type ApproveBatch struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID

	// Key — основание однородности шарда, полученное вместе с составом.
	Key domain.HomogeneityKey

	// Digest — digest состава, возвращённый сценарием BuildBatch.
	Digest string

	// Composition — состав шарда с точными версиями.
	Composition []BatchApproveItem

	Reason string
}

// BatchDecisionResult — исход по одной позиции пакета.
type BatchDecisionResult struct {
	DecisionID   domain.DecisionID
	DraftID      domain.DraftID
	DraftVersion int
	Batch        bool
}

// ApproveBatchResult — итог пакетного утверждения. Пакета целиком в результате
// нет: решений столько же, сколько черновиков; Digest лишь называет провенанс.
type ApproveBatchResult struct {
	CampaignID domain.CampaignID
	Key        domain.HomogeneityKey
	Digest     string
	Decisions  []BatchDecisionResult
	Recorded   int
	EventCount int
}

// ApproveBatchHandler — use case пакетного утверждения.
type ApproveBatchHandler struct {
	access ports.AccessDecision
	clock  ports.Clock
	uow    ports.UnitOfWork
	risk   domain.CanBeDecidedInPackage
}

// NewApproveBatchHandler собирает сценарий из портов доступа, времени,
// транзакции и внешнего предиката риска. Предикат тот же, что при формировании
// состава: агрегат повторно проверяет по нему каждую позицию.
func NewApproveBatchHandler(
	access ports.AccessDecision,
	clock ports.Clock,
	uow ports.UnitOfWork,
	risk domain.CanBeDecidedInPackage,
) *ApproveBatchHandler {
	return &ApproveBatchHandler{access: access, clock: clock, uow: uow, risk: risk}
}

// Handle фиксирует N решений по составу и записывает события в outbox в одной
// транзакции с изменением агрегата.
//
// Допустимость, однородность, повторная валидация версий, риск-исключение и
// идемпотентность повтора — правила агрегата (domain.ApproveBatch). Прикладной
// слой лишь отказывает при пустом или отсутствующем digest и передаёт состав
// без изменений: решать, кого включать, здесь нельзя.
func (h *ApproveBatchHandler) Handle(ctx context.Context, cmd ApproveBatch) (ApproveBatchResult, error) {
	if err := authorize(ctx, h.access, cmd.Actor, cmd.TenantID); err != nil {
		return ApproveBatchResult{}, err
	}
	if strings.TrimSpace(cmd.Digest) == "" || len(cmd.Composition) == 0 {
		return ApproveBatchResult{}, fmt.Errorf("%w: кампания %s", domain.ErrBatchDigestMismatch, cmd.CampaignID)
	}

	composition := make([]domain.BatchApprovalItem, 0, len(cmd.Composition))
	for _, item := range cmd.Composition {
		composition = append(composition, domain.BatchApprovalItem{
			DecisionID:   batchDecisionID(cmd.Digest, item.DraftID, item.Version),
			DraftID:      item.DraftID,
			DraftVersion: item.Version,
		})
	}

	var result ApproveBatchResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := rejectIfCampaignCancelled(ctx, tx, cmd.TenantID.String(), cmd.CampaignID.String()); err != nil {
			return err
		}
		campaign, err := loadCampaign(ctx, tx.Campaigns(), cmd.CampaignID, cmd.TenantID)
		if err != nil {
			return err
		}

		before := len(campaign.Decisions())
		decisions, _, err := campaign.ApproveBatch(ctx, domain.BatchApprovalParams{
			Actor:       cmd.Actor,
			DecidedAt:   h.clock.Now(),
			Digest:      cmd.Digest,
			Composition: composition,
			Reason:      cmd.Reason,
		}, h.risk)
		if err != nil {
			return err
		}

		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return fmt.Errorf("сохранение кампании %s: %w", cmd.CampaignID, err)
		}
		events := campaign.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return fmt.Errorf("запись событий кампании %s в outbox: %w", cmd.CampaignID, err)
		}
		campaign.PullDomainEvents()

		result = ApproveBatchResult{
			CampaignID: campaign.ID(),
			Key:        cmd.Key,
			Digest:     strings.TrimSpace(cmd.Digest),
			EventCount: len(events),
			Recorded:   len(campaign.Decisions()) - before,
		}
		for _, decision := range decisions {
			result.Decisions = append(result.Decisions, BatchDecisionResult{
				DecisionID:   decision.ID(),
				DraftID:      decision.DraftID(),
				DraftVersion: decision.DraftVersion(),
				Batch:        decision.IsBatchDecision(),
			})
		}
		return nil
	})
	if err != nil {
		return ApproveBatchResult{}, err
	}
	return result, nil
}

// batchDecisionID строит детерминированный идентификатор решения пакетного пути
// из digest, черновика и его версии. Детерминированность сохраняет идемпотентность
// повтора команды и не даёт двум разным черновикам одного пакета получить один
// идентификатор.
func batchDecisionID(digest string, draftID domain.DraftID, version int) domain.DecisionID {
	sum := sha256.Sum256([]byte(strings.TrimSpace(digest) + "|" + string(draftID) + "|" + strconv.Itoa(version)))
	return domain.DecisionID("batch-" + hex.EncodeToString(sum[:8]) + "-" + string(draftID))
}
