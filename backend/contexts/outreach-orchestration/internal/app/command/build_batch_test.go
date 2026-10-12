package command

import (
	"context"
	"errors"
	"sort"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// LoadDraftWindow дополняет тест-дубль fakeRepository до
// ports.DraftWindowRepository: сценарий BuildBatch работает только через окно.
// Порядок и keyset-переход повторяют контракт порта, чтобы тесты видели окно
// таким же, как рабочая реализация.
func (r *fakeRepository) LoadDraftWindow(_ context.Context, query ports.DraftWindowQuery) (ports.DraftWindow, error) {
	if err := query.Validate(); err != nil {
		return ports.DraftWindow{}, err
	}
	if r.findErr != nil {
		return ports.DraftWindow{}, r.findErr
	}
	campaign, ok := r.campaigns[query.CampaignID]
	if !ok {
		return ports.DraftWindow{}, ports.ErrCampaignNotFound
	}
	if campaign.TenantID() != query.TenantID {
		return ports.DraftWindow{}, ports.ErrCampaignNotFound
	}

	ordered := campaign.Drafts()
	sort.Slice(ordered, func(i, j int) bool {
		return ports.CompareDraftKey(ports.CursorOf(ordered[i]), ports.CursorOf(ordered[j])) < 0
	})
	start := sort.Search(len(ordered), func(i int) bool {
		return ports.CompareDraftKey(ports.CursorOf(ordered[i]), query.After) > 0
	})
	end := start + query.Limit
	if end > len(ordered) {
		end = len(ordered)
	}

	window := ports.DraftWindow{}
	if start < len(ordered) {
		window.Drafts = append([]*domain.ActionDraft(nil), ordered[start:end]...)
		window.HasMore = end < len(ordered)
		if window.HasMore {
			window.Next = ports.CursorOf(ordered[end-1])
		}
	}
	return window, nil
}

// windowOnlyRepository — репозиторий окна для сценария BuildBatch. Полная
// загрузка агрегата намеренно запрещена: FindByID возвращает ошибку, поэтому
// успешный Handle доказывает, что use case ходит именно за окном.
type windowOnlyRepository struct {
	*fakeRepository

	drafts  []*domain.ActionDraft
	hasMore bool
	next    ports.DraftWindowCursor

	loads     int
	lastQuery ports.DraftWindowQuery
}

func (r *windowOnlyRepository) FindByID(_ context.Context, _ domain.CampaignID) (*domain.Campaign, error) {
	return nil, errors.New("build_batch: полная загрузка кампании запрещена")
}

func (r *windowOnlyRepository) LoadDraftWindow(_ context.Context, query ports.DraftWindowQuery) (ports.DraftWindow, error) {
	if err := query.Validate(); err != nil {
		return ports.DraftWindow{}, err
	}
	// Дубль повторяет границу тенанта рабочей реализации: чужой тенант неотличим
	// от отсутствующей кампании, поэтому сценарий не увидит чужой состав даже
	// при заранее заготовленном окне.
	campaign, ok := r.campaigns[query.CampaignID]
	if !ok || campaign.TenantID() != query.TenantID {
		return ports.DraftWindow{}, ports.ErrCampaignNotFound
	}
	r.loads++
	r.lastQuery = query
	return ports.DraftWindow{Drafts: r.drafts, HasMore: r.hasMore, Next: r.next}, nil
}

// TestBuildBatchCompositionFromWindowMatchesAggregate проверяет, что состав из
// окна совпадает с составом по агрегату для одного набора черновиков: тот же
// ключ однородности, размер и digest.
func TestBuildBatchCompositionFromWindowMatchesAggregate(t *testing.T) {
	kit := newTestKit(t)
	campaign := kit.mustBatchCampaign(t, 3)

	repository := &windowOnlyRepository{fakeRepository: kit.repo, drafts: campaign.PendingApproval()}
	viaWindow, err := NewBuildBatchHandler(repository, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	viaAggregate, err := campaign.BuildApprovalBatch(context.Background(), batchLimitsForTest(), riskFor())
	if err != nil {
		t.Fatalf("BuildApprovalBatch: %v", err)
	}

	if repository.loads != 1 {
		t.Fatalf("окно загружено %d раз, ожидался 1", repository.loads)
	}
	if repository.lastQuery.CampaignID != domain.CampaignID("campaign-1") {
		t.Fatalf("кампания окна: got %q", repository.lastQuery.CampaignID)
	}
	if repository.lastQuery.TenantID != domain.TenantID("tenant-1") {
		t.Fatalf("тенант окна: got %q", repository.lastQuery.TenantID)
	}
	if repository.lastQuery.Limit != batchLimitsForTest().MaxSize {
		t.Fatalf("лимит окна: got %d, want %d", repository.lastQuery.Limit, batchLimitsForTest().MaxSize)
	}
	if len(viaWindow.Batches) != len(viaAggregate.Batches()) {
		t.Fatalf("шардов из окна %d, из агрегата %d", len(viaWindow.Batches), len(viaAggregate.Batches()))
	}
	for i, batch := range viaWindow.Batches {
		fromAggregate := viaAggregate.Batches()[i]
		if batch.Key != fromAggregate.Key() || batch.Size != fromAggregate.Size() {
			t.Fatalf("состав из окна отличается от состава из агрегата: %+v / %+v", batch, fromAggregate)
		}
		if batch.Digest != fromAggregate.Digest() {
			t.Fatalf("digest одного состава должен совпадать: окно %s, агрегат %s", batch.Digest, fromAggregate.Digest())
		}
	}
}

// TestBuildBatchReportsWindowContinuation проверяет, что хвост очереди за окном
// не теряется молча: результат сообщает HasMore и курсор продолжения, а
// следующий вызов по курсору забирает остаток.
func TestBuildBatchReportsWindowContinuation(t *testing.T) {
	kit := newTestKit(t)
	campaign := kit.mustBatchCampaign(t, 5)
	pending := campaign.PendingApproval()

	next := ports.CursorOf(pending[3])
	repository := &windowOnlyRepository{
		fakeRepository: kit.repo,
		drafts:         pending[:4],
		hasMore:        true,
		next:           next,
	}
	handler := NewBuildBatchHandler(repository, kit.access, riskFor())

	first, err := handler.Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("первый Handle: %v", err)
	}
	if !first.HasMore {
		t.Fatal("за окном остались черновики: ожидался HasMore")
	}
	if first.NextCursor != next {
		t.Fatalf("курсор продолжения: got %+v, want %+v", first.NextCursor, next)
	}
	if len(first.Batches) != 1 || first.Batches[0].Size != 4 {
		t.Fatalf("в окне ожидался один пакет из 4: %+v", first.Batches)
	}

	// Продолжение по курсору: остаток очереди приходит вторым окном.
	repository.drafts = pending[4:]
	repository.hasMore = false
	repository.next = ports.DraftWindowCursor{}
	command := buildBatchCommand()
	command.After = first.NextCursor

	second, err := handler.Handle(context.Background(), command)
	if err != nil {
		t.Fatalf("второй Handle: %v", err)
	}
	if repository.lastQuery.After != first.NextCursor {
		t.Fatalf("курсор не передан в окно: got %+v", repository.lastQuery.After)
	}
	if second.HasMore {
		t.Fatal("окно покрыло очередь: HasMore не ожидался")
	}
	if len(second.Individual) != 1 || second.Individual[0].DraftID != domain.DraftID("draft-4") {
		t.Fatalf("остаток очереди потерян: %+v", second.Individual)
	}
}

// TestBuildBatchRejectsForeignTenant проверяет, что команда не пересекает границу
// тенанта: чужой тенант не получает состав, а отказ неотличим от отсутствующей
// кампании и покрывает все части результата.
func TestBuildBatchRejectsForeignTenant(t *testing.T) {
	kit := newTestKit(t)
	kit.mustBatchCampaign(t, 3)

	command := buildBatchCommand()
	command.TenantID = domain.TenantID("tenant-2")

	result, err := NewBuildBatchHandler(kit.repo, kit.access, riskFor()).Handle(context.Background(), command)
	if !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("чужой тенант: ожидался отказ без раскрытия кампании, получено %v", err)
	}
	if len(result.Batches) != 0 || len(result.Individual) != 0 || len(result.Excluded) != 0 {
		t.Fatalf("при отказе состав не выдаётся: %+v", result)
	}
}

// TestBuildBatchWithoutContinuationIsComplete проверяет, что полное окно не
// выдаёт себя за продолжение.
func TestBuildBatchWithoutContinuationIsComplete(t *testing.T) {
	kit := newTestKit(t)
	campaign := kit.mustBatchCampaign(t, 3)

	repository := &windowOnlyRepository{fakeRepository: kit.repo, drafts: campaign.PendingApproval()}
	result, err := NewBuildBatchHandler(repository, kit.access, riskFor()).Handle(context.Background(), buildBatchCommand())
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.HasMore {
		t.Fatal("окно покрыло всю очередь: HasMore не ожидался")
	}
	if !result.NextCursor.IsZero() {
		t.Fatalf("без продолжения курсор должен быть нулевым: %+v", result.NextCursor)
	}
}
