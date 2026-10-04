package readmodel_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// seedReport заполняет проекцию сценарием из двух кампаний и двух менеджеров:
// каждая группа проходит порог минимальной группы.
func seedReport(t *testing.T) *readmodel.ReadModel {
	t.Helper()
	model := readmodel.New()
	events := []readmodel.Event{
		// Кампания camp-1, источник web, отрасль IT, менеджер m1.
		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c1", Status: "warming", Source: "web", Industry: "IT", Segment: "smb", CampaignID: "camp-1", ManagerID: "m1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c1", CampaignID: "camp-1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindDeliveryEvent, CompanyID: "c1", Outcome: "delivered", CampaignID: "camp-1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", Outcome: "clicked", CampaignID: "camp-1", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c1", Outcome: "replied", CampaignID: "camp-1", OccurredAt: day(2)},
		{TenantID: "t1", Kind: readmodel.KindLeadOutcome, CompanyID: "c1", Outcome: "won", CampaignID: "camp-1", OccurredAt: day(3)},
		{TenantID: "t1", Kind: readmodel.KindEmailVerification, CompanyID: "c1", Outcome: "valid", CampaignID: "camp-1", OccurredAt: day(0)},

		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c2", Status: "warming", Source: "web", Industry: "IT", Segment: "smb", CampaignID: "camp-1", ManagerID: "m1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c2", CampaignID: "camp-1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindDeliveryEvent, CompanyID: "c2", Outcome: "delivered", CampaignID: "camp-1", OccurredAt: day(0)},
		{TenantID: "t1", Kind: readmodel.KindDeliveryEvent, CompanyID: "c2", Outcome: "bounced", CampaignID: "camp-1", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindEmailVerification, CompanyID: "c2", Outcome: "invalid", CampaignID: "camp-1", OccurredAt: day(0)},

		// Кампания camp-2, источник referral, отрасль finance, менеджер m2.
		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c3", Status: "warming", Source: "referral", Industry: "finance", Segment: "ent", CampaignID: "camp-2", ManagerID: "m2", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindMessageSent, CompanyID: "c3", CampaignID: "camp-2", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindEngagementObserved, CompanyID: "c3", Outcome: "unsubscribed", CampaignID: "camp-2", OccurredAt: day(2)},

		{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c4", Status: "duplicate", Source: "referral", Industry: "finance", Segment: "ent", CampaignID: "camp-2", ManagerID: "m2", OccurredAt: day(1)},
		{TenantID: "t1", Kind: readmodel.KindEmailVerification, CompanyID: "c4", Outcome: "valid", CampaignID: "camp-2", OccurredAt: day(1)},
	}
	for _, event := range events {
		model.Apply(event)
	}
	return model
}

// TestReportCampaignsAndDimensions проверяет метрики кампаний, распределения по
// источникам, отраслям и периодам за период.
func TestReportCampaignsAndDimensions(t *testing.T) {
	report := seedReport(t).Report("t1", readmodel.ReportFilter{})
	if !report.Visible {
		t.Fatalf("отчёт по четырём компаниям должен быть видим: %+v", report)
	}
	if report.Records != 4 {
		t.Fatalf("Records = %d, ожидалось 4", report.Records)
	}
	if len(report.Campaigns) != 2 {
		t.Fatalf("кампаний %d, ожидалось 2: %+v", len(report.Campaigns), report.Campaigns)
	}
	first := report.Campaigns[0]
	if first.CampaignID != "camp-1" {
		t.Fatalf("первая кампания = %q, ожидалась camp-1", first.CampaignID)
	}
	if first.Sent != 2 || first.Delivered != 2 || first.Transitions != 1 || first.Replies != 1 || first.Unsubscribes != 0 || first.Bounces != 1 {
		t.Fatalf("метрики camp-1 = %+v", first)
	}
	second := report.Campaigns[1]
	if second.CampaignID != "camp-2" || second.Sent != 1 || second.Unsubscribes != 1 || second.Bounces != 0 {
		t.Fatalf("метрики camp-2 = %+v", second)
	}

	if len(report.Sources) != 2 {
		t.Fatalf("источников %d, ожидалось 2: %+v", len(report.Sources), report.Sources)
	}
	for _, source := range report.Sources {
		if source.Count != 2 {
			t.Fatalf("распределение по источникам = %+v, ожидалось по 2", report.Sources)
		}
	}
	if len(report.Industries) != 2 || len(report.Periods) != 2 {
		t.Fatalf("отрасли/периоды = %+v / %+v", report.Industries, report.Periods)
	}
	if report.Periods[0].Date != day(0).Format("2006-01-02") || report.Periods[0].Count != 2 {
		t.Fatalf("первый период = %+v", report.Periods[0])
	}
}

// TestReportFunnelConversion проверяет воронку и конверсию на известных числах.
func TestReportFunnelConversion(t *testing.T) {
	report := seedReport(t).Report("t1", readmodel.ReportFilter{})
	if len(report.Funnel) != 4 {
		t.Fatalf("этапов воронки %d, ожидалось 4: %+v", len(report.Funnel), report.Funnel)
	}
	want := []struct {
		key   string
		count int
		conv  string
	}{
		{"lead", 4, ""},
		{"communication", 3, "75.0%"},
		{"reply", 1, "33.3%"},
		{"deal", 1, "100.0%"},
	}
	for index, stage := range report.Funnel {
		if stage.Key != want[index].key || stage.Count != want[index].count || stage.Conversion != want[index].conv {
			t.Fatalf("этап %d = %+v, ожидалось %+v", index, stage, want[index])
		}
	}
}

// TestReportQualityShares проверяет доли валидных, отказов и дублей.
func TestReportQualityShares(t *testing.T) {
	report := seedReport(t).Report("t1", readmodel.ReportFilter{})
	quality := report.Quality
	if quality.Total != 4 || quality.Valid != 2 || quality.Bounces != 1 || quality.Duplicates != 1 {
		t.Fatalf("качество базы = %+v", quality)
	}
	if quality.ValidShare != "50.0%" || quality.BounceShare != "25.0%" || quality.DuplicateShare != "25.0%" {
		t.Fatalf("доли качества = %+v", quality)
	}
}

// TestReportManagersSeparated проверяет управленческую отчётность отдельно:
// активность и конверсия по менеджерам, а отчётность по лидам менеджеров не
// содержит.
func TestReportManagersSeparated(t *testing.T) {
	report := seedReport(t).Report("t1", readmodel.ReportFilter{})
	if len(report.Managers) != 2 {
		t.Fatalf("менеджеров %d, ожидалось 2: %+v", len(report.Managers), report.Managers)
	}
	if report.Managers[0].ManagerID != "m1" || report.Managers[0].Volume != 2 || report.Managers[0].Deals != 1 || report.Managers[0].Conversion != "50.0%" {
		t.Fatalf("метрики менеджера m1 = %+v", report.Managers[0])
	}
	// Сведения о менеджерах не проникают в лидовые разделы.
	assertNoFieldLike(t, readmodel.DimensionCount{}, "manager")
	assertNoFieldLike(t, readmodel.PeriodCount{}, "manager")
	assertNoFieldLike(t, readmodel.QualityReport{}, "manager")
	assertNoFieldLike(t, readmodel.FunnelStage{}, "manager")
}

// TestReportFilterCombination проверяет комбинацию фильтров по кампании, источнику
// и периоду.
func TestReportFilterCombination(t *testing.T) {
	model := seedReport(t)

	byCampaign := model.Report("t1", readmodel.ReportFilter{CampaignID: "camp-2"})
	if !byCampaign.Visible || byCampaign.Records != 2 {
		t.Fatalf("срез по camp-2 = %+v", byCampaign)
	}
	if len(byCampaign.Campaigns) != 1 || byCampaign.Campaigns[0].CampaignID != "camp-2" {
		t.Fatalf("кампании среза = %+v", byCampaign.Campaigns)
	}

	bySource := model.Report("t1", readmodel.ReportFilter{Source: "web"})
	if !bySource.Visible || bySource.Records != 2 || len(bySource.Sources) != 1 {
		t.Fatalf("срез по источнику web = %+v", bySource)
	}

	from := day(1)
	to := day(1).Add(14 * time.Hour)
	byPeriod := model.Report("t1", readmodel.ReportFilter{From: from, To: to})
	if !byPeriod.Visible || byPeriod.Records != 2 {
		t.Fatalf("срез по периоду = %+v", byPeriod)
	}
}

// TestReportFilterBySegment проверяет фильтр по сегменту: срез сужается до
// записей сегмента, чужой сегмент в него не проникает, а неизвестный сегмент не
// отдаётся (ниже порога минимальной группы).
func TestReportFilterBySegment(t *testing.T) {
	model := seedReport(t)

	bySegment := model.Report("t1", readmodel.ReportFilter{Segment: "smb"})
	if !bySegment.Visible || bySegment.Records != 2 {
		t.Fatalf("срез по сегменту smb = %+v", bySegment)
	}
	for _, source := range bySegment.Sources {
		if source.Key != "web" {
			t.Fatalf("срез сегмента smb содержит чужой источник %q: %+v", source.Key, bySegment.Sources)
		}
	}

	missing := model.Report("t1", readmodel.ReportFilter{Segment: "unknown"})
	if missing.Visible {
		t.Fatalf("неизвестный сегмент не должен отдавать записи: %+v", missing)
	}
}

// TestReportMinGroupHidesSlices проверяет порог минимальной группы: узкий срез не
// отдаётся и не показывает счётчики.
func TestReportMinGroupHidesSlices(t *testing.T) {
	model := seedReport(t)
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindLeadWorkflow, CompanyID: "c5", Status: "warming", Source: "solo", Industry: "IT", Segment: "smb", CampaignID: "camp-3", ManagerID: "m3", OccurredAt: day(0)})

	// Узкий срез кампании из одной записи скрыт.
	scoped := model.Report("t1", readmodel.ReportFilter{CampaignID: "camp-3"})
	if scoped.Visible {
		t.Fatalf("срез из одной записи не должен показываться: %+v", scoped)
	}
	if scoped.Records != 0 || len(scoped.Campaigns) != 0 {
		t.Fatalf("скрытый срез не должен отдавать метрики: %+v", scoped)
	}
	if scoped.HiddenReason == "" {
		t.Fatalf("скрытый срез должен сообщать причину")
	}

	// В полном срезе группа camp-3 ниже порога не показывается, а видимые — да.
	full := model.Report("t1", readmodel.ReportFilter{})
	if !full.Visible {
		t.Fatalf("полный срез должен быть видим: %+v", full)
	}
	for _, campaign := range full.Campaigns {
		if campaign.CampaignID == "camp-3" {
			t.Fatalf("группа ниже порога не должна попадать в срез: %+v", full.Campaigns)
		}
	}
}

// TestReportUnapprovedDrafts проверяет объём неутверждённых черновиков как
// показатель простоя.
func TestReportUnapprovedDrafts(t *testing.T) {
	model := seedReport(t)
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindOutreachOutcome, CompanyID: "c1", Outcome: "drafted", CampaignID: "camp-1", OccurredAt: day(1)})
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindOutreachOutcome, CompanyID: "c1", Outcome: "drafted", CampaignID: "camp-1", OccurredAt: day(1)})
	model.Apply(readmodel.Event{TenantID: "t1", Kind: readmodel.KindOutreachOutcome, CompanyID: "c1", Outcome: "approved", CampaignID: "camp-1", OccurredAt: day(1)})

	report := model.Report("t1", readmodel.ReportFilter{CampaignID: "camp-1"})
	if report.Unapproved != 1 {
		t.Fatalf("неутверждённых черновиков всего = %d, ожидалось 1", report.Unapproved)
	}
	if len(report.Campaigns) != 1 || report.Campaigns[0].Unapproved != 1 {
		t.Fatalf("неутверждённые черновики кампании = %+v", report.Campaigns)
	}
}

// TestReportNoOpenMetrics проверяет, что метрик открытий нет ни в одном типе
// отчёта: сигнал не собирается.
func TestReportNoOpenMetrics(t *testing.T) {
	assertNoFieldLike(t, readmodel.ReportData{}, "open")
	assertNoFieldLike(t, readmodel.CampaignMetric{}, "open")
	assertNoFieldLike(t, readmodel.QualityReport{}, "open")
	assertNoFieldLike(t, readmodel.ManagerMetric{}, "open")
	assertNoFieldLike(t, readmodel.FunnelStage{}, "open")
}

// assertNoFieldLike проверяет, что у типа нет поля, в имени которого встречается
// подстрока (регистр не важен).
func assertNoFieldLike(t *testing.T, value any, substr string) {
	t.Helper()
	typ := reflect.TypeOf(value)
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if strings.Contains(strings.ToLower(field.Name), substr) {
			t.Fatalf("тип %s содержит поле %q", typ.Name(), field.Name)
		}
	}
}
