package query

import (
	"context"
	"fmt"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// BatchQueue — запрос пакетной проекции очереди.
//
// Проекция строится исключительно из результата агрегата BuildApprovalBatch и
// сгруппирована по ключу однородности: query не группирует и не фильтрует
// черновики сам и не решает, кого исключать. Он лишь сериализует уже
// посчитанные шарды, исключённых с причинами и направленных на индивидуальное
// рассмотрение (design.md, Решение 2).
//
// TenantID и Actor обязательны: тенант ограничивает область данных, актор
// подтверждает право на чтение.
type BatchQueue struct {
	CampaignID domain.CampaignID
	TenantID   domain.TenantID
	Actor      domain.ActorID
	Limits     domain.BatchLimits
}

// BatchQueueItem — позиция состава в проекции очереди.
type BatchQueueItem struct {
	DraftID      domain.DraftID
	DraftVersion int
	Spec         domain.DraftSpec
}

// BatchQueueShard — шард пакета: основание однородности, digest и состав.
type BatchQueueShard struct {
	Key    domain.HomogeneityKey
	Digest string
	Size   int
	Items  []BatchQueueItem
}

// BatchQueueExcluded — исключённый агрегатом черновик с причинами.
type BatchQueueExcluded struct {
	DraftID      domain.DraftID
	DraftVersion int
	Reasons      []domain.RiskReason
}

// BatchQueueProjection — пакетная проекция очереди.
type BatchQueueProjection struct {
	CampaignID domain.CampaignID
	Batches    []BatchQueueShard
	Excluded   []BatchQueueExcluded
	Individual []BatchQueueItem
}

// BatchQueueHandler — use case чтения пакетной проекции очереди.
type BatchQueueHandler struct {
	campaigns ports.CampaignRepository
	risk      domain.CanBeDecidedInPackage
	access    ports.AccessDecision
}

// NewBatchQueueHandler собирает запрос из порта репозитория кампаний, внешнего
// предиката риска и (необязательного) порта доступа. Без порта доступа любой
// запрос отвергается с ErrAccessDenied, а не обслуживается без проверки.
func NewBatchQueueHandler(
	campaigns ports.CampaignRepository,
	risk domain.CanBeDecidedInPackage,
	access ...ports.AccessDecision,
) *BatchQueueHandler {
	return &BatchQueueHandler{campaigns: campaigns, risk: risk, access: optionalAccess(access)}
}

// Handle возвращает пакетную проекцию очереди: шарды с digest, исключённых с
// причинами и черновики на индивидуальное рассмотрение. Всё это — результат
// BuildApprovalBatch; запрос не добавляет и не убирает ни одной позиции.
func (h *BatchQueueHandler) Handle(ctx context.Context, q BatchQueue) (BatchQueueProjection, error) {
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return BatchQueueProjection{}, err
	}

	campaign, err := h.campaigns.FindByID(ctx, q.CampaignID)
	if err != nil {
		return BatchQueueProjection{}, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, err)
	}
	if campaign == nil {
		return BatchQueueProjection{}, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, ports.ErrCampaignNotFound)
	}
	// Тенант обязателен: кампания другого тенанта не раскрывается даже фактом
	// существования.
	if campaign.TenantID() != q.TenantID {
		return BatchQueueProjection{}, fmt.Errorf("загрузка кампании %s: %w", q.CampaignID, ports.ErrCampaignNotFound)
	}

	built, err := campaign.BuildApprovalBatch(ctx, q.Limits, h.risk)
	if err != nil {
		return BatchQueueProjection{}, err
	}

	projection := BatchQueueProjection{CampaignID: campaign.ID()}
	for _, batch := range built.Batches() {
		shard := BatchQueueShard{Key: batch.Key(), Digest: batch.Digest(), Size: batch.Size()}
		for _, entry := range batch.Entries() {
			shard.Items = append(shard.Items, BatchQueueItem{
				DraftID:      entry.DraftID,
				DraftVersion: entry.Version,
				Spec:         entry.Spec,
			})
		}
		projection.Batches = append(projection.Batches, shard)
	}
	for _, excluded := range built.Excluded() {
		projection.Excluded = append(projection.Excluded, BatchQueueExcluded{
			DraftID:      excluded.DraftID,
			DraftVersion: excluded.Version,
			Reasons:      append([]domain.RiskReason(nil), excluded.Reasons...),
		})
	}
	for _, entry := range built.Individual() {
		projection.Individual = append(projection.Individual, BatchQueueItem{
			DraftID:      entry.DraftID,
			DraftVersion: entry.Version,
			Spec:         entry.Spec,
		})
	}
	return projection, nil
}
