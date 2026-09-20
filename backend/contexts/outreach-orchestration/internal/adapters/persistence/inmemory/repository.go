// Package inmemory реализует ports.CampaignRepository поверх оперативной памяти.
//
// Адаптер хранит не указатели на агрегаты, а снимки состояния
// (domain.CampaignSnapshot), а загрузка идёт через domain.RehydrateCampaign.
// Поэтому сохранённый агрегат изолирован от вызывающего: последующие правки
// кампании не меняют уже сохранённое состояние, а загрузка заново проверяет
// инварианты агрегата. Реализация не знает о транзакциях, драйверах и SQL —
// атомарностью записи управляет unit-of-work адаптера outbox/inmemory.
package inmemory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Repository — in-memory реализация ports.CampaignRepository.
//
// Состояние защищено мьютексом, поэтому репозиторий пригоден для `go test -race`
// и для параллельных читателей. Методы Clone и ReplaceWith существуют для
// unit-of-work: транзакция работает с копией, а commit переносит её состояние.
type Repository struct {
	mu        sync.RWMutex
	campaigns map[domain.CampaignID]domain.CampaignSnapshot
}

// Repository реализует порт репозитория кампаний и его аддитивное расширение
// загрузкой окна черновиков (DraftWindowRepository).
var (
	_ ports.CampaignRepository    = (*Repository)(nil)
	_ ports.DraftWindowRepository = (*Repository)(nil)
)

// New создаёт пустой репозиторий.
func New() *Repository {
	return &Repository{campaigns: make(map[domain.CampaignID]domain.CampaignSnapshot)}
}

// Save сохраняет агрегат целиком как снимок. Повторный вызов для той же
// кампании заменяет прежний снимок (upsert по идентификатору), не создавая
// вторую кампанию.
func (r *Repository) Save(_ context.Context, campaign *domain.Campaign) error {
	if campaign == nil {
		return errors.New("inmemory: сохранение nil-кампании")
	}
	snapshot := campaign.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.campaigns[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает кампанию по идентификатору через domain.RehydrateCampaign.
// Отсутствие кампании даёт ошибку, для которой истинно
// errors.Is(err, ports.ErrCampaignNotFound).
func (r *Repository) FindByID(_ context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	r.mu.RLock()
	snapshot, ok := r.campaigns[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, id)
	}
	campaign, err := domain.RehydrateCampaign(snapshot)
	if err != nil {
		return nil, fmt.Errorf("inmemory: восстановление кампании %s: %w", id, err)
	}
	return campaign, nil
}

// LoadDraftWindow реализует ports.DraftWindowRepository поверх снимков кампаний.
//
// Запрос проверяется первой же строкой, поэтому все реализации отвергают
// некорректный запрос одинаково. Кампания берётся из карты снимков под RLock,
// затем из её черновиков строится keyset-последовательность в порядке
// (материал, сегмент, идентификатор): повторный вызов с тем же запросом даёт тот
// же состав и порядок. Полная кампания не восстанавливается — черновики окна
// рехидратируются поштучно.
//
// Тенант кампании сверяется с query.TenantID: чужой тенант получает
// ErrCampaignNotFound, а не отвержение с раскрытием существования. Проверка
// стоит до сортировки и рехидратации, поэтому состав чужого тенанта не читается.
func (r *Repository) LoadDraftWindow(_ context.Context, query ports.DraftWindowQuery) (ports.DraftWindow, error) {
	if err := query.Validate(); err != nil {
		return ports.DraftWindow{}, err
	}

	r.mu.RLock()
	snapshot, ok := r.campaigns[query.CampaignID]
	r.mu.RUnlock()
	if !ok {
		return ports.DraftWindow{}, fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, query.CampaignID)
	}
	if snapshot.TenantID != query.TenantID {
		// Чужой тенант неотличим от отсутствующей кампании: наличие состава не раскрывается.
		return ports.DraftWindow{}, fmt.Errorf("%w: %s", ports.ErrCampaignNotFound, query.CampaignID)
	}

	// Ключи сортировки и сами снимки черновиков. Снимок кампании неизменяем
	// после сохранения, поэтому читать его срезы после снятия блокировки безопасно.
	type keyedDraft struct {
		snapshot domain.ActionDraftSnapshot
		cursor   ports.DraftWindowCursor
	}
	ordered := make([]keyedDraft, 0, len(snapshot.Drafts))
	for _, draft := range snapshot.Drafts {
		ordered = append(ordered, keyedDraft{
			snapshot: draft,
			cursor: ports.DraftWindowCursor{
				Material: draft.Spec.Material,
				Segment:  draft.Spec.Segment,
				DraftID:  draft.ID,
			},
		})
	}
	sort.Slice(ordered, func(i, j int) bool {
		return ports.CompareDraftKey(ordered[i].cursor, ordered[j].cursor) < 0
	})

	// Keyset-переход исключающий: первая позиция строго после курсора запроса.
	// Нулевой курсор задаёт начало последовательности.
	start := sort.Search(len(ordered), func(i int) bool {
		return ports.CompareDraftKey(ordered[i].cursor, query.After) > 0
	})
	end := start + query.Limit
	if end > len(ordered) {
		end = len(ordered)
	}

	window := ports.DraftWindow{}
	if start < len(ordered) {
		window.Drafts = make([]*domain.ActionDraft, 0, end-start)
		for _, item := range ordered[start:end] {
			draft, err := domain.RehydrateActionDraft(item.snapshot)
			if err != nil {
				return ports.DraftWindow{}, fmt.Errorf(
					"inmemory: восстановление черновика %s: %w", item.snapshot.ID, err,
				)
			}
			window.Drafts = append(window.Drafts, draft)
		}
		window.HasMore = end < len(ordered)
		if window.HasMore {
			window.Next = ordered[end-1].cursor
		}
	}
	return window, nil
}

// Clone возвращает независимую копию репозитория для одной транзакции.
//
// Копируются только заголовки карты и снимки: срезы внутри снимка после
// создания не мутируются (Save всегда заменяет снимок целиком), поэтому
// разделяемое владение ими безопасно.
func (r *Repository) Clone() *Repository {
	r.mu.RLock()
	defer r.mu.RUnlock()
	clone := New()
	for id, snapshot := range r.campaigns {
		clone.campaigns[id] = snapshot
	}
	return clone
}

// ReplaceWith заменяет состояние репозитория состоянием другого. Вызывается
// unit-of-work в момент commit: рабочая копия транзакции становится основным
// состоянием. При откате ReplaceWith не вызывается, и копия отбрасывается.
func (r *Repository) ReplaceWith(other *Repository) {
	other.mu.RLock()
	snapshots := make(map[domain.CampaignID]domain.CampaignSnapshot, len(other.campaigns))
	for id, snapshot := range other.campaigns {
		snapshots[id] = snapshot
	}
	other.mu.RUnlock()

	r.mu.Lock()
	r.campaigns = snapshots
	r.mu.Unlock()
}
