package outreachorchestration_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

// Однородный пакет сквозного сценария задачи 6.1: число черновиков не меньше
// минимального размера пакета (50) и не больше размера шарда (100).
const (
	batchE2EDrafts      = 60
	batchE2EMaterial    = "material-1"
	batchE2ESegment     = "segment-1"
	batchE2EOtherDraft  = "draft-other"
	batchE2ERiskyDraft  = "draft-risky"
	batchE2EMaxShard    = 100
	batchE2EMinDecision = 50
)

// batchCompositionItem — позиция состава в ответе HTTP-адаптера.
type batchCompositionItem struct {
	DraftID      string `json:"draft_id"`
	DraftVersion int    `json:"draft_version"`
	MaterialID   string `json:"material_id"`
	SegmentID    string `json:"segment_id"`
	RecipientID  string `json:"recipient_id"`
	Channel      string `json:"channel"`
}

// batchCompositionShard — шард состава с digest.
type batchCompositionShard struct {
	MaterialID string                 `json:"material_id"`
	SegmentID  string                 `json:"segment_id"`
	Digest     string                 `json:"digest"`
	Size       int                    `json:"size"`
	Entries    []batchCompositionItem `json:"entries"`
}

// batchExcludedItem — исключённый черновик с причинами.
type batchExcludedItem struct {
	DraftID      string   `json:"draft_id"`
	DraftVersion int      `json:"draft_version"`
	Reasons      []string `json:"reasons"`
}

// batchCompositionView — ответ показа состава пакета.
type batchCompositionView struct {
	CampaignID    string                  `json:"campaign_id"`
	Batches       []batchCompositionShard `json:"batches"`
	Excluded      []batchExcludedItem     `json:"excluded"`
	ExcludedCount int                     `json:"excluded_count"`
	Individual    []batchCompositionItem  `json:"individual"`
	HasMore       bool                    `json:"has_more"`
}

// batchApproveView — ответ пакетного решения: N решений по позициям.
type batchApproveView struct {
	CampaignID string `json:"campaign_id"`
	Digest     string `json:"digest"`
	Recorded   int    `json:"recorded"`
	EventCount int    `json:"event_count"`
	Decisions  []struct {
		DecisionID   string `json:"decision_id"`
		DraftID      string `json:"draft_id"`
		DraftVersion int    `json:"draft_version"`
		Batch        bool   `json:"batch"`
	} `json:"decisions"`
}

// batchRiskPredicate помечает один черновик признаком риска и пропускает
// остальные. Предикат чистый и детерминированный: признаки не зависят от
// порядка вызовов.
func batchRiskPredicate(risky domain.DraftID) domain.CanBeDecidedInPackage {
	return func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if req.DraftID == risky {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}
}

// createCampaignThroughHTTP создаёт кампанию через собранный HTTP-адаптер и
// требует 201 Created. Идентификатор команды идемпотентности привязан к
// кампании, чтобы повторное создание в одном срезе не вернуло прежнюю.
func createCampaignThroughHTTP(t *testing.T, fixture *e2eFixture, campaignID string) {
	t.Helper()
	body := createCampaignBody()
	body["campaign_id"] = campaignID
	body["command_id"] = "command-" + campaignID
	response, data := doJSON(t, http.MethodPost, fixture.server.URL+"/campaigns", body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании %s: статус %d, тело %s", campaignID, response.StatusCode, data)
	}
}

// createDraftThroughHTTP ставит черновик через собранный HTTP-адаптер и требует
// 201 Created. Возвращает версию черновика.
func createDraftThroughHTTP(t *testing.T, fixture *e2eFixture, campaignID, draftID, materialID, segmentID, recipientID string) int {
	t.Helper()
	body := draftActionBody()
	body["draft_id"] = draftID
	body["material_id"] = materialID
	body["segment_id"] = segmentID
	body["recipient_id"] = recipientID
	response, data := doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/"+campaignID+"/drafts", body)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("постановка черновика %s: статус %d, тело %s", draftID, response.StatusCode, data)
	}
	draft := decodeBody[struct {
		DraftID string `json:"draft_id"`
		Version int    `json:"version"`
	}](t, data)
	if draft.DraftID != draftID || draft.Version <= 0 {
		t.Fatalf("ответ постановки черновика %s: %+v", draftID, draft)
	}
	return draft.Version
}

// requestBatchComposition запрашивает состав пакета через HTTP-адаптер.
func requestBatchComposition(t *testing.T, fixture *e2eFixture, campaignID string) batchCompositionView {
	t.Helper()
	url := fmt.Sprintf("%s/campaigns/%s/batches/composition?tenant_id=%s&actor_id=%s",
		fixture.server.URL, campaignID, e2eTenant, e2eOperator)
	response, data := doJSON(t, http.MethodGet, url, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("показ состава пакета: статус %d, тело %s", response.StatusCode, data)
	}
	return decodeBody[batchCompositionView](t, data)
}

// approveBatchThroughHTTP отправляет пакетное решение с заданными digest и
// составом. Возвращает HTTP-статус и разобранное тело.
func approveBatchThroughHTTP(
	t *testing.T,
	fixture *e2eFixture,
	campaignID, materialID, segmentID, digest string,
	composition []batchCompositionItem,
) (int, batchApproveView) {
	t.Helper()
	items := make([]map[string]any, 0, len(composition))
	for _, entry := range composition {
		items = append(items, map[string]any{
			"draft_id":      entry.DraftID,
			"draft_version": entry.DraftVersion,
		})
	}
	body := map[string]any{
		"tenant_id":   e2eTenant.String(),
		"actor_id":    e2eOperator.String(),
		"material_id": materialID,
		"segment_id":  segmentID,
		"digest":      digest,
		"composition": items,
		"reason":      "",
	}
	response, data := doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/"+campaignID+"/batches/approve", body)
	return response.StatusCode, decodeBody[batchApproveView](t, data)
}

// pendingEventCount считает неподтверждённые события журнала outbox по имени.
func pendingEventCount(t *testing.T, fixture *e2eFixture, name domain.EventName) int {
	t.Helper()
	pending, err := fixture.slice.Journal.Pending(context.Background())
	if err != nil {
		t.Fatalf("чтение журнала outbox: %v", err)
	}
	count := 0
	for _, message := range pending {
		if message.Event.Name() == name {
			count++
		}
	}
	return count
}

// loadCampaign загружает агрегат из собранного репозитория среза.
func loadCampaign(t *testing.T, fixture *e2eFixture, campaignID string) *domain.Campaign {
	t.Helper()
	campaign, err := fixture.slice.Repository.FindByID(context.Background(), domain.CampaignID(campaignID))
	if err != nil {
		t.Fatalf("загрузка кампании %s: %v", campaignID, err)
	}
	return campaign
}

// TestOutreachApprovalBatchEndToEnd — сквозной сценарий задачи 6.1 через
// собранный composition.Build: однородные черновики → состав с digest → пакетное
// решение → N отдельных решений и N × action-approved плюс campaign-state-changed.
//
// Проверяются обе стороны однородности: разнородный черновик не попадает в
// пакет, рисковый исключён агрегатом и остаётся неутверждённым. Инвариант I1
// подтверждается до решения (исполнение отвергается) и после (исполняется только
// черновик с положительным решением по точной версии).
func TestOutreachApprovalBatchEndToEnd(t *testing.T) {
	const campaignID = "campaign-batch-e2e"

	fixture := newE2EFixtureWithRisk(t, batchRiskPredicate(batchE2ERiskyDraft))
	createCampaignThroughHTTP(t, fixture, campaignID)

	expected := make(map[string]struct{}, batchE2EDrafts)
	for i := 0; i < batchE2EDrafts; i++ {
		draftID := fmt.Sprintf("draft-%03d", i)
		createDraftThroughHTTP(t, fixture, campaignID,
			draftID, batchE2EMaterial, batchE2ESegment, fmt.Sprintf("recipient-%03d", i))
		expected[draftID] = struct{}{}
	}
	// Разнородный черновик: другой материал при том же сегменте.
	createDraftThroughHTTP(t, fixture, campaignID,
		batchE2EOtherDraft, "material-2", batchE2ESegment, "recipient-other")
	// Рисковый черновик: тот же ключ однородности, но признак риска от владельца
	// (стоп-лист) исключает его из пакета.
	createDraftThroughHTTP(t, fixture, campaignID,
		batchE2ERiskyDraft, batchE2EMaterial, batchE2ESegment, "recipient-risky")

	if got := pendingEventCount(t, fixture, domain.EventActionApproved); got != 0 {
		t.Fatalf("до решения action-approved в outbox: %d, ожидалось 0", got)
	}
	stateBefore := pendingEventCount(t, fixture, domain.EventCampaignStateChanged)

	// Состав пакета: один однородный шард из всех годных черновиков, рисковый
	// исключён с причиной, разнородный направлен на индивидуальное рассмотрение.
	composition := requestBatchComposition(t, fixture, campaignID)
	if composition.CampaignID != campaignID {
		t.Fatalf("кампания состава %q, ожидалась %q", composition.CampaignID, campaignID)
	}
	if len(composition.Batches) != 1 {
		t.Fatalf("шардов пакета %d, ожидался 1: %+v", len(composition.Batches), composition.Batches)
	}
	shard := composition.Batches[0]
	if shard.MaterialID != batchE2EMaterial || shard.SegmentID != batchE2ESegment {
		t.Fatalf("основание однородности шарда: %+v", shard)
	}
	if shard.Size != batchE2EDrafts || len(shard.Entries) != batchE2EDrafts {
		t.Fatalf("размер шарда %d/%d, ожидалось %d", shard.Size, len(shard.Entries), batchE2EDrafts)
	}
	if shard.Digest == "" {
		t.Fatal("digest состава пуст")
	}
	gotEntries := make(map[string]struct{}, len(shard.Entries))
	for _, entry := range shard.Entries {
		if entry.DraftVersion != 1 {
			t.Fatalf("версия позиции %s равна %d, ожидалась 1", entry.DraftID, entry.DraftVersion)
		}
		gotEntries[entry.DraftID] = struct{}{}
	}
	if _, inBatch := gotEntries[batchE2EOtherDraft]; inBatch {
		t.Fatalf("разнородный черновик %s попал в пакет", batchE2EOtherDraft)
	}
	if _, inBatch := gotEntries[batchE2ERiskyDraft]; inBatch {
		t.Fatalf("рисковый черновик %s попал в пакет", batchE2ERiskyDraft)
	}
	if len(gotEntries) != len(expected) {
		t.Fatalf("в шарде %d позиций, ожидалось %d: %+v", len(gotEntries), len(expected), gotEntries)
	}
	for draftID := range expected {
		if _, ok := gotEntries[draftID]; !ok {
			t.Fatalf("однородный черновик %s отсутствует в шарде", draftID)
		}
	}
	if composition.ExcludedCount != 1 || len(composition.Excluded) != 1 {
		t.Fatalf("исключённых %d/%d, ожидался один: %+v",
			composition.ExcludedCount, len(composition.Excluded), composition.Excluded)
	}
	excluded := composition.Excluded[0]
	if excluded.DraftID != batchE2ERiskyDraft || excluded.DraftVersion != 1 {
		t.Fatalf("исключённый черновик: %+v", excluded)
	}
	if len(excluded.Reasons) != 1 || excluded.Reasons[0] != string(domain.RiskStopRuleTriggered) {
		t.Fatalf("причины исключения: %+v", excluded.Reasons)
	}
	if len(composition.Individual) != 1 || composition.Individual[0].DraftID != batchE2EOtherDraft {
		t.Fatalf("индивидуальное рассмотрение: %+v", composition.Individual)
	}
	if composition.HasMore {
		t.Fatal("окно состава не покрыло очередь, хотя черновиков меньше лимита окна")
	}

	if shard.Size < batchE2EMinDecision || shard.Size > batchE2EMaxShard {
		t.Fatalf("размер шарда %d вне порога [%d, %d]",
			shard.Size, batchE2EMinDecision, batchE2EMaxShard)
	}

	// I1 до решения: исполнение ожидающего черновика отвергается агрегатом.
	before := loadCampaign(t, fixture, campaignID)
	if err := before.ExecuteDraft("draft-000", 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение до решения: получено %v, ожидалась ErrApprovalRequired", err)
	}

	// «Решение без просмотра»: несовпадающий digest отвергается без изменения
	// состояния, второй записи решения не появляется.
	status, _ := approveBatchThroughHTTP(t, fixture, campaignID,
		shard.MaterialID, shard.SegmentID, "tampered-digest", shard.Entries)
	if status != http.StatusConflict {
		t.Fatalf("решение с несовпадающим digest: статус %d, ожидался 409", status)
	}

	// Исключение рискового нельзя обойти: попытка включить его в состав
	// отвергается агрегатом повторной валидацией признака.
	bypass := append(append([]batchCompositionItem(nil), shard.Entries...), batchCompositionItem{
		DraftID:      batchE2ERiskyDraft,
		DraftVersion: 1,
		MaterialID:   batchE2EMaterial,
		SegmentID:    batchE2ESegment,
	})
	status, _ = approveBatchThroughHTTP(t, fixture, campaignID,
		shard.MaterialID, shard.SegmentID, shard.Digest, bypass)
	if status != http.StatusConflict {
		t.Fatalf("попытка включить рисковый черновик: статус %d, ожидался 409", status)
	}
	if got := pendingEventCount(t, fixture, domain.EventActionApproved); got != 0 {
		t.Fatalf("после отвергнутых решений action-approved в outbox: %d, ожидалось 0", got)
	}

	// Пакетное решение по digest.
	status, approved := approveBatchThroughHTTP(t, fixture, campaignID,
		shard.MaterialID, shard.SegmentID, shard.Digest, shard.Entries)
	if status != http.StatusOK {
		t.Fatalf("пакетное решение: статус %d", status)
	}
	if approved.Recorded != batchE2EDrafts || len(approved.Decisions) != batchE2EDrafts {
		t.Fatalf("записано решений %d/%d, ожидалось %d",
			approved.Recorded, len(approved.Decisions), batchE2EDrafts)
	}
	if approved.EventCount != batchE2EDrafts+1 {
		t.Fatalf("событий %d, ожидалось %d (N action-approved плюс campaign-state-changed)",
			approved.EventCount, batchE2EDrafts+1)
	}
	for _, decision := range approved.Decisions {
		if !decision.Batch {
			t.Fatalf("решение %s по черновику %s без признака пакетного принятия",
				decision.DecisionID, decision.DraftID)
		}
		if decision.DraftVersion != 1 {
			t.Fatalf("решение по черновику %s привязано к версии %d, ожидалась 1",
				decision.DraftID, decision.DraftVersion)
		}
		if _, ok := expected[decision.DraftID]; !ok {
			t.Fatalf("решение по черновику %s вне пакета", decision.DraftID)
		}
	}

	// N × action-approved и ровно одно campaign-state-changed на пакет.
	if got := pendingEventCount(t, fixture, domain.EventActionApproved); got != batchE2EDrafts {
		t.Fatalf("после решения action-approved в outbox: %d, ожидалось %d", got, batchE2EDrafts)
	}
	if got := pendingEventCount(t, fixture, domain.EventCampaignStateChanged) - stateBefore; got != 1 {
		t.Fatalf("после решения campaign-state-changed в outbox: %d, ожидалось 1", got)
	}

	// I1 после решения: исполняется только черновик с положительным решением по
	// точной версии; рисковый и разнородный остаются неутверждёнными.
	after := loadCampaign(t, fixture, campaignID)
	if err := after.ExecuteDraft("draft-000", 1); err != nil {
		t.Fatalf("исполнение утверждённого черновика: %v", err)
	}
	if err := after.ExecuteDraft("draft-000", 2); !errors.Is(err, domain.ErrDraftVersionMismatch) {
		t.Fatalf("исполнение по чужой версии: получено %v, ожидалась ErrDraftVersionMismatch", err)
	}
	if err := after.ExecuteDraft(batchE2ERiskyDraft, 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение рискового черновика: получено %v, ожидалась ErrApprovalRequired", err)
	}
	if err := after.ExecuteDraft(batchE2EOtherDraft, 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение разнородного черновика: получено %v, ожидалась ErrApprovalRequired", err)
	}
	if draft, ok := after.Draft(batchE2ERiskyDraft); !ok || draft.State() != domain.DraftStatePendingApproval {
		t.Fatalf("состояние рискового черновика после решения: %+v", draft)
	}

	// Повтор той же пакетной команды идемпотентен: второй записи решения и
	// второго события не возникает.
	status, repeated := approveBatchThroughHTTP(t, fixture, campaignID,
		shard.MaterialID, shard.SegmentID, shard.Digest, shard.Entries)
	if status != http.StatusOK || repeated.Recorded != 0 || repeated.EventCount != 0 {
		t.Fatalf("повтор пакетного решения: статус %d, записано %d, событий %d",
			status, repeated.Recorded, repeated.EventCount)
	}
	if got := pendingEventCount(t, fixture, domain.EventActionApproved); got != batchE2EDrafts {
		t.Fatalf("после повтора action-approved в outbox: %d, ожидалось %d", got, batchE2EDrafts)
	}

	// Публикация: N контрактов approved-action, по одному на черновик, с точной
	// версией. Отдельного контракта «пакет утверждён» не появляется.
	published, err := fixture.slice.Relay.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("публикация approved-action: %v", err)
	}
	if published != batchE2EDrafts {
		t.Fatalf("опубликовано %d контрактов, ожидалось %d", published, batchE2EDrafts)
	}
	delivered := fixture.sink.delivered()
	if len(delivered) != batchE2EDrafts {
		t.Fatalf("получатель получил %d контрактов, ожидалось %d", len(delivered), batchE2EDrafts)
	}
	seen := make(map[string]struct{}, len(delivered))
	for _, action := range delivered {
		if err := action.Validate(); err != nil {
			t.Fatalf("опубликованный контракт невалиден: %v", err)
		}
		if action.Name != contracts.ContractApprovedAction || action.DraftVersion != 1 {
			t.Fatalf("контракт: %+v", action)
		}
		seen[action.DraftID] = struct{}{}
	}
	if len(seen) != len(expected) {
		t.Fatalf("уникальных черновиков среди контрактов %d, ожидалось %d", len(seen), len(expected))
	}
}

// TestBatchApprovalRejectsChangedDraftVersion — инвариант I2 на пакетном пути
// (задача 6.1): доработка черновика после получения состава повышает версию,
// digest перестаёт совпадать, и решение по устаревшему составу отвергается без
// изменения состояния. Новое решение по изменённой версии не появляется, поэтому
// исполнение остаётся невозможным (I1).
func TestBatchApprovalRejectsChangedDraftVersion(t *testing.T) {
	const campaignID = "campaign-batch-i2"

	fixture := newE2EFixture(t)
	createCampaignThroughHTTP(t, fixture, campaignID)

	for i := 0; i < batchE2EDrafts; i++ {
		createDraftThroughHTTP(t, fixture, campaignID,
			fmt.Sprintf("draft-%03d", i), batchE2EMaterial, batchE2ESegment, fmt.Sprintf("recipient-%03d", i))
	}

	composition := requestBatchComposition(t, fixture, campaignID)
	if len(composition.Batches) != 1 {
		t.Fatalf("шардов пакета %d, ожидался 1", len(composition.Batches))
	}
	shard := composition.Batches[0]

	// Доработка одной позиции после получения состава: точная версия меняется.
	response, data := doJSON(t, http.MethodPost,
		fixture.server.URL+"/campaigns/"+campaignID+"/drafts/draft-000/revise", map[string]any{
			"tenant_id":        e2eTenant.String(),
			"actor_id":         e2eOperator.String(),
			"expected_version": 1,
			"material_id":      batchE2EMaterial,
			"segment_id":       batchE2ESegment,
			"recipient_id":     "recipient-revised",
			"channel":          domain.ActionChannelEmailName,
		})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("доработка черновика: статус %d, тело %s", response.StatusCode, data)
	}
	revised := decodeBody[struct {
		Version int `json:"version"`
	}](t, data)
	if revised.Version != 2 {
		t.Fatalf("версия после доработки %d, ожидалась 2", revised.Version)
	}

	status, result := approveBatchThroughHTTP(t, fixture, campaignID,
		shard.MaterialID, shard.SegmentID, shard.Digest, shard.Entries)
	if status != http.StatusConflict {
		t.Fatalf("решение по устаревшему составу: статус %d, ожидался 409", status)
	}
	if result.Recorded != 0 {
		t.Fatalf("по устаревшему составу записано решений: %d, ожидалось 0", result.Recorded)
	}
	if got := pendingEventCount(t, fixture, domain.EventActionApproved); got != 0 {
		t.Fatalf("после отвергнутого решения action-approved в outbox: %d, ожидалось 0", got)
	}

	// Изменённая версия не исполняется без нового решения; остальные позиции
	// прежнего состава тоже не получили решения.
	campaign := loadCampaign(t, fixture, campaignID)
	if err := campaign.ExecuteDraft("draft-000", 2); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение изменённой версии: получено %v, ожидалась ErrApprovalRequired", err)
	}
	if err := campaign.ExecuteDraft("draft-001", 1); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("исполнение черновика без решения: получено %v, ожидалась ErrApprovalRequired", err)
	}
	if draft, ok := campaign.Draft("draft-000"); !ok || draft.State() != domain.DraftStatePendingApproval || draft.Version() != 2 {
		t.Fatalf("состояние доработанного черновика: %+v", draft)
	}
}
