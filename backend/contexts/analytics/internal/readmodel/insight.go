package readmodel

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Insight-модель качества поиска: агрегаты исходов классификации компаний
// (вердикты и причины «не подходит») в разрезах задание/источник/ниша/период.
//
// Модель не хранит персональных данных и содержимого красной зоны: наружу
// отдаются только счётчики и доли, а идентификатор компании используется
// исключительно как ключ свёртки и никогда не выходит за пределы модели.
// Различаются три несмешиваемые категории причины: «сигнал не найден»,
// «потребность опровергнута» и «ошибка извлечения» — рост ошибок извлечения не
// выдаётся за рост «не подходит» по существу.
//
// Снимок модели воспроизводим по watermark и версии формул
// (InsightFormulaVersion): из снимка восстанавливается то же состояние и тот же
// агрегированный срез.

// Verdict — вердикт классификации компании относительно задания поиска.
type Verdict string

// Согласованный набор вердиктов (совпадает с lead-assessment контекста
// lead-management).
const (
	// VerdictFits — компания подходит запросу.
	VerdictFits Verdict = "fits"
	// VerdictHypothesis — совпадение по профилю без сигнала потребности.
	VerdictHypothesis Verdict = "hypothesis"
	// VerdictUnsuitable — установлено несоответствие запросу.
	VerdictUnsuitable Verdict = "unsuitable"
)

// ReasonCategory — категория причины, по которой компания не прошла отбор.
//
// Ошибка извлечения (ReasonExtractionError) — самостоятельная категория: она не
// смешивается ни с «сигнал не найден», ни с «потребность опровергнута».
type ReasonCategory string

// Согласованный набор категорий причины.
const (
	// ReasonNoSignal — сигнал не найден (совпадение по профилю без потребности).
	ReasonNoSignal ReasonCategory = "no_signal"
	// ReasonRefuted — потребность опровергнута (установлено несоответствие).
	ReasonRefuted ReasonCategory = "refuted"
	// ReasonExtractionError — данные компании не удалось извлечь.
	ReasonExtractionError ReasonCategory = "extraction_error"
)

// Label возвращает подпись категории причины.
func (r ReasonCategory) Label() string {
	switch r {
	case ReasonNoSignal:
		return "сигнал не найден"
	case ReasonRefuted:
		return "потребность опровергнута"
	case ReasonExtractionError:
		return "ошибка извлечения"
	default:
		return "не указана"
	}
}

// InsightFormulaVersion — версия формул инсайта. Снимок воспроизводим по
// watermark и этой версии: смена формул меняет версию.
const InsightFormulaVersion = "quality-insight/1"

// InsightMinGroup — порог минимальной группы среза инсайта. Срез ниже порога
// (сводящийся к одной записи) не показывается.
const InsightMinGroup = MinGroup

// RecommendationMinObservations — минимальное число наблюдений в срезе, при
// котором выводится рекомендация. Меньше — рекомендация не формируется.
const RecommendationMinObservations = 5

// recommendationShareThreshold — доля категории (в процентах), начиная с
// которой причина в срезе считается устойчивой.
const recommendationShareThreshold = 50

// InsightOutcome — один исход классификации компании, применённый к инсайту и
// сохраняемый в снимке. CompanyID — непрозрачная ссылка: она служит только
// ключом свёртки и не выходит за пределы модели.
type InsightOutcome struct {
	TenantID   string
	JobID      string
	CompanyID  string
	Source     string
	Industry   string
	Verdict    Verdict
	Reason     ReasonCategory
	OccurredAt time.Time
}

// insightCompany — состояние одной компании в проекции: последний известный
// вердикт и причина в контексте задания.
type insightCompany struct {
	companyID  string
	jobID      string
	source     string
	industry   string
	verdict    Verdict
	reason     ReasonCategory
	occurredAt time.Time
}

// insightKey — ключ свёртки: задание и компания (оценка живёт в контексте задания).
type insightKey struct {
	job     string
	company string
}

// InsightModel — потокобезопасная проекция качества поиска «тенант → исходы».
type InsightModel struct {
	mu         sync.Mutex
	tenants    map[string]map[insightKey]insightCompany
	watermarks map[string]time.Time
}

// NewInsightModel создаёт пустую проекцию качества поиска.
func NewInsightModel() *InsightModel {
	return &InsightModel{
		tenants:    make(map[string]map[insightKey]insightCompany),
		watermarks: make(map[string]time.Time),
	}
}

// Apply применяет исход к проекции. Исход без тенанта или компании игнорируется:
// его нельзя отнести к срезу, а выдумывать принадлежность модель не вправе.
// Повторная оценка той же компании в том же задании заменяет прежнюю, не двоя
// счётчики.
func (m *InsightModel) Apply(outcome InsightOutcome) {
	if m == nil {
		return
	}
	tenant := strings.TrimSpace(outcome.TenantID)
	companyID := strings.TrimSpace(outcome.CompanyID)
	if tenant == "" || companyID == "" {
		return
	}
	record := insightCompany{
		companyID:  companyID,
		jobID:      strings.TrimSpace(outcome.JobID),
		source:     strings.TrimSpace(outcome.Source),
		industry:   strings.TrimSpace(outcome.Industry),
		verdict:    outcome.Verdict,
		reason:     effectiveReason(outcome),
		occurredAt: outcome.OccurredAt,
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	tenantModel := m.tenants[tenant]
	if tenantModel == nil {
		tenantModel = make(map[insightKey]insightCompany)
		m.tenants[tenant] = tenantModel
	}
	tenantModel[insightKey{job: record.jobID, company: companyID}] = record
	if outcome.OccurredAt.After(m.watermarks[tenant]) {
		m.watermarks[tenant] = outcome.OccurredAt
	}
}

// Tenants возвращает идентификаторы тенантов проекции инсайтов в возрастающем
// порядке: сводные агрегаты экрана операций строятся по всем клиентам, а не по
// одному тенанту запроса. Метод раскрывает только идентификаторы.
func (m *InsightModel) Tenants() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.tenants))
	for id := range m.tenants {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// effectiveReason нормализует причину исхода: явно заданная категория имеет
// приоритет, иначе она выводится из вердикта. Ошибка извлечения никогда не
// выводится из отрицательного вердикта — только из явного указания.
func effectiveReason(outcome InsightOutcome) ReasonCategory {
	switch outcome.Reason {
	case ReasonNoSignal, ReasonRefuted, ReasonExtractionError:
		return outcome.Reason
	}
	switch outcome.Verdict {
	case VerdictHypothesis:
		return ReasonNoSignal
	case VerdictUnsuitable:
		return ReasonRefuted
	default:
		return ""
	}
}

// Watermark возвращает максимальную отметку применённых исходов тенанта.
func (m *InsightModel) Watermark(tenantID string) time.Time {
	if m == nil {
		return time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.watermarks[strings.TrimSpace(tenantID)]
}

// VerdictCounts — распределение вердиктов. Ошибки извлечения считаются отдельно
// от вердиктов: они не выдаются за отрицательное решение компании.
type VerdictCounts struct {
	Fits             int
	Hypothesis       int
	Unsuitable       int
	ExtractionErrors int
}

// Total возвращает общее число исходов в распределении.
func (c VerdictCounts) Total() int {
	return c.Fits + c.Hypothesis + c.Unsuitable + c.ExtractionErrors
}

// ReasonCount — число исходов по категории причины.
type ReasonCount struct {
	Category ReasonCategory
	Label    string
	Count    int
}

// InsightSlice — агрегат одного разреза (значение измерения) с распределением
// вердиктов и причин.
type InsightSlice struct {
	Key      string
	Label    string
	Total    int
	Verdicts VerdictCounts
	Reasons  []ReasonCount
}

// Recommendation — рекомендация оператору с наблюдаемым основанием.
type Recommendation struct {
	// ScopeKind — измерение среза: job, source или industry.
	ScopeKind string
	ScopeKey  string
	// ScopeLabel — подпись среза.
	ScopeLabel string
	// Reason — устойчивая категория причины.
	Reason ReasonCategory
	// ReasonLabel — подпись категории.
	ReasonLabel string
	// Action — предлагаемое изменение в контуре поиска.
	Action string
	// Basis — наблюдаемое основание: число и доля наблюдений.
	Basis string
	// Observations — число наблюдений в срезе.
	Observations int
	// Share — доля категории в срезе.
	Share string
}

// InsightData — агрегированный срез качества поиска. Visible == false означает,
// что срез скрыт (порог минимальной группы либо нет наблюдений): метрики не
// отдаются и нулём не подменяются.
type InsightData struct {
	TenantID string
	Visible  bool
	// HasData — есть ли хоть один исход в срезе. Ложь вместе с Visible == false
	// означает отсутствие данных, а не скрытие.
	HasData      bool
	MinGroup     int
	HiddenReason string

	Watermark      time.Time
	FormulaVersion string

	Total    int
	Verdicts VerdictCounts
	Reasons  []ReasonCount

	ByJob      []InsightSlice
	BySource   []InsightSlice
	ByIndustry []InsightSlice
	ByPeriod   []InsightSlice

	Recommendations []Recommendation
	// RecommendationReason — почему рекомендаций нет (недостаток данных либо
	// отсутствие устойчивых отклонений). Пусто, когда рекомендации есть.
	RecommendationReason string
}

// InsightFilter — фильтр среза анализа: сужение по заданию, источнику, нише и
// периоду. Пустое значение поля не сужает.
type InsightFilter struct {
	JobID    string
	Source   string
	Industry string
	From     time.Time
	To       time.Time
}

// Сообщаемые причины скрытия среза: модель сообщает о скрытии, а не отдаёт
// неполный срез.
const (
	// InsightNoDataReason — в срезе нет наблюдений.
	InsightNoDataReason = "нет наблюдений в выбранном срезе"
	// InsightHiddenReason — срез ниже порога минимальной группы.
	InsightHiddenReason = "недостаточный размер группы: срез меньше порога минимальной группы"
)

// Insight собирает агрегированный срез качества поиска по фильтру.
func (m *InsightModel) Insight(tenantID string, filter InsightFilter) InsightData {
	tenant := strings.TrimSpace(tenantID)
	data := InsightData{TenantID: tenant, MinGroup: InsightMinGroup, FormulaVersion: InsightFormulaVersion}
	if m == nil {
		data.HiddenReason = InsightNoDataReason
		return data
	}

	m.mu.Lock()
	records := make([]insightCompany, 0, len(m.tenants[tenant]))
	for _, record := range m.tenants[tenant] {
		if !matchesInsightFilter(record, filter) {
			continue
		}
		records = append(records, record)
	}
	watermark := m.watermarks[tenant]
	m.mu.Unlock()

	data.Watermark = watermark
	data.Total = len(records)
	data.HasData = len(records) > 0
	if len(records) < InsightMinGroup {
		if len(records) == 0 {
			data.HiddenReason = InsightNoDataReason
		} else {
			data.HiddenReason = InsightHiddenReason
		}
		return data
	}
	data.Visible = true

	data.Verdicts = verdictCounts(records)
	data.Reasons = reasonCounts(records)

	data.ByJob = groupSlices(records, func(c insightCompany) (string, string) {
		return c.jobID, jobLabel(c.jobID)
	})
	data.BySource = groupSlices(records, func(c insightCompany) (string, string) {
		return c.source, dimensionLabel(c.source)
	})
	data.ByIndustry = groupSlices(records, func(c insightCompany) (string, string) {
		return c.industry, dimensionLabel(c.industry)
	})
	data.ByPeriod = periodSlices(records)

	data.Recommendations, data.RecommendationReason = recommendations(data.ByIndustry, "industry", "ниша")
	if extra, _ := recommendations(data.BySource, "source", "источник"); len(extra) > 0 {
		data.Recommendations = append(data.Recommendations, extra...)
	}
	if extra, _ := recommendations(data.ByJob, "job", "задание"); len(extra) > 0 {
		data.Recommendations = append(data.Recommendations, extra...)
	}
	sort.Slice(data.Recommendations, func(i, j int) bool {
		if data.Recommendations[i].ScopeKind != data.Recommendations[j].ScopeKind {
			return data.Recommendations[i].ScopeKind < data.Recommendations[j].ScopeKind
		}
		if data.Recommendations[i].ScopeKey != data.Recommendations[j].ScopeKey {
			return data.Recommendations[i].ScopeKey < data.Recommendations[j].ScopeKey
		}
		return data.Recommendations[i].Reason < data.Recommendations[j].Reason
	})
	if len(data.Recommendations) > 0 {
		data.RecommendationReason = ""
	} else if data.Total < RecommendationMinObservations {
		data.RecommendationReason = "недостаточно данных для рекомендации: наблюдений меньше порога"
	} else {
		data.RecommendationReason = "устойчивых отклонений не выявлено: доля причин ниже порога"
	}
	return data
}

// matchesInsightFilter проверяет период и измерения записи.
func matchesInsightFilter(c insightCompany, filter InsightFilter) bool {
	if !filter.From.IsZero() && (c.occurredAt.IsZero() || c.occurredAt.Before(filter.From)) {
		return false
	}
	if !filter.To.IsZero() && !c.occurredAt.IsZero() && c.occurredAt.After(filter.To) {
		return false
	}
	if value := strings.TrimSpace(filter.JobID); value != "" && c.jobID != value {
		return false
	}
	if value := strings.TrimSpace(filter.Source); value != "" && !strings.EqualFold(c.source, value) {
		return false
	}
	if value := strings.TrimSpace(filter.Industry); value != "" && !strings.EqualFold(c.industry, value) {
		return false
	}
	return true
}

// verdictCounts считает распределение вердиктов. Компания с ошибкой извлечения
// учитывается только в отдельной категории ошибок и не приписывается вердикту.
func verdictCounts(records []insightCompany) VerdictCounts {
	var counts VerdictCounts
	for _, c := range records {
		if c.reason == ReasonExtractionError {
			counts.ExtractionErrors++
			continue
		}
		switch c.verdict {
		case VerdictFits:
			counts.Fits++
		case VerdictHypothesis:
			counts.Hypothesis++
		case VerdictUnsuitable:
			counts.Unsuitable++
		}
	}
	return counts
}

// reasonCounts считает распределение причин. Категории не смешиваются: ошибка
// извлечения идёт отдельной строкой.
func reasonCounts(records []insightCompany) []ReasonCount {
	totals := map[ReasonCategory]int{}
	for _, c := range records {
		if c.reason == "" {
			continue
		}
		totals[c.reason]++
	}
	return sortedReasonCounts(totals)
}

// sortedReasonCounts переводит карту причин в устойчиво упорядоченный срез.
func sortedReasonCounts(totals map[ReasonCategory]int) []ReasonCount {
	order := []ReasonCategory{ReasonNoSignal, ReasonRefuted, ReasonExtractionError}
	counts := make([]ReasonCount, 0, len(totals))
	for _, category := range order {
		if count := totals[category]; count > 0 {
			counts = append(counts, ReasonCount{Category: category, Label: category.Label(), Count: count})
		}
	}
	return counts
}

// groupSlices группирует записи по измерению. Группы ниже порога минимальной
// группы не отдаются.
func groupSlices(records []insightCompany, keyLabel func(insightCompany) (string, string)) []InsightSlice {
	type group struct {
		label   string
		records []insightCompany
	}
	groups := make(map[string]*group)
	for _, c := range records {
		key, label := keyLabel(c)
		entry := groups[key]
		if entry == nil {
			entry = &group{label: label}
			groups[key] = entry
		}
		entry.records = append(entry.records, c)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	slices := make([]InsightSlice, 0, len(keys))
	for _, key := range keys {
		entry := groups[key]
		if len(entry.records) < InsightMinGroup {
			continue
		}
		slices = append(slices, InsightSlice{
			Key:      key,
			Label:    entry.label,
			Total:    len(entry.records),
			Verdicts: verdictCounts(entry.records),
			Reasons:  reasonCounts(entry.records),
		})
	}
	return slices
}

// periodSlices группирует записи по дню. Дни ниже порога минимальной группы не
// отдаются.
func periodSlices(records []insightCompany) []InsightSlice {
	period := make(map[string][]insightCompany)
	for _, c := range records {
		if c.occurredAt.IsZero() {
			continue
		}
		date := c.occurredAt.UTC().Format("2006-01-02")
		period[date] = append(period[date], c)
	}
	dates := make([]string, 0, len(period))
	for date, group := range period {
		if len(group) < InsightMinGroup {
			continue
		}
		dates = append(dates, date)
	}
	sort.Strings(dates)

	slices := make([]InsightSlice, 0, len(dates))
	for _, date := range dates {
		group := period[date]
		slices = append(slices, InsightSlice{
			Key:      date,
			Label:    date,
			Total:    len(group),
			Verdicts: verdictCounts(group),
			Reasons:  reasonCounts(group),
		})
	}
	return slices
}

// recommendations формирует рекомендации по устойчивым причинам разреза. Срез
// ниже порога наблюдений рекомендации не даёт.
func recommendations(slices []InsightSlice, scopeKind, scopeName string) ([]Recommendation, string) {
	recommendations := make([]Recommendation, 0)
	for _, slice := range slices {
		if slice.Total < RecommendationMinObservations {
			continue
		}
		for _, reason := range slice.Reasons {
			if reason.Count*100/slice.Total < recommendationShareThreshold {
				continue
			}
			recommendations = append(recommendations, Recommendation{
				ScopeKind:    scopeKind,
				ScopeKey:     slice.Key,
				ScopeLabel:   scopeName + " " + slice.Label,
				Reason:       reason.Category,
				ReasonLabel:  reason.Label,
				Action:       recommendationAction(reason.Category),
				Basis:        "наблюдений " + strconv.Itoa(slice.Total) + ", доля " + share(reason.Count, slice.Total) + " (" + reason.Label + ")",
				Observations: slice.Total,
				Share:        share(reason.Count, slice.Total),
			})
		}
	}
	return recommendations, ""
}

// recommendationAction переводит категорию причины в предлагаемое изменение
// контура поиска.
func recommendationAction(category ReasonCategory) string {
	switch category {
	case ReasonNoSignal:
		return "добавить цели обхода или подключить источник"
	case ReasonRefuted:
		return "уточнить критерий задания или ослабить ограничение"
	case ReasonExtractionError:
		return "проверить и подключить источник извлечения"
	default:
		return "уточнить параметры поиска"
	}
}

// jobLabel даёт подпись задания: пустое — «без задания».
func jobLabel(jobID string) string {
	if strings.TrimSpace(jobID) == "" {
		return "без задания"
	}
	return jobID
}

// share форматирует долю в процентах с одним знаком после запятой.
func share(part, total int) string {
	if total <= 0 {
		return "0.0%"
	}
	return strconv.FormatFloat(float64(part)/float64(total)*100, 'f', 1, 64) + "%"
}

// InsightSummary — сводка среза инсайта для сравнения до/после применения
// рекомендации.
type InsightSummary struct {
	Total    int
	Verdicts VerdictCounts
	Reasons  []ReasonCount
}

// Summarize выделяет сводку из агрегированного среза.
func Summarize(data InsightData) InsightSummary {
	return InsightSummary{Total: data.Total, Verdicts: data.Verdicts, Reasons: data.Reasons}
}

// RecommendationApplication — зафиксированный факт применения рекомендации
// оператором с состоянием анализа на момент применения.
type RecommendationApplication struct {
	TenantID  string
	ScopeKind string
	ScopeKey  string
	Action    string
	Reason    ReasonCategory
	Basis     string
	AppliedAt time.Time
	// Reference — ссылка на созданное в контуре поиска изменение.
	Reference string
	// BeforeWatermark — watermark снимка «до» применения.
	BeforeWatermark time.Time
	Before          InsightSummary
}

// ApplicationComparison — сравнение показателей до и после применения
// рекомендации с учётом новых исходов.
type ApplicationComparison struct {
	Application    RecommendationApplication
	AfterWatermark time.Time
	Before         InsightSummary
	After          InsightSummary
	// NewOutcomes — прирост числа исходов после применения.
	NewOutcomes int
	// Changed — появились ли новые исходы после применения.
	Changed bool
}

// CompareApplication сравнивает сохранённый снимок «до» с текущим срезом.
func CompareApplication(application RecommendationApplication, current InsightData) ApplicationComparison {
	after := Summarize(current)
	return ApplicationComparison{
		Application:    application,
		AfterWatermark: current.Watermark,
		Before:         application.Before,
		After:          after,
		NewOutcomes:    after.Total - application.Before.Total,
		Changed:        current.Watermark.After(application.BeforeWatermark),
	}
}

// InsightSnapshot — воспроизводимый снимок проекции качества поиска: исходы
// тенанта, watermark и версия формул. Восстановление снимка даёт то же состояние
// восстановление снимка даёт то же состояние
// и тот же агрегированный срез по этому watermark.
type InsightSnapshot struct {
	TenantID       string
	Watermark      time.Time
	FormulaVersion string
	Outcomes       []InsightOutcome
}

// Snapshot возвращает снимок проекции тенанта в устойчивом порядке. Снимок
// содержит непрозрачные ссылки на компании как ключи свёртки и не выносит
// персональных данных: наружу снимок отдаётся только хранилищу.
func (m *InsightModel) Snapshot(tenantID string) InsightSnapshot {
	tenant := strings.TrimSpace(tenantID)
	snap := InsightSnapshot{TenantID: tenant, FormulaVersion: InsightFormulaVersion}
	if m == nil {
		return snap
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snap.Watermark = m.watermarks[tenant]
	records := m.tenants[tenant]
	snap.Outcomes = make([]InsightOutcome, 0, len(records))
	for _, record := range records {
		snap.Outcomes = append(snap.Outcomes, InsightOutcome{
			TenantID:   tenant,
			JobID:      record.jobID,
			CompanyID:  record.companyID,
			Source:     record.source,
			Industry:   record.industry,
			Verdict:    record.verdict,
			Reason:     record.reason,
			OccurredAt: record.occurredAt,
		})
	}
	sort.Slice(snap.Outcomes, func(i, j int) bool {
		if snap.Outcomes[i].JobID != snap.Outcomes[j].JobID {
			return snap.Outcomes[i].JobID < snap.Outcomes[j].JobID
		}
		return snap.Outcomes[i].CompanyID < snap.Outcomes[j].CompanyID
	})
	return snap
}

// RestoreSnapshot восстанавливает проекцию из снимка. Повторное восстановление
// свёрнуто по ключу (задание, компания) и потому идемпотентно.
func (m *InsightModel) RestoreSnapshot(snap InsightSnapshot) {
	if m == nil {
		return
	}
	for _, outcome := range snap.Outcomes {
		m.Apply(outcome)
	}
}
