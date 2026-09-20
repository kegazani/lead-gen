package outreachorchestration_test

import (
	"fmt"
	"testing"
	"time"

	composition "lead-gen/backend/contexts/outreach-orchestration"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
)

// Размерность кампании из раздела 5 BRD и требование «не менее 50 черновиков за
// одно решение». Полный объём 50 000 черновиков — целевой замер; в short-режиме
// объём уменьшается, чтобы быстрый прогон оставался выполнимым, но проверка
// порога 50 черновиков на пакет сохраняется.
const (
	throughputDraftsFull           = 50_000
	throughputDraftsShort          = 5_000
	throughputMinDraftsPerDecision = 50
)

// TestBatchApprovalThroughput — замер пропускной способности задачи 6.2.
//
// Замер идёт по доменному пути пакетного утверждения на in-memory хранилище:
// BuildApprovalBatch нарезает однородную кампанию на шарды по лимитам, а
// ApproveBatch фиксирует решение по каждому шарду. Сценарий композиционного
// пути с UnitOfWork на объёме 50 000 черновиков непригоден для замера: каждая
// транзакция клонирует и рехидратирует агрегат целиком, поэтому время растёт
// сверхлинейно и измеряло бы издержки in-memory адаптера, а не пропускную
// способность решений оператора.
//
// Показатель соответствия — не среднее время, а число черновиков за одно
// решение. Лимиты берутся из собранного composition.Build, то есть ровно те,
// что видит процесс. Если за одно решение утверждается меньше 50 черновиков или
// число решений превышает бюджет BRD (пакет по 50), несоответствие фиксируется
// явной ошибкой, а не усреднением.
func TestBatchApprovalThroughput(t *testing.T) {
	drafts := throughputDraftsFull
	if testing.Short() {
		drafts = throughputDraftsShort
		t.Logf("short-режим: замер на %d черновиках вместо %d", drafts, throughputDraftsFull)
	}

	slice, err := composition.Build(composition.Params{
		Clock: fixedClock{now: e2eNow()},
		// Пакетный путь меряется без проверок риска: Build требует предикат
		// явно, поэтому передаётся осознанная заглушка, а не nil.
		Risk: composition.NoUpstreamRiskChecks,
	})
	if err != nil {
		t.Fatalf("сборка контекста среза: %v", err)
	}
	limits := slice.BatchLimits
	if limits.MinSize < throughputMinDraftsPerDecision {
		t.Fatalf("минимальный размер пакета %d ниже порога %d",
			limits.MinSize, throughputMinDraftsPerDecision)
	}

	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("разбор канала: %v", err)
	}
	window, err := domain.NewCampaignWindow(e2eNow(), e2eNow().Add(72*time.Hour))
	if err != nil {
		t.Fatalf("окно кампании: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-throughput",
		TenantID: e2eTenant,
		Name:     "Кампания замера пропускной способности",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("создание кампании: %v", err)
	}

	setupStart := time.Now()
	for i := 0; i < drafts; i++ {
		draftID := domain.DraftID(fmt.Sprintf("draft-%06d", i))
		spec := domain.DraftSpec{
			Material:  "material-throughput",
			Segment:   "segment-throughput",
			Recipient: domain.RecipientID(fmt.Sprintf("recipient-%06d", i)),
			Channel:   channel,
		}
		if _, _, err := campaign.DraftAction(draftID, spec, e2eNow()); err != nil {
			t.Fatalf("постановка черновика %s: %v", draftID, err)
		}
	}
	campaign.PullDomainEvents()
	setupElapsed := time.Since(setupStart)

	start := time.Now()
	approved := 0
	decisions := 0
	minShard, maxShard := 0, 0
	for {
		built, err := campaign.BuildApprovalBatch(limits, composition.NoUpstreamRiskChecks)
		if err != nil {
			t.Fatalf("формирование состава: %v", err)
		}
		batches := built.Batches()
		if len(batches) == 0 {
			break
		}
		for _, batch := range batches {
			size := batch.Size()
			if size < limits.MinSize || size > limits.ShardSize {
				t.Fatalf("размер шарда %d вне лимитов [%d, %d]",
					size, limits.MinSize, limits.ShardSize)
			}
			if minShard == 0 || size < minShard {
				minShard = size
			}
			if size > maxShard {
				maxShard = size
			}

			entries := batch.Entries()
			items := make([]domain.BatchApprovalItem, 0, len(entries))
			for i, entry := range entries {
				items = append(items, domain.BatchApprovalItem{
					DecisionID:   domain.DecisionID(fmt.Sprintf("decision-%06d-%06d", decisions, i)),
					DraftID:      entry.DraftID,
					DraftVersion: entry.Version,
				})
			}
			recorded, _, err := campaign.ApproveBatch(domain.BatchApprovalParams{
				Actor:       e2eOperator,
				DecidedAt:   e2eNow(),
				Digest:      batch.Digest(),
				Composition: items,
			}, composition.NoUpstreamRiskChecks)
			if err != nil {
				t.Fatalf("пакетное утверждение шарда: %v", err)
			}
			if len(recorded) != size {
				t.Fatalf("решений %d на шард из %d черновиков", len(recorded), size)
			}
			campaign.PullDomainEvents()
			approved += size
			decisions++
		}
	}
	elapsed := time.Since(start)

	if approved != drafts {
		t.Fatalf("утверждено %d черновиков, ожидалось %d", approved, drafts)
	}
	if decisions == 0 {
		t.Fatal("ни одного пакетного решения не принято")
	}

	draftsPerDecision := approved / decisions
	brdBudget := (drafts + throughputMinDraftsPerDecision - 1) / throughputMinDraftsPerDecision
	t.Logf(
		"пропускная способность: %d черновиков за %d решений (%.1f за решение), "+
			"шарды [%d, %d], бюджет BRD %d решений, подготовка %v, решения %v, %.0f черновиков/с",
		approved, decisions, float64(approved)/float64(decisions),
		minShard, maxShard, brdBudget, setupElapsed, elapsed,
		float64(approved)/elapsed.Seconds(),
	)

	// Явный показатель несоответствия, а не среднее время: порог «не менее 50
	// черновиков за одно решение» обязан выполняться на каждом решении.
	if draftsPerDecision < throughputMinDraftsPerDecision {
		t.Errorf(
			"несоответствие пропускной способности: %d черновиков за одно решение, требуется не менее %d",
			draftsPerDecision, throughputMinDraftsPerDecision,
		)
	}
	// Число решений не должно превышать бюджет раздела 5 BRD: кампания
	// утверждается пакетами по 50, то есть не более ceil(N/50) операций.
	if decisions > brdBudget {
		t.Errorf(
			"несоответствие пропускной способности: %d решений превышают бюджет BRD в %d решений",
			decisions, brdBudget,
		)
	}
}
