package query_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/app/query"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// fakeClock — управляемые часы: время обновления детерминировано.
type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// toggleSource — источник агрегатов, который можно переключить в отказ. Позволяет
// проверить честную деградацию без внешнего контура.
type toggleSource struct {
	mu   sync.Mutex
	data readmodel.ReportData
	err  error
}

func (s *toggleSource) Report(string, readmodel.ReportFilter) (readmodel.ReportData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data, s.err
}

func (s *toggleSource) fail(err error) {
	s.mu.Lock()
	s.err = err
	s.mu.Unlock()
}

// TestReportHandlerDegradesToLastKnown проверяет честную деградацию: при
// недоступности контура клиента возвращаются последние рассчитанные метрики с
// явным временем, а не нули.
func TestReportHandlerDegradesToLastKnown(t *testing.T) {
	now := time.Date(2026, 1, 10, 9, 0, 0, 0, time.UTC)
	source := &toggleSource{data: readmodel.ReportData{
		TenantID: "t1",
		Visible:  true,
		Records:  4,
		Campaigns: []readmodel.CampaignMetric{
			{CampaignID: "camp-1", Records: 2, Sent: 2, Replies: 1},
		},
	}}
	handler := query.NewReportHandler(source, fakeClock{now: now})
	reportQuery := query.ReportQuery{TenantID: "t1"}

	live := handler.Handle(context.Background(), reportQuery)
	if !live.Available || !live.Visible || live.UpdatedAtLabel != "2026-01-10 09:00:00 UTC" {
		t.Fatalf("живой срез = %+v", live)
	}

	source.fail(query.ErrReportSourceUnavailable)
	degraded := handler.Handle(context.Background(), reportQuery)
	if !degraded.Available {
		t.Fatalf("последние метрики остаются доступны при деградации: %+v", degraded)
	}
	if !degraded.Stale || degraded.StaleReason == "" {
		t.Fatalf("деградация должна быть помечена с причиной: %+v", degraded)
	}
	if degraded.UpdatedAtLabel != live.UpdatedAtLabel {
		t.Fatalf("время последних метрик изменилось: %q → %q", live.UpdatedAtLabel, degraded.UpdatedAtLabel)
	}
	if len(degraded.Data.Campaigns) != 1 || degraded.Data.Campaigns[0].CampaignID != "camp-1" {
		t.Fatalf("последние рассчитанные метрики не сохранены: %+v", degraded.Data)
	}
}

// TestReportHandlerUnavailableWithoutCache проверяет честный отказ без кэша:
// отсутствующие значения не подменяются нулём.
func TestReportHandlerUnavailableWithoutCache(t *testing.T) {
	source := &toggleSource{err: query.ErrReportSourceUnavailable}
	handler := query.NewReportHandler(source, fakeClock{now: time.Now()})

	view := handler.Handle(context.Background(), query.ReportQuery{TenantID: "t1"})
	if view.Available || !view.Stale {
		t.Fatalf("без кэша отчёт должен быть недоступен и помечен: %+v", view)
	}
	if view.Data.Records != 0 || len(view.Data.Campaigns) != 0 {
		t.Fatalf("отсутствующие метрики не должны подменяться нулём: %+v", view.Data)
	}
}

// TestReportHandlerSeparatesCachesByFilter проверяет, что кэш деградации
// различает срезы: фильтр влияет на последние метрики.
func TestReportHandlerSeparatesCachesByFilter(t *testing.T) {
	source := &toggleSource{data: readmodel.ReportData{TenantID: "t1", Visible: true, Records: 4}}
	handler := query.NewReportHandler(source, fakeClock{now: time.Now()})

	handler.Handle(context.Background(), query.ReportQuery{TenantID: "t1"})
	source.fail(query.ErrReportSourceUnavailable)

	filtered := handler.Handle(context.Background(), query.ReportQuery{TenantID: "t1", CampaignID: "camp-9"})
	if filtered.Available {
		t.Fatalf("для несчитанного среза кэша нет: %+v", filtered)
	}
}

// TestExportRowsContainsReportSections проверяет, что выгрузка содержит разделы
// отчёта: кампании, лидогенерацию, воронку, качество и менеджеров, причём без
// управленческих сведений в лидовых разделах.
func TestExportRowsContainsReportSections(t *testing.T) {
	view := query.ReportView{
		Available:      true,
		Visible:        true,
		PeriodLabel:    "весь период",
		UpdatedAtLabel: "2026-01-10 09:00:00 UTC",
		Data: readmodel.ReportData{
			Campaigns:  []readmodel.CampaignMetric{{CampaignID: "camp-1", Records: 2, Sent: 2, Delivered: 2, Transitions: 1, Replies: 1, Unsubscribes: 0, Bounces: 1}},
			Sources:    []readmodel.DimensionCount{{Key: "web", Label: "web", Count: 2}},
			Industries: []readmodel.DimensionCount{{Key: "IT", Label: "IT", Count: 2}},
			Periods:    []readmodel.PeriodCount{{Date: "2026-01-01", Count: 2}},
			Funnel: []readmodel.FunnelStage{
				{Key: "lead", Label: "Лид", Count: 4},
				{Key: "communication", Label: "Коммуникация", Count: 3, Conversion: "75.0%"},
			},
			Quality:  readmodel.QualityReport{Total: 4, ValidShare: "50.0%", BounceShare: "25.0%", DuplicateShare: "25.0%"},
			Managers: []readmodel.ManagerMetric{{ManagerID: "m1", Label: "m1", Activity: 2, Volume: 2, Deals: 1, Conversion: "50.0%"}},
		},
	}

	rows := query.ExportRows(view)
	flat := flatten(rows)
	for _, want := range []string{"Кампании", "camp-1", "Лидогенерация по источникам", "Воронка", "Качество базы", "Менеджеры", "m1"} {
		if !strings.Contains(flat, want) {
			t.Fatalf("выгрузка не содержит %q:\n%s", want, flat)
		}
	}
	if strings.Contains(flat, "open") || strings.Contains(flat, "открыт") {
		t.Fatalf("выгрузка не должна содержать метрик открытий:\n%s", flat)
	}
}

// TestExportRowsHiddenSlice проверяет, что срез ниже порога минимальной группы в
// выгрузку не попадает: вместо метрик записывается причина.
func TestExportRowsHiddenSlice(t *testing.T) {
	rows := query.ExportRows(query.ReportView{
		Available: true,
		Visible:   false,
		Data:      readmodel.ReportData{HiddenReason: readmodel.HiddenReason},
	})
	flat := flatten(rows)
	if !strings.Contains(flat, "не показан") || !strings.Contains(flat, readmodel.HiddenReason) {
		t.Fatalf("выгрузка скрытого среза не сообщает причину:\n%s", flat)
	}
	if strings.Contains(flat, "Кампании") {
		t.Fatalf("скрытый срез не должен попадать в выгрузку:\n%s", flat)
	}
}

// TestExportRowsUnavailable проверяет, что при недоступности контура выгрузка
// содержит честный отказ, а не пустую таблицу с нулями.
func TestExportRowsUnavailable(t *testing.T) {
	rows := query.ExportRows(query.ReportView{Available: false, StaleReason: "контур недоступен"})
	flat := flatten(rows)
	if !strings.Contains(flat, "недоступен") || !strings.Contains(flat, "контур недоступен") {
		t.Fatalf("выгрузка недоступного отчёта = %s", flat)
	}
}

// flatten склеивает строки выгрузки в одну строку для проверок вхождения.
func flatten(rows [][]string) string {
	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, strings.Join(row, ","))
	}
	return strings.Join(lines, "\n")
}
