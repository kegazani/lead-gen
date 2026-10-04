package readmodel

import (
	"sort"
	"strconv"
	"strings"
	"time"
)

// Отчётная read-модель: агрегаты по кампаниям, лидогенерации, воронке и
// качеству базы плюс управленческая отчётность. Считается по той же проекции,
// что и дашборд, из нормализованных событий контура клиента. Записи пула наружу
// не отдаются: Report возвращает только счётчики и доли.
//
// Метрик открытий здесь нет по построению (pixel-free): сигнал не собирается.

// ReportFilter — фильтр среза отчёта: период и измерения.
type ReportFilter struct {
	// From — нижняя граница периода по дате обнаружения лида (включительно).
	// Нулевое значение — без нижней границы.
	From time.Time
	// To — верхняя граница периода (включительно). Нулевое значение — без
	// верхней границы.
	To time.Time
	// Segment, Source — измерения среза. Пусто — не сужать.
	Segment string
	Source  string
	// CampaignID — сужение до одной кампании. Пусто — все кампании.
	CampaignID string
}

// CampaignMetric — метрики одной кампании. Показателя открытий нет.
type CampaignMetric struct {
	CampaignID   string
	Records      int
	Sent         int
	Delivered    int
	Transitions  int
	Replies      int
	Unsubscribes int
	Bounces      int
	// Unapproved — объём неутверждённых черновиков кампании (простой).
	Unapproved int
}

// DimensionCount — распределение по измерению (источник, отрасль).
type DimensionCount struct {
	Key   string
	Label string
	Count int
}

// PeriodCount — распределение по дню обнаружения лида.
type PeriodCount struct {
	Date  string
	Count int
}

// FunnelStage — этап воронки и конверсия с предыдущего этапа.
type FunnelStage struct {
	Key        string
	Label      string
	Count      int
	Conversion string
	// Share — ширина полосы этапа в процентах от первого этапа.
	Share int
}

// QualityReport — отчёт по качеству базы: три доли.
type QualityReport struct {
	Total          int
	Valid          int
	Bounces        int
	Duplicates     int
	ValidShare     string
	BounceShare    string
	DuplicateShare string
}

// ManagerMetric — активность и конверсия менеджера. Управленческая отчётность
// отделена от отчётности по лидам: это сведения о работе сотрудников клиента.
type ManagerMetric struct {
	ManagerID string
	Label     string
	// Activity — число обработанных контактов (размер группы).
	Activity int
	// Volume — объём коммуникаций менеджера.
	Volume int
	// Replies — ответы по контактам менеджера.
	Replies int
	// Deals — сделки по контактам менеджера.
	Deals int
	// Conversion — доля сделок от объёма коммуникаций.
	Conversion string
}

// ReportData — агрегированный срез отчёта. Visible == false означает, что срез
// скрыт порогом минимальной группы: метрики не отдаются и нулём не подменяются.
type ReportData struct {
	TenantID     string
	Visible      bool
	MinGroup     int
	Records      int
	HiddenReason string

	Campaigns  []CampaignMetric
	Sources    []DimensionCount
	Industries []DimensionCount
	Periods    []PeriodCount
	Funnel     []FunnelStage
	Quality    QualityReport
	Managers   []ManagerMetric

	// Unapproved — суммарный объём неутверждённых черновиков (простой цепочек).
	Unapproved int
}

// hiddenReason — сообщаемая причина скрытия среза: система сообщает о
// недостаточном размере группы, а не отдаёт срез из одной записи.
const HiddenReason = "недостаточный размер группы: срез меньше порога минимальной группы"

// Report собирает агрегированный срез отчёта по фильтру. Срез, в котором число
// записей ниже порога минимальной группы, не отдаётся: возвращается Visible ==
// false с причиной и обнулёнными метриками.
func (m *ReadModel) Report(tenantID string, filter ReportFilter) ReportData {
	report := ReportData{TenantID: strings.TrimSpace(tenantID), MinGroup: MinGroup}
	if m == nil {
		report.HiddenReason = HiddenReason
		return report
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	records := make([]*company, 0, len(m.tenants[report.TenantID]))
	for _, c := range m.tenants[report.TenantID] {
		if !matchesReportFilter(c, filter) {
			continue
		}
		records = append(records, c)
	}

	if len(records) < MinGroup {
		report.HiddenReason = HiddenReason
		return report
	}
	report.Visible = true
	report.Records = len(records)

	report.Campaigns = campaignMetrics(records)
	report.Sources = dimensionCounts(records, func(c *company) string { return c.source })
	report.Industries = dimensionCounts(records, func(c *company) string { return c.industry })
	report.Periods = periodCounts(records)
	report.Funnel = funnelStages(records)
	report.Quality = qualityReport(records)
	report.Managers = managerMetrics(records)

	for _, c := range records {
		report.Unapproved += c.pendingDrafts
	}
	return report
}

// matchesReportFilter проверяет период и измерения записи.
func matchesReportFilter(c *company, filter ReportFilter) bool {
	if c == nil {
		return false
	}
	if !filter.From.IsZero() && (c.foundAt.IsZero() || c.foundAt.Before(filter.From)) {
		return false
	}
	if !filter.To.IsZero() && !c.foundAt.IsZero() && c.foundAt.After(filter.To) {
		return false
	}
	if value := strings.TrimSpace(filter.Segment); value != "" && !strings.EqualFold(c.segment, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Source); value != "" && !strings.EqualFold(c.source, value) {
		return false
	}
	if value := strings.TrimSpace(filter.CampaignID); value != "" && c.campaignID != value {
		return false
	}
	return true
}

// campaignMetrics группирует записи по кампании. Группы ниже порога минимальной
// группы не отдаются.
func campaignMetrics(records []*company) []CampaignMetric {
	groups := make(map[string]*CampaignMetric)
	for _, c := range records {
		key := c.campaignID
		group := groups[key]
		if group == nil {
			group = &CampaignMetric{CampaignID: key}
			groups[key] = group
		}
		group.Records++
		group.Unapproved += c.pendingDrafts
		if c.hasSent {
			group.Sent++
		}
		if c.hasDelivered {
			group.Delivered++
		}
		if c.hasTransition {
			group.Transitions++
		}
		if c.hasReply {
			group.Replies++
		}
		if c.hasUnsubscribed {
			group.Unsubscribes++
		}
		if c.hasBounce {
			group.Bounces++
		}
	}

	metrics := make([]CampaignMetric, 0, len(groups))
	for _, group := range groups {
		if group.Records < MinGroup {
			continue
		}
		metrics = append(metrics, *group)
	}
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].CampaignID < metrics[j].CampaignID })
	return metrics
}

// dimensionCounts группирует записи по измерению. Пустое измерение образует
// группу «не указан», а не теряется: неизвестное измерение — факт среза.
func dimensionCounts(records []*company, value func(*company) string) []DimensionCount {
	groups := make(map[string]int)
	for _, c := range records {
		groups[strings.TrimSpace(value(c))]++
	}
	counts := make([]DimensionCount, 0, len(groups))
	for key, count := range groups {
		if count < MinGroup {
			continue
		}
		counts = append(counts, DimensionCount{Key: key, Label: dimensionLabel(key), Count: count})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Count != counts[j].Count {
			return counts[i].Count > counts[j].Count
		}
		return counts[i].Key < counts[j].Key
	})
	return counts
}

// periodCounts группирует записи по дню обнаружения лида. Дни ниже порога
// минимальной группы не отдаются.
func periodCounts(records []*company) []PeriodCount {
	groups := make(map[string]int)
	for _, c := range records {
		if c.foundAt.IsZero() {
			continue
		}
		groups[c.foundAt.UTC().Format("2006-01-02")]++
	}
	dates := make([]string, 0, len(groups))
	for date, count := range groups {
		if count < MinGroup {
			continue
		}
		dates = append(dates, date)
	}
	sort.Strings(dates)
	periods := make([]PeriodCount, 0, len(dates))
	for _, date := range dates {
		periods = append(periods, PeriodCount{Date: date, Count: groups[date]})
	}
	return periods
}

// funnelStages считает воронку «лид → коммуникация → ответ → сделка» и конверсию
// на каждом переходе.
func funnelStages(records []*company) []FunnelStage {
	var lead, communication, reply, deal int
	for _, c := range records {
		lead++
		if c.hasSent {
			communication++
		}
		if c.hasReply {
			reply++
		}
		if c.hasWon {
			deal++
		}
	}
	stages := []struct {
		key   string
		label string
		count int
	}{
		{"lead", "Лид", lead},
		{"communication", "Коммуникация", communication},
		{"reply", "Ответ", reply},
		{"deal", "Сделка", deal},
	}
	result := make([]FunnelStage, 0, len(stages))
	previous := 0
	for index, stage := range stages {
		row := FunnelStage{Key: stage.key, Label: stage.label, Count: stage.count}
		if index > 0 {
			row.Conversion = ratio(row.Count, previous)
		}
		if lead > 0 {
			row.Share = row.Count * 100 / lead
		}
		result = append(result, row)
		previous = row.Count
	}
	return result
}

// qualityReport считает доли валидных, отказов и дублей от общего числа записей.
func qualityReport(records []*company) QualityReport {
	report := QualityReport{Total: len(records)}
	for _, c := range records {
		if c.validity == validityValid {
			report.Valid++
		}
		if c.hasBounce {
			report.Bounces++
		}
		if c.duplicate {
			report.Duplicates++
		}
	}
	report.ValidShare = ratio(report.Valid, report.Total)
	report.BounceShare = ratio(report.Bounces, report.Total)
	report.DuplicateShare = ratio(report.Duplicates, report.Total)
	return report
}

// managerMetrics группирует записи по менеджеру. Группы ниже порога минимальной
// группы не отдаются: узкий срез о работе сотрудника не показывается.
func managerMetrics(records []*company) []ManagerMetric {
	groups := make(map[string]*ManagerMetric)
	for _, c := range records {
		key := c.managerID
		group := groups[key]
		if group == nil {
			group = &ManagerMetric{ManagerID: key}
			groups[key] = group
		}
		group.Activity++
		if c.hasSent {
			group.Volume++
		}
		if c.hasReply {
			group.Replies++
		}
		if c.hasWon {
			group.Deals++
		}
	}
	metrics := make([]ManagerMetric, 0, len(groups))
	for _, group := range groups {
		if group.Activity < MinGroup {
			continue
		}
		group.Label = managerLabel(group.ManagerID)
		group.Conversion = ratio(group.Deals, group.Volume)
		metrics = append(metrics, *group)
	}
	sort.Slice(metrics, func(i, j int) bool {
		if metrics[i].Volume != metrics[j].Volume {
			return metrics[i].Volume > metrics[j].Volume
		}
		return metrics[i].ManagerID < metrics[j].ManagerID
	})
	return metrics
}

// dimensionLabel даёт подпись измерения: пустое — «не указан».
func dimensionLabel(key string) string {
	if key == "" {
		return "не указан"
	}
	return key
}

// managerLabel даёт подпись менеджера.
func managerLabel(id string) string {
	if id == "" {
		return "не указан"
	}
	return id
}

// ratio форматирует долю в процентах с одним знаком после запятой.
func ratio(part, total int) string {
	if total <= 0 {
		return "0.0%"
	}
	return strconv.FormatFloat(float64(part)/float64(total)*100, 'f', 1, 64) + "%"
}
