package contracttest

import (
	"context"
	"reflect"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// InsightStoreFactory создаёт хранилище инсайтов для одного прогона suite с
// пустым состоянием.
type InsightStoreFactory func(t *testing.T) ports.InsightStore

// RunInsightStoreContract проверяет, что реализация InsightStore соблюдает
// контракт порта: сохраняет и восстанавливает снимок инсайтов (для
// воспроизводимости), различает тенантов и идемпотентна по ключу применения.
func RunInsightStoreContract(t *testing.T, newStore InsightStoreFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("пустое хранилище", func(t *testing.T) {
		store := newStore(t)
		snapshots, err := store.LoadSnapshots(ctx)
		if err != nil {
			t.Fatalf("LoadSnapshots: %v", err)
		}
		if len(snapshots) != 0 {
			t.Fatalf("пустое хранилище вернуло %d снимков", len(snapshots))
		}
		applications, err := store.LoadApplications(ctx)
		if err != nil {
			t.Fatalf("LoadApplications: %v", err)
		}
		if len(applications) != 0 {
			t.Fatalf("пустое хранилище вернуло %d применений", len(applications))
		}
	})

	t.Run("снимок сохраняется и восстанавливается", func(t *testing.T) {
		store := newStore(t)
		snapshot := readmodel.InsightSnapshot{
			TenantID:       "t1",
			Watermark:      baseTime,
			FormulaVersion: readmodel.InsightFormulaVersion,
			Outcomes: []readmodel.InsightOutcome{
				{TenantID: "t1", JobID: "job-a", CompanyID: "c1", Source: "crawl", Industry: "saas",
					Verdict: readmodel.VerdictHypothesis, Reason: readmodel.ReasonNoSignal, OccurredAt: baseTime},
				{TenantID: "t1", JobID: "job-a", CompanyID: "c2", Source: "crawl", Industry: "saas",
					Verdict: readmodel.VerdictUnsuitable, Reason: readmodel.ReasonRefuted, OccurredAt: baseTime.Add(time.Hour)},
			},
		}
		if err := store.SaveSnapshot(ctx, snapshot); err != nil {
			t.Fatalf("SaveSnapshot: %v", err)
		}

		snapshots, err := store.LoadSnapshots(ctx)
		if err != nil {
			t.Fatalf("LoadSnapshots: %v", err)
		}
		if len(snapshots) != 1 {
			t.Fatalf("загружено %d снимков, ожидался 1", len(snapshots))
		}
		got := snapshots[0]
		if got.TenantID != snapshot.TenantID || got.FormulaVersion != snapshot.FormulaVersion {
			t.Fatalf("снимок не восстановлен:\nполучено: %+v\nожидалось: %+v", got, snapshot)
		}
		// Момент времени сравнивается по инстанту: PostgreSQL отдаёт timestamptz в
		// локали соединения, поэтому location незначима (см. ProjectionStore).
		if !got.Watermark.Equal(snapshot.Watermark) {
			t.Fatalf("watermark не восстановлен: получено %s, ожидалось %s", got.Watermark, snapshot.Watermark)
		}
		if !reflect.DeepEqual(got.Outcomes, snapshot.Outcomes) {
			t.Fatalf("исходы снимка не восстановлены:\nполучено: %+v\nожидалось: %+v", got.Outcomes, snapshot.Outcomes)
		}
	})

	t.Run("снимок заменяется по тенанту", func(t *testing.T) {
		store := newStore(t)
		if err := store.SaveSnapshot(ctx, readmodel.InsightSnapshot{TenantID: "t1", FormulaVersion: "v1"}); err != nil {
			t.Fatalf("первый SaveSnapshot: %v", err)
		}
		latest := readmodel.InsightSnapshot{TenantID: "t1", FormulaVersion: "v2", Watermark: baseTime}
		if err := store.SaveSnapshot(ctx, latest); err != nil {
			t.Fatalf("второй SaveSnapshot: %v", err)
		}
		snapshots, err := store.LoadSnapshots(ctx)
		if err != nil {
			t.Fatalf("LoadSnapshots: %v", err)
		}
		if len(snapshots) != 1 || snapshots[0].FormulaVersion != "v2" {
			t.Fatalf("снимок не заменён: %+v", snapshots)
		}
	})

	t.Run("применение фиксируется и заменяется по ключу", func(t *testing.T) {
		store := newStore(t)
		application := readmodel.RecommendationApplication{
			TenantID: "t1", ScopeKind: "industry", ScopeKey: "saas",
			Action: "добавить цели обхода", Reason: readmodel.ReasonNoSignal, Basis: "наблюдений 6",
			AppliedAt: baseTime, Reference: "target-1",
			BeforeWatermark: baseTime,
			Before:          readmodel.InsightSummary{Total: 6, Verdicts: readmodel.VerdictCounts{Hypothesis: 6}},
		}
		if err := store.SaveApplication(ctx, application); err != nil {
			t.Fatalf("SaveApplication: %v", err)
		}
		updated := application
		updated.Reference = "target-2"
		if err := store.SaveApplication(ctx, updated); err != nil {
			t.Fatalf("повторный SaveApplication: %v", err)
		}

		applications, err := store.LoadApplications(ctx)
		if err != nil {
			t.Fatalf("LoadApplications: %v", err)
		}
		if len(applications) != 1 {
			t.Fatalf("загружено %d применений, ожидалось 1", len(applications))
		}
		got := applications[0]
		if got.TenantID != updated.TenantID || got.ScopeKind != updated.ScopeKind || got.ScopeKey != updated.ScopeKey ||
			got.Action != updated.Action || got.Reason != updated.Reason || got.Basis != updated.Basis ||
			got.Reference != updated.Reference {
			t.Fatalf("применение не восстановлено:\nполучено: %+v\nожидалось: %+v", got, updated)
		}
		if !got.AppliedAt.Equal(updated.AppliedAt) || !got.BeforeWatermark.Equal(updated.BeforeWatermark) {
			t.Fatalf("отметки применения не восстановлены: получено %s/%s, ожидалось %s/%s",
				got.AppliedAt, got.BeforeWatermark, updated.AppliedAt, updated.BeforeWatermark)
		}
		if !reflect.DeepEqual(got.Before, updated.Before) {
			t.Fatalf("снимок «до» не восстановлен:\nполучено: %+v\nожидалось: %+v", got.Before, updated.Before)
		}
	})

	t.Run("различение тенантов", func(t *testing.T) {
		store := newStore(t)
		if err := store.SaveSnapshot(ctx, readmodel.InsightSnapshot{TenantID: "t1"}); err != nil {
			t.Fatalf("SaveSnapshot t1: %v", err)
		}
		if err := store.SaveSnapshot(ctx, readmodel.InsightSnapshot{TenantID: "t2"}); err != nil {
			t.Fatalf("SaveSnapshot t2: %v", err)
		}
		snapshots, err := store.LoadSnapshots(ctx)
		if err != nil {
			t.Fatalf("LoadSnapshots: %v", err)
		}
		if len(snapshots) != 2 {
			t.Fatalf("загружено %d снимков, ожидалось 2", len(snapshots))
		}
	})
}
