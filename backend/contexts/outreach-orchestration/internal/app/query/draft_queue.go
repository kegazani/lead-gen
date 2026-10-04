package query

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// ErrInvalidRiskFilter — фильтр риска не объявлен запросом. Неизвестный фильтр
// отвергается, а не трактуется как «показать всё»: молчаливая подмена фильтра
// скрыла бы от оператора часть очереди.
var ErrInvalidRiskFilter = errors.New("запросы: неизвестный фильтр риска")

// RiskFilter — фильтр очереди по признаку риска черновика.
type RiskFilter string

const (
	// RiskFilterAll — без фильтра по риску.
	RiskFilterAll RiskFilter = ""

	// RiskFilterRisky — только черновики с признаками риска: они решаются
	// индивидуально и не входят в пакет.
	RiskFilterRisky RiskFilter = "risky"

	// RiskFilterSafe — только черновики без признаков риска.
	RiskFilterSafe RiskFilter = "safe"
)

// valid сообщает, что фильтр объявлен.
func (f RiskFilter) valid() bool {
	switch f {
	case RiskFilterAll, RiskFilterRisky, RiskFilterSafe:
		return true
	default:
		return false
	}
}

// DraftQueue — запрос очереди черновиков, ожидающих решения оператора, с
// фильтрами и результатом автоматических проверок риска.
//
// Кампании перечисляет вызывающий через порт CampaignLister, поэтому очередь не
// требует от вызывающего списка идентификаторов (в отличие от ApprovalQueue).
// TenantID и Actor обязательны: тенант ограничивает область данных, актор
// подтверждает право на чтение.
type DraftQueue struct {
	// CampaignID сужает очередь до одной кампании. Пусто — все кампании тенанта.
	CampaignID domain.CampaignID

	// Channel сужает очередь до одного канала. Нулевое значение — без фильтра.
	Channel domain.ActionChannel

	// Risk сужает очередь по признаку риска. Пусто — без фильтра.
	Risk RiskFilter

	TenantID domain.TenantID
	Actor    domain.ActorID
}

// DraftQueueItem — позиция очереди: черновик и результат автоматических
// проверок (признаки риска приходят от внешнего предиката).
type DraftQueueItem struct {
	CampaignID     domain.CampaignID
	DraftID        domain.DraftID
	DraftVersion   int
	Channel        domain.ActionChannel
	Spec           domain.DraftSpec
	RiskReasons    []domain.RiskReason
	PostponedUntil time.Time
}

// Risky сообщает, что по черновику есть признаки риска: такой черновик решается
// только индивидуально.
func (i DraftQueueItem) Risky() bool { return len(i.RiskReasons) > 0 }

// DraftQueueHandler — use case чтения очереди черновиков с проверками риска.
type DraftQueueHandler struct {
	campaigns ports.CampaignRepository
	lister    ports.CampaignLister
	risk      domain.CanBeDecidedInPackage
	access    ports.AccessDecision
}

// NewDraftQueueHandler собирает запрос из порта репозитория, перечислителя
// кампаний, внешнего предиката риска и (необязательного) порта доступа. Без
// порта доступа любой запрос отвергается с ErrAccessDenied, а не обслуживается
// без проверки. nil-предикат риска означает «рисков нет» и трактуется доменом
// так же.
func NewDraftQueueHandler(
	campaigns ports.CampaignRepository,
	lister ports.CampaignLister,
	risk domain.CanBeDecidedInPackage,
	access ...ports.AccessDecision,
) *DraftQueueHandler {
	return &DraftQueueHandler{
		campaigns: campaigns,
		lister:    lister,
		risk:      risk,
		access:    optionalAccess(access),
	}
}

// Handle возвращает очередь черновиков с применёнными фильтрами. Очередь
// строится публичным API агрегата: PendingApproval уже исключает утверждённые,
// отклонённые и просроченные черновики. Порядок детерминирован.
func (h *DraftQueueHandler) Handle(ctx context.Context, q DraftQueue) ([]DraftQueueItem, error) {
	if !q.Risk.valid() {
		return nil, fmt.Errorf("%w: %q", ErrInvalidRiskFilter, q.Risk)
	}
	if err := authorizeRead(ctx, h.access, q.Actor, q.TenantID); err != nil {
		return nil, err
	}

	ids, err := h.scope(ctx, q)
	if err != nil {
		return nil, err
	}

	var items []DraftQueueItem
	for _, id := range ids {
		campaign, err := h.campaigns.FindByID(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, err)
		}
		if campaign == nil {
			return nil, fmt.Errorf("загрузка кампании %s: %w", id, ports.ErrCampaignNotFound)
		}
		// Тенант обязателен: кампания чужого тенанта в выборку не попадает.
		if campaign.TenantID() != q.TenantID {
			continue
		}
		for _, draft := range campaign.PendingApproval() {
			if !q.Channel.IsZero() && !draft.Channel().Equals(q.Channel) {
				continue
			}
			reasons := riskReasons(h.risk, draft.ID(), draft.Version())
			if !q.Risk.matches(len(reasons) > 0) {
				continue
			}
			items = append(items, DraftQueueItem{
				CampaignID:     campaign.ID(),
				DraftID:        draft.ID(),
				DraftVersion:   draft.Version(),
				Channel:        draft.Channel(),
				Spec:           draft.Spec(),
				RiskReasons:    reasons,
				PostponedUntil: draft.PostponedUntil(),
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].CampaignID != items[j].CampaignID {
			return items[i].CampaignID < items[j].CampaignID
		}
		if items[i].Spec.Material != items[j].Spec.Material {
			return items[i].Spec.Material < items[j].Spec.Material
		}
		if items[i].Spec.Segment != items[j].Spec.Segment {
			return items[i].Spec.Segment < items[j].Spec.Segment
		}
		return items[i].DraftID < items[j].DraftID
	})
	return items, nil
}

// matches сообщает, проходит ли позиция фильтр риска.
func (f RiskFilter) matches(risky bool) bool {
	switch f {
	case RiskFilterRisky:
		return risky
	case RiskFilterSafe:
		return !risky
	default:
		return true
	}
}

// scope возвращает идентификаторы кампаний для выборки: одна кампания при
// заданном фильтре, иначе — все сохранённые.
func (h *DraftQueueHandler) scope(ctx context.Context, q DraftQueue) ([]domain.CampaignID, error) {
	if q.CampaignID != "" {
		return []domain.CampaignID{q.CampaignID}, nil
	}
	ids, err := h.lister.ListCampaignIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("перечисление кампаний: %w", err)
	}
	return ids, nil
}

// riskReasons применяет внешний предикат, допуская nil как «рисков нет».
func riskReasons(can domain.CanBeDecidedInPackage, id domain.DraftID, version int) []domain.RiskReason {
	if can == nil {
		return nil
	}
	return can(id, version)
}
