package readmodel

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

var insightBase = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

// seedOutcome добавляет исход в модель с заданным смещением по времени.
func seedOutcome(t *testing.T, model *InsightModel, tenant, job, company, source, industry string, verdict Verdict, reason ReasonCategory, offset time.Duration) {
	t.Helper()
	model.Apply(InsightOutcome{
		TenantID:   tenant,
		JobID:      job,
		CompanyID:  company,
		Source:     source,
		Industry:   industry,
		Verdict:    verdict,
		Reason:     reason,
		OccurredAt: insightBase.Add(offset),
	})
}

// TestInsightDistributionBySlices проверяет распределение вердиктов и причин в
// разрезах задание/источник/ниша/период (задача 4.1).
func TestInsightDistributionBySlices(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"

	// Задание job-a: 2 «подходит», 2 «гипотеза», 2 «не подходит».
	seedOutcome(t, model, tenant, "job-a", "a1", "crawl", "saas", VerdictFits, "", 0)
	seedOutcome(t, model, tenant, "job-a", "a2", "crawl", "saas", VerdictFits, "", 0)
	seedOutcome(t, model, tenant, "job-a", "a3", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "a4", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "a5", "crawl", "saas", VerdictUnsuitable, ReasonRefuted, 0)
	seedOutcome(t, model, tenant, "job-a", "a6", "crawl", "saas", VerdictUnsuitable, ReasonRefuted, 0)

	// Задание job-b на следующий день, другой источник и ниша.
	day := 24 * time.Hour
	seedOutcome(t, model, tenant, "job-b", "b1", "forum", "fintech", VerdictFits, "", day)
	seedOutcome(t, model, tenant, "job-b", "b2", "forum", "fintech", VerdictUnsuitable, ReasonRefuted, day)

	data := model.Insight(tenant, InsightFilter{})
	if !data.Visible {
		t.Fatalf("срез не виден: %s", data.HiddenReason)
	}
	if data.Total != 8 {
		t.Fatalf("Total = %d, ожидалось 8", data.Total)
	}
	if data.Verdicts.Fits != 3 || data.Verdicts.Hypothesis != 2 || data.Verdicts.Unsuitable != 3 {
		t.Fatalf("распределение вердиктов = %+v", data.Verdicts)
	}
	if got := reasonTotal(data.Reasons, ReasonNoSignal); got != 2 {
		t.Fatalf("причина «сигнал не найден» = %d, ожидалось 2", got)
	}
	if got := reasonTotal(data.Reasons, ReasonRefuted); got != 3 {
		t.Fatalf("причина «опровергнуто» = %d, ожидалось 3", got)
	}

	// Разрез по заданию: job-a — 6 записей, job-b — 2.
	if len(data.ByJob) != 2 {
		t.Fatalf("разрезов по заданию = %d, ожидалось 2", len(data.ByJob))
	}
	jobA := sliceByKey(data.ByJob, "job-a")
	if jobA == nil || jobA.Total != 6 {
		t.Fatalf("разрез job-a = %+v", jobA)
	}
	if jobA.Verdicts.Fits != 2 || jobA.Verdicts.Hypothesis != 2 || jobA.Verdicts.Unsuitable != 2 {
		t.Fatalf("вердикты job-a = %+v", jobA.Verdicts)
	}

	// Разрез по источнику и нише.
	if sliceByKey(data.BySource, "crawl") == nil || sliceByKey(data.BySource, "forum") == nil {
		t.Fatalf("разрез по источнику = %+v", data.BySource)
	}
	if sliceByKey(data.ByIndustry, "saas") == nil || sliceByKey(data.ByIndustry, "fintech") == nil {
		t.Fatalf("разрез по нише = %+v", data.ByIndustry)
	}

	// Разрез по периоду: два дня.
	if len(data.ByPeriod) != 2 {
		t.Fatalf("разрезов по периоду = %d, ожидалось 2", len(data.ByPeriod))
	}

	// Фильтр по заданию пересчитывает срез.
	filtered := model.Insight(tenant, InsightFilter{JobID: "job-b"})
	if filtered.Total != 2 || filtered.Verdicts.Fits != 1 || filtered.Verdicts.Unsuitable != 1 {
		t.Fatalf("срез по заданию job-b = %+v", filtered)
	}
	// Фильтр по нише.
	byIndustry := model.Insight(tenant, InsightFilter{Industry: "fintech"})
	if byIndustry.Total != 2 {
		t.Fatalf("срез по нише fintech = %+v", byIndustry)
	}
}

// TestInsightSeparatesExtractionError проверяет, что рост ошибок извлечения
// учитывается отдельно и не выдаётся за рост «не подходит» (задача 4.2).
func TestInsightSeparatesExtractionError(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"

	// 4 опровержения и 4 ошибки извлечения.
	for i := 0; i < 4; i++ {
		seedOutcome(t, model, tenant, "job-a", "r"+string(rune('a'+i)), "crawl", "saas", VerdictUnsuitable, ReasonRefuted, 0)
	}
	for i := 0; i < 4; i++ {
		seedOutcome(t, model, tenant, "job-a", "e"+string(rune('a'+i)), "crawl", "saas", VerdictUnsuitable, ReasonExtractionError, 0)
	}

	data := model.Insight(tenant, InsightFilter{})
	if data.Verdicts.ExtractionErrors != 4 {
		t.Fatalf("ошибок извлечения = %d, ожидалось 4", data.Verdicts.ExtractionErrors)
	}
	// Ошибка не приписана вердикту «не подходит».
	if data.Verdicts.Unsuitable != 4 {
		t.Fatalf("«не подходит» = %d, ожидалось 4 (ошибки не смешаны)", data.Verdicts.Unsuitable)
	}
	if got := reasonTotal(data.Reasons, ReasonExtractionError); got != 4 {
		t.Fatalf("категория ошибок извлечения = %d, ожидалось 4", got)
	}
	// Ошибка извлечения идёт отдельной строкой от опровержения.
	if reasonTotal(data.Reasons, ReasonRefuted) != 4 {
		t.Fatalf("опровержений = %d, ожидалось 4", reasonTotal(data.Reasons, ReasonRefuted))
	}

	// Из-за явной категории ошибки отрицательный вердикт не подменяется успехом.
	if data.Verdicts.Fits != 0 {
		t.Fatalf("«подходит» = %d, ожидалось 0", data.Verdicts.Fits)
	}
}

// TestInsightRecommendationWithBasis проверяет рекомендацию с наблюдаемым
// основанием (задача 4.3).
func TestInsightRecommendationWithBasis(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"

	// Ниша saas: 8 наблюдений, 6 из них — «сигнал не найден» (75%).
	for i := 0; i < 6; i++ {
		seedOutcome(t, model, tenant, "job-a", "n"+string(rune('a'+i)), "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	}
	seedOutcome(t, model, tenant, "job-a", "f1", "crawl", "saas", VerdictFits, "", 0)
	seedOutcome(t, model, tenant, "job-a", "f2", "crawl", "saas", VerdictFits, "", 0)

	data := model.Insight(tenant, InsightFilter{})
	if len(data.Recommendations) == 0 {
		t.Fatalf("рекомендация не сформирована: %s", data.RecommendationReason)
	}
	rec := data.Recommendations[0]
	if rec.ScopeKind != "industry" || rec.ScopeKey != "saas" {
		t.Fatalf("срез рекомендации = %+v", rec)
	}
	if rec.Reason != ReasonNoSignal {
		t.Fatalf("категория рекомендации = %q", rec.Reason)
	}
	if rec.Observations != 8 {
		t.Fatalf("наблюдений = %d, ожидалось 8", rec.Observations)
	}
	if !strings.Contains(rec.Basis, "8") || !strings.Contains(rec.Basis, "75") {
		t.Fatalf("основание без числа и доли наблюдений: %q", rec.Basis)
	}
	if rec.Action == "" {
		t.Fatalf("действие рекомендации пусто")
	}
}

// TestInsightRecommendationRefusedWithoutData проверяет запрет выдуманной
// рекомендации при недостатке данных (задача 4.3).
func TestInsightRecommendationRefusedWithoutData(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"

	// 3 наблюдения — меньше порога.
	seedOutcome(t, model, tenant, "job-a", "n1", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "n2", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "n3", "crawl", "saas", VerdictFits, "", 0)

	data := model.Insight(tenant, InsightFilter{})
	if len(data.Recommendations) != 0 {
		t.Fatalf("рекомендация при недостатке данных: %+v", data.Recommendations)
	}
	if !strings.Contains(data.RecommendationReason, "недостаточно данных") {
		t.Fatalf("причина отсутствия рекомендации = %q", data.RecommendationReason)
	}
}

// TestInsightMinimumGroupHidesSlice проверяет порог минимальной группы:
// срез из одной записи не показывается, данные не подменяются нулём (задача 4.4).
func TestInsightMinimumGroupHidesSlice(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"

	seedOutcome(t, model, tenant, "job-a", "n1", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "n2", "crawl", "saas", VerdictFits, "", 0)
	// Ниша fintech представлена одной записью — срез не показывается.
	seedOutcome(t, model, tenant, "job-a", "solo", "forum", "fintech", VerdictHypothesis, ReasonNoSignal, 0)

	data := model.Insight(tenant, InsightFilter{})
	if sliceByKey(data.ByIndustry, "fintech") != nil {
		t.Fatalf("срез из одной записи показан: %+v", data.ByIndustry)
	}
	if sliceByKey(data.ByIndustry, "saas") == nil {
		t.Fatalf("срез из двух записей пропал")
	}

	// Срез целиком ниже порога не виден и не обнуляется как факт.
	small := model.Insight(tenant, InsightFilter{Industry: "fintech"})
	if small.Visible {
		t.Fatalf("срез из одной записи виден")
	}
	if small.HasData != true {
		t.Fatalf("HasData = false, запись есть")
	}
	if small.HiddenReason == "" {
		t.Fatalf("причина скрытия не сообщена")
	}
	if small.Verdicts.Total() != 0 {
		t.Fatalf("скрытый срез отдал метрики: %+v", small.Verdicts)
	}

	// Пустой срез отличается от скрытого.
	empty := model.Insight("tenant-unknown", InsightFilter{})
	if empty.Visible || empty.HasData {
		t.Fatalf("пустой срез считан видимым: %+v", empty)
	}
}

// TestInsightNoPersonalData проверяет, что результаты инсайта не содержат
// персональных данных и ссылок на компании (задача 4.4).
func TestInsightNoPersonalData(t *testing.T) {
	for _, value := range []any{
		InsightData{}, InsightSlice{}, ReasonCount{}, Recommendation{}, InsightSummary{}, VerdictCounts{},
	} {
		typ := reflect.TypeOf(value)
		for i := 0; i < typ.NumField(); i++ {
			name := strings.ToLower(typ.Field(i).Name)
			for _, banned := range []string{"company", "email", "contact", "phone", "reply", "address", "firstname", "lastname"} {
				if strings.Contains(name, banned) {
					t.Fatalf("тип %s содержит поле %q, похожее на персональные данные", typ.Name(), typ.Field(i).Name)
				}
			}
		}
	}
}

// TestInsightSnapshotReproducible проверяет воспроизводимость снимка по watermark
// и версии формул (задача 4.6).
func TestInsightSnapshotReproducible(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"
	seedOutcome(t, model, tenant, "job-a", "a1", "crawl", "saas", VerdictFits, "", 0)
	seedOutcome(t, model, tenant, "job-a", "a2", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, time.Hour)
	seedOutcome(t, model, tenant, "job-a", "a3", "crawl", "saas", VerdictUnsuitable, ReasonRefuted, 2*time.Hour)

	before := model.Insight(tenant, InsightFilter{})
	snapshot := model.Snapshot(tenant)
	if snapshot.FormulaVersion != InsightFormulaVersion {
		t.Fatalf("версия формул снимка = %q", snapshot.FormulaVersion)
	}
	if !snapshot.Watermark.Equal(insightBase.Add(2 * time.Hour)) {
		t.Fatalf("watermark снимка = %v", snapshot.Watermark)
	}

	restored := NewInsightModel()
	restored.RestoreSnapshot(snapshot)
	after := restored.Insight(tenant, InsightFilter{})

	if !reflect.DeepEqual(before, after) {
		t.Fatalf("восстановленный снимок не воспроизводит срез:\nдо:  %+v\nпосле: %+v", before, after)
	}
	if !restored.Watermark(tenant).Equal(model.Watermark(tenant)) {
		t.Fatalf("watermark восстановления = %v, ожидался %v", restored.Watermark(tenant), model.Watermark(tenant))
	}
}

// TestInsightApplyRevisionDoesNotDoubleCount проверяет, что повторная оценка
// компании в том же задании заменяет прежнюю, не двоя счётчики.
func TestInsightApplyRevisionDoesNotDoubleCount(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"
	seedOutcome(t, model, tenant, "job-a", "a1", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "a2", "crawl", "saas", VerdictFits, "", 0)
	// Ревизия a1: теперь «подходит».
	seedOutcome(t, model, tenant, "job-a", "a1", "crawl", "saas", VerdictFits, "", time.Hour)

	data := model.Insight(tenant, InsightFilter{})
	if data.Total != 2 {
		t.Fatalf("Total = %d, ожидалось 2 (ревизия не удвоила)", data.Total)
	}
	if data.Verdicts.Fits != 2 || data.Verdicts.Hypothesis != 0 {
		t.Fatalf("вердикты после ревизии = %+v", data.Verdicts)
	}
}

// TestCompareApplicationBeforeAfter проверяет сравнение до/после применения
// рекомендации (задача 4.5).
func TestCompareApplicationBeforeAfter(t *testing.T) {
	model := NewInsightModel()
	tenant := "tenant-1"
	seedOutcome(t, model, tenant, "job-a", "a1", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)
	seedOutcome(t, model, tenant, "job-a", "a2", "crawl", "saas", VerdictHypothesis, ReasonNoSignal, 0)

	before := Summarize(model.Insight(tenant, InsightFilter{}))
	application := RecommendationApplication{
		TenantID:        tenant,
		ScopeKind:       "industry",
		ScopeKey:        "saas",
		AppliedAt:       insightBase.Add(3 * time.Hour),
		BeforeWatermark: model.Watermark(tenant),
		Before:          before,
	}

	// До нового поиска изменений нет.
	noChange := CompareApplication(application, model.Insight(tenant, InsightFilter{}))
	if noChange.Changed || noChange.NewOutcomes != 0 {
		t.Fatalf("сравнение без новых исходов = %+v", noChange)
	}

	// Прошёл новый поиск: появились новые исходы.
	seedOutcome(t, model, tenant, "job-a", "a3", "crawl", "saas", VerdictFits, "", 4*time.Hour)
	comparison := CompareApplication(application, model.Insight(tenant, InsightFilter{}))
	if !comparison.Changed {
		t.Fatalf("новые исходы не отражены в сравнении")
	}
	if comparison.NewOutcomes != 1 {
		t.Fatalf("прирост исходов = %d, ожидалось 1", comparison.NewOutcomes)
	}
	if comparison.After.Total != comparison.Before.Total+1 {
		t.Fatalf("после = %+v, до = %+v", comparison.After, comparison.Before)
	}
}

// reasonTotal и sliceByKey — утилиты тестов.
func reasonTotal(counts []ReasonCount, category ReasonCategory) int {
	for _, count := range counts {
		if count.Category == category {
			return count.Count
		}
	}
	return 0
}

func sliceByKey(slices []InsightSlice, key string) *InsightSlice {
	for i := range slices {
		if slices[i].Key == key {
			return &slices[i]
		}
	}
	return nil
}
