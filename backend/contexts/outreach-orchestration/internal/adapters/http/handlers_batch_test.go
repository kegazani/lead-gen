package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// TestBatchApprovalFlowOverHTTP проходит путь пакетного решения через HTTP:
// открыть состав → увидеть digest, образец, отличия и исключённых → принять
// решение по digest. Тот же сценарий проверяет гейт «решение без просмотра»:
// команда без состава и digest отвергается, а разнородный и рисковый черновики в
// пакет не попадают.
func TestBatchApprovalFlowOverHTTP(t *testing.T) {
	const campaignID = "campaign-batch"
	riskyDraft := domain.DraftID("draft-risky")

	// Внешний предикат риска: рисковым объявлен один конкретный черновик.
	// Домен сам решает исключить его — адаптер признак не пересматривает.
	risk := func(_ context.Context, req domain.RiskFactsRequest) ([]domain.RiskReason, error) {
		if req.DraftID == riskyDraft {
			return []domain.RiskReason{domain.RiskStopRuleTriggered}, nil
		}
		return nil, nil
	}

	fixture := newFixtureWithRisk(t, risk)
	base := fixture.server.URL

	response, body := doJSON(t, http.MethodPost, base+"/campaigns", map[string]any{
		"command_id":   "command-batch",
		"campaign_id":  campaignID,
		"tenant_id":    testTenant.String(),
		"actor_id":     testOperator.String(),
		"name":         "Пакетная кампания",
		"window_start": testNow().Format(time.RFC3339),
		"window_end":   testNow().Add(24 * time.Hour).Format(time.RFC3339),
	})
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("создание кампании: статус %d, тело %s", response.StatusCode, body)
	}

	addDraft := func(draftID, material, recipient string) {
		t.Helper()
		resp, data := doJSON(t, http.MethodPost, base+"/campaigns/"+campaignID+"/drafts", map[string]any{
			"tenant_id":    testTenant.String(),
			"actor_id":     testOperator.String(),
			"draft_id":     draftID,
			"material_id":  material,
			"segment_id":   "segment-1",
			"recipient_id": recipient,
			"channel":      "email",
		})
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("постановка черновика %s: статус %d, тело %s", draftID, resp.StatusCode, data)
		}
	}

	// Три однородных черновика, один рисковый с тем же ключом и один разнородный.
	addDraft("draft-1", "material-1", "recipient-1")
	addDraft("draft-2", "material-1", "recipient-2")
	addDraft("draft-3", "material-1", "recipient-3")
	addDraft(riskyDraft.String(), "material-1", "recipient-4")
	addDraft("draft-hetero", "material-2", "recipient-5")

	// Открытие пакетной проекции очереди: рисковый исключён с причиной,
	// разнородный одиночка направлен на индивидуальное рассмотрение.
	queueURL := base + "/campaigns/" + campaignID + "/batches?tenant_id=tenant-1&actor_id=operator-1"
	response, body = doJSON(t, http.MethodGet, queueURL, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("пакетная очередь: статус %d, тело %s", response.StatusCode, body)
	}
	queue := decodeBody[batchQueueResponse](t, body)
	if len(queue.Batches) != 1 {
		t.Fatalf("пакетов в проекции: %d, ожидался 1 (%+v)", len(queue.Batches), queue.Batches)
	}
	shard := queue.Batches[0]
	if shard.Size != 3 || shard.Digest == "" {
		t.Fatalf("шард проекции: %+v", shard)
	}
	if shard.MaterialID != "material-1" || shard.SegmentID != "segment-1" {
		t.Fatalf("основание однородности проекции: %+v", shard)
	}
	if queue.ExcludedCount != 1 || len(queue.Excluded) != 1 {
		t.Fatalf("исключённых в проекции: %d/%d", queue.ExcludedCount, len(queue.Excluded))
	}
	if excluded := queue.Excluded[0]; excluded.DraftID != riskyDraft.String() ||
		len(excluded.Reasons) != 1 || excluded.Reasons[0] != string(domain.RiskStopRuleTriggered) {
		t.Fatalf("исключённый в проекции: %+v", excluded)
	}
	if len(queue.Individual) != 1 || queue.Individual[0].DraftID != "draft-hetero" {
		t.Fatalf("индивидуальное рассмотрение: %+v", queue.Individual)
	}

	// Показ состава: digest шарда, образец содержимого, отличия и исключённые с
	// причинами. Разнородный и рисковый черновики в состав не входят.
	compositionURL := base + "/campaigns/" + campaignID + "/batches/composition?tenant_id=tenant-1&actor_id=operator-1"
	response, body = doJSON(t, http.MethodGet, compositionURL, nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("состав пакета: статус %d, тело %s", response.StatusCode, body)
	}
	composition := decodeBody[batchCompositionResponse](t, body)
	if composition.HasMore || composition.NextCursor != nil {
		t.Fatalf("окно не покрыло очередь: %+v", composition)
	}
	if len(composition.Batches) != 1 {
		t.Fatalf("составов: %d, ожидался 1 (%+v)", len(composition.Batches), composition.Batches)
	}
	batch := composition.Batches[0]
	if batch.Digest == "" || batch.Digest != shard.Digest {
		t.Fatalf("digest состава %q не совпал с проекцией %q", batch.Digest, shard.Digest)
	}
	if batch.Sample.RecipientID != "recipient-1" || batch.Sample.MaterialID != "material-1" {
		t.Fatalf("образец содержимого: %+v", batch.Sample)
	}
	if len(batch.Entries) != 3 {
		t.Fatalf("позиций состава: %d, ожидалось 3", len(batch.Entries))
	}
	if len(batch.Differences) != 2 {
		t.Fatalf("отличий в составе: %d, ожидалось 2 (%+v)", len(batch.Differences), batch.Differences)
	}
	for _, difference := range batch.Differences {
		if difference.Field != "recipient" || difference.From != "recipient-1" {
			t.Fatalf("отличие не от образца: %+v", difference)
		}
	}
	if composition.ExcludedCount != 1 || len(composition.Excluded) != 1 ||
		composition.Excluded[0].DraftID != riskyDraft.String() {
		t.Fatalf("исключённые в составе: %+v", composition.Excluded)
	}
	for _, entry := range batch.Entries {
		if entry.DraftID == riskyDraft.String() || entry.DraftID == "draft-hetero" {
			t.Fatalf("недопустимый черновик в составе: %+v", entry)
		}
	}

	approveURL := base + "/campaigns/" + campaignID + "/batches/approve"
	approveBody := func(digest string, composition []map[string]any) map[string]any {
		return map[string]any{
			"tenant_id":   testTenant.String(),
			"actor_id":    testOperator.String(),
			"material_id": "material-1",
			"segment_id":  "segment-1",
			"digest":      digest,
			"composition": composition,
			"reason":      "",
		}
	}
	fullComposition := []map[string]any{
		{"draft_id": "draft-1", "draft_version": 1},
		{"draft_id": "draft-2", "draft_version": 1},
		{"draft_id": "draft-3", "draft_version": 1},
	}

	// Гейт «решение без просмотра»: без digest решение не принимается.
	response, body = doJSON(t, http.MethodPost, approveURL, approveBody("", fullComposition))
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("решение без digest: статус %d, тело %s", response.StatusCode, body)
	}

	// Без состава — тоже отказ, даже если digest предъявлен.
	response, body = doJSON(t, http.MethodPost, approveURL, approveBody(batch.Digest, nil))
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("решение без состава: статус %d, тело %s", response.StatusCode, body)
	}

	// Чужой digest отвергается без изменения состояния.
	response, body = doJSON(t, http.MethodPost, approveURL, approveBody("чужой-digest", fullComposition))
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("решение с чужим digest: статус %d, тело %s", response.StatusCode, body)
	}
	if got := countApproved(t, fixture); got != 0 {
		t.Fatalf("отказы изменили состояние: action-approved %d", got)
	}

	// Решение с полученным digest проходит: по одному решению на черновик.
	response, body = doJSON(t, http.MethodPost, approveURL, approveBody(batch.Digest, fullComposition))
	if response.StatusCode != http.StatusOK {
		t.Fatalf("пакетное решение: статус %d, тело %s", response.StatusCode, body)
	}
	approved := decodeBody[batchApproveResponse](t, body)
	if approved.Digest != batch.Digest || approved.Recorded != 3 || len(approved.Decisions) != 3 {
		t.Fatalf("ответ пакетного решения: %+v", approved)
	}
	for _, decision := range approved.Decisions {
		if !decision.Batch {
			t.Fatalf("решение не помечено пакетным: %+v", decision)
		}
	}
	if got := countApproved(t, fixture); got != 3 {
		t.Fatalf("событий action-approved: %d, ожидалось 3", got)
	}

	// Журнал по черновику возвращает его собственное решение с признаком
	// пакетного принятия.
	response, body = doJSON(t, http.MethodGet,
		base+"/campaigns/"+campaignID+"/drafts/draft-1/decisions?tenant_id=tenant-1&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("журнал решений: статус %d, тело %s", response.StatusCode, body)
	}
	journal := decodeBody[decisionJournalResponse](t, body)
	if len(journal.Entries) != 1 {
		t.Fatalf("записей журнала: %d, ожидалась 1", len(journal.Entries))
	}
	if entry := journal.Entries[0]; !entry.Batch || entry.Outcome != string(domain.DecisionApproved) {
		t.Fatalf("запись журнала: %+v", entry)
	}

	// Рисковый черновик остался на индивидуальное рассмотрение: пакет его не
	// утвердил.
	response, body = doJSON(t, http.MethodGet,
		base+"/campaigns?campaign_id="+campaignID+"&tenant_id=tenant-1&actor_id=operator-1", nil)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("очередь утверждения: статус %d, тело %s", response.StatusCode, body)
	}
	pending := decodeBody[approvalQueueResponse](t, body)
	foundRisky := false
	for _, item := range pending.Items {
		if item.DraftID == riskyDraft.String() {
			foundRisky = true
		}
	}
	if !foundRisky {
		t.Fatalf("рисковый черновик пропал из очереди: %+v", pending.Items)
	}
}
