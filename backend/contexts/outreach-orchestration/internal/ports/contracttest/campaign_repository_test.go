package contracttest

import (
	"context"
	"sort"
	"sync"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// stubRepository — минимальный тест-дубль репозитория, которым проверяется сам
// contract suite. Это не адаптер и не in-memory реализация среза: дубль лишь
// хранит указатели на агрегаты и нужен только затем, чтобы `go test` прогонял
// suite без внешней инфраструктуры.
type stubRepository struct {
	mu        sync.Mutex
	campaigns map[domain.CampaignID]*domain.Campaign
}

// stubRepository реализует и базовый порт, и расширение загрузки окна, чтобы
// оба contract suite прогонялись без внешней инфраструктуры.
var _ ports.DraftWindowRepository = (*stubRepository)(nil)

func newStubRepository() *stubRepository {
	return &stubRepository{campaigns: make(map[domain.CampaignID]*domain.Campaign)}
}

func (r *stubRepository) Save(_ context.Context, campaign *domain.Campaign) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.campaigns[campaign.ID()] = campaign
	return nil
}

func (r *stubRepository) FindByID(_ context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	campaign, ok := r.campaigns[id]
	if !ok {
		return nil, ports.ErrCampaignNotFound
	}
	return campaign, nil
}

// LoadDraftWindow реализует ports.DraftWindowRepository. Дубль собирает ключи
// черновиков, отбрасывает всё не строго после курсора, сортирует контрактным
// порядком и отдаёт страницу. Эффективность дубля не важна: suite проверяет
// контракт порта, а не производительность реализации.
func (r *stubRepository) LoadDraftWindow(_ context.Context, query ports.DraftWindowQuery) (ports.DraftWindow, error) {
	if err := query.Validate(); err != nil {
		return ports.DraftWindow{}, err
	}
	r.mu.Lock()
	campaign, ok := r.campaigns[query.CampaignID]
	r.mu.Unlock()
	if !ok {
		return ports.DraftWindow{}, ports.ErrCampaignNotFound
	}
	if campaign.TenantID() != query.TenantID {
		// Чужой тенант неотличим от отсутствующей кампании.
		return ports.DraftWindow{}, ports.ErrCampaignNotFound
	}

	type keyedDraft struct {
		key   ports.DraftWindowCursor
		draft *domain.ActionDraft
	}
	entries := make([]keyedDraft, 0, len(campaign.Drafts()))
	for _, draft := range campaign.Drafts() {
		key := ports.CursorOf(draft)
		if !query.After.IsZero() && ports.CompareDraftKey(key, query.After) <= 0 {
			continue
		}
		entries = append(entries, keyedDraft{key: key, draft: draft})
	}
	sort.Slice(entries, func(i, j int) bool {
		return ports.CompareDraftKey(entries[i].key, entries[j].key) < 0
	})

	window := ports.DraftWindow{}
	if len(entries) > query.Limit {
		window.HasMore = true
		entries = entries[:query.Limit]
	}
	for _, entry := range entries {
		window.Drafts = append(window.Drafts, entry.draft)
	}
	if window.HasMore && len(window.Drafts) > 0 {
		window.Next = ports.CursorOf(window.Drafts[len(window.Drafts)-1])
	}
	return window, nil
}

// TestCampaignRepositoryContract прогоняет общий suite против тест-дубля. Он
// подтверждает, что suite компилируется и исполняется; реальные адаптеры
// подключают его через свои фабрики.
func TestCampaignRepositoryContract(t *testing.T) {
	RunCampaignRepositoryContract(t, func(t *testing.T) ports.CampaignRepository {
		t.Helper()
		return newStubRepository()
	})
}

// TestDraftWindowContract прогоняет расширение suite загрузки окна против того
// же тест-дубля. Реальные адаптеры подключают его через свои фабрики.
func TestDraftWindowContract(t *testing.T) {
	RunDraftWindowContract(t, func(t *testing.T) ports.DraftWindowRepository {
		t.Helper()
		return newStubRepository()
	})
}
