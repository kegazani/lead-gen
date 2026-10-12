package query

import (
	"context"
	"sort"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
	"lead-gen/contracts/analytics/campaignreport"
)

// Операционные агрегаты процесса (published contract campaign-report): цифры
// процесса с разбивкой по клиенту и времени и агрегаты по клиентам для
// полноэкранного дашборда. Источник — read-модели самого владельца
// (campaign-reporting и lead-quality-insights): чужие хранилища не читаются.
//
// Приватность: наружу отдаются только сводные счётчики, персональных данных и
// содержимого красной зоны здесь нет. Срез ниже порога минимальной группы
// (клиент или день) не показывается — он отсутствует в ответе, а не отдаётся
// нулём. Недоступность источника — честная ошибка (campaignreport.ErrUnavailable),
// а не пустой успех.

// operationsMinObservations — минимальное число наблюдений, при котором
// выводится аномалия: на малом числе основание вывода нет.
const operationsMinObservations = 5

// operationsRejectionShareThreshold — доля отклонённых (в процентах), с которой
// всплеск отклонений считается аномалией. Согласована с порогом устойчивой
// причины в read-модели инсайтов.
const operationsRejectionShareThreshold = 50

// OperationsHandler собирает операционные агрегаты процесса из read-моделей
// владельца. Пустые модели означают, что контур не собран, — это честная
// недоступность, а не пустой срез.
type OperationsHandler struct {
	readModel *readmodel.ReadModel
	insights  *readmodel.InsightModel
	clock     ports.Clock
	minGroup  int
}

// NewOperationsHandler собирает use case агрегатов процесса. minGroup <= 0
// означает порог минимальной группы по умолчанию.
func NewOperationsHandler(model *readmodel.ReadModel, insights *readmodel.InsightModel, clock ports.Clock, minGroup int) *OperationsHandler {
	if minGroup < 1 {
		minGroup = readmodel.MinGroup
	}
	return &OperationsHandler{readModel: model, insights: insights, clock: clock, minGroup: minGroup}
}

// operationsSample — сводка одного клиента и вспомогательные значения для аномалий.
type operationsSample struct {
	numbers campaignreport.ClientNumbers
	// queue — накопившаяся очередь без ответа (для аномалии «процесс замер»).
	queue int
}

// Numbers реализует campaignreport.Port: цифры процесса по клиентам и разбивка
// по времени. Окно запроса ограничивает разбивку по времени; счётчики
// «найдено»/«в работе»/«результат» — текущие сводные значения источника.
func (h *OperationsHandler) Numbers(_ context.Context, request campaignreport.Request) (campaignreport.NumbersResponse, error) {
	if err := request.Validate(); err != nil {
		return campaignreport.NumbersResponse{}, err
	}
	if h == nil || (h.readModel == nil && h.insights == nil) {
		return campaignreport.NumbersResponse{}, campaignreport.ErrUnavailable
	}
	samples, periods := h.collect(h.now(), request.Window)
	clients := make([]campaignreport.ClientNumbers, 0, len(samples))
	for _, sample := range samples {
		clients = append(clients, sample.numbers)
	}
	return campaignreport.NumbersResponse{
		Contract:      campaignreport.ContractName,
		SchemaVersion: campaignreport.SchemaVersion,
		Clients:       clients,
		Periods:       periods,
		Count:         len(clients),
	}, nil
}

// Clients реализует campaignreport.Port: агрегаты по клиентам для
// полноэкранного дашборда с кодами выявленных аномалий.
func (h *OperationsHandler) Clients(_ context.Context, request campaignreport.Request) (campaignreport.ClientsResponse, error) {
	if err := request.Validate(); err != nil {
		return campaignreport.ClientsResponse{}, err
	}
	if h == nil || (h.readModel == nil && h.insights == nil) {
		return campaignreport.ClientsResponse{}, campaignreport.ErrUnavailable
	}
	samples, _ := h.collect(h.now(), request.Window)
	clients := make([]campaignreport.ClientAggregate, 0, len(samples))
	for _, sample := range samples {
		clients = append(clients, campaignreport.ClientAggregate{
			ClientID:   sample.numbers.ClientID,
			Found:      sample.numbers.Found,
			Processed:  sample.numbers.Processed,
			Rejected:   sample.numbers.Rejected,
			InProgress: sample.numbers.InProgress,
			Result:     sample.numbers.Result,
			Anomalies:  anomalies(sample),
		})
	}
	return campaignreport.ClientsResponse{
		Contract:      campaignreport.ContractName,
		SchemaVersion: campaignreport.SchemaVersion,
		Clients:       clients,
		Count:         len(clients),
	}, nil
}

// collect собирает срез по клиентам и разбивку по времени. Клиенты ниже порога
// минимальной группы в срез не попадают.
func (h *OperationsHandler) collect(now time.Time, window time.Duration) ([]operationsSample, []campaignreport.PeriodEntry) {
	tenants := h.tenants()
	samples := make([]operationsSample, 0, len(tenants))
	buckets := make(map[string]*periodBucket)
	for _, tenant := range tenants {
		snapshot := h.snapshot(tenant, now)
		insight := h.insight(tenant)
		numbers := campaignreport.ClientNumbers{
			ClientID:   tenant,
			Found:      snapshot.Found,
			Processed:  insight.Total,
			Rejected:   insight.Verdicts.Unsuitable,
			InProgress: snapshot.InProgress,
			Result:     snapshot.Replied,
		}
		if !h.visible(numbers) {
			continue
		}
		samples = append(samples, operationsSample{numbers: numbers, queue: snapshot.Queue})
		accumulatePeriods(buckets, snapshot, insight, windowStart(window, now))
	}
	return samples, periodEntries(buckets, h.minGroup)
}

// tenants возвращает объединение тенантов обеих read-моделей в возрастающем
// порядке: сводные агрегаты строятся по всем клиентам.
func (h *OperationsHandler) tenants() []string {
	set := make(map[string]struct{})
	if h.readModel != nil {
		for _, tenant := range h.readModel.Tenants() {
			set[tenant] = struct{}{}
		}
	}
	if h.insights != nil {
		for _, tenant := range h.insights.Tenants() {
			set[tenant] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for tenant := range set {
		out = append(out, tenant)
	}
	sort.Strings(out)
	return out
}

// snapshot возвращает срез кампаний тенанта или пустой срез, если модель не
// собрана. Минимальная группа проверяется самой моделью (Visible).
func (h *OperationsHandler) snapshot(tenant string, now time.Time) readmodel.Snapshot {
	if h.readModel == nil {
		return readmodel.Snapshot{TenantID: tenant}
	}
	return h.readModel.Snapshot(tenant, readmodel.Scope{}, now)
}

// insight возвращает срез исходов классификации тенанта или пустой срез, если
// модель не собрана. Минимальная группа проверяется самой моделью (Visible).
func (h *OperationsHandler) insight(tenant string) readmodel.InsightData {
	if h.insights == nil {
		return readmodel.InsightData{TenantID: tenant, MinGroup: readmodel.InsightMinGroup}
	}
	return h.insights.Insight(tenant, readmodel.InsightFilter{})
}

// visible сообщает, проходит ли клиент порог минимальной группы.
func (h *OperationsHandler) visible(numbers campaignreport.ClientNumbers) bool {
	total := numbers.Found + numbers.Processed + numbers.Rejected + numbers.InProgress + numbers.Result
	return total >= h.minGroup
}

// periodBucket — накопитель цифр одного дня.
type periodBucket struct {
	found     int
	processed int
	rejected  int
}

// accumulatePeriods добавляет в накопители роста по дням и исходы классификации
// за дни, попадающие в окно разбивки. Найденные даты берутся из ряда роста
// read-модели кампаний, обработанные/отклонённые — из дневных срезов инсайтов.
func accumulatePeriods(buckets map[string]*periodBucket, snapshot readmodel.Snapshot, insight readmodel.InsightData, from string) {
	for _, point := range snapshot.Growth {
		if !withinWindow(point.Date, from) {
			continue
		}
		bucket(buckets, point.Date).found += point.Found
	}
	for _, slice := range insight.ByPeriod {
		if !withinWindow(slice.Key, from) {
			continue
		}
		entry := bucket(buckets, slice.Key)
		entry.processed += slice.Total
		entry.rejected += slice.Verdicts.Unsuitable
	}
}

// bucket возвращает накопитель дня, создавая его при необходимости.
func bucket(buckets map[string]*periodBucket, date string) *periodBucket {
	entry := buckets[date]
	if entry == nil {
		entry = &periodBucket{}
		buckets[date] = entry
	}
	return entry
}

// periodEntries сортирует дни и отбрасывает срезы ниже порога минимальной
// группы: день с суммарным числом ниже порога не показывается.
func periodEntries(buckets map[string]*periodBucket, minGroup int) []campaignreport.PeriodEntry {
	dates := make([]string, 0, len(buckets))
	for date := range buckets {
		dates = append(dates, date)
	}
	sort.Strings(dates)

	out := make([]campaignreport.PeriodEntry, 0, len(dates))
	for _, date := range dates {
		entry := buckets[date]
		total := entry.found + entry.processed + entry.rejected
		if total < minGroup {
			continue
		}
		out = append(out, campaignreport.PeriodEntry{
			Date:      date,
			Found:     entry.found,
			Processed: entry.processed,
			Rejected:  entry.rejected,
		})
	}
	return out
}

// anomalies выводит машинные коды аномалий клиента: всплеск отклонений при
// достаточном числе наблюдений и накопившуюся очередь без ответа. Основание —
// наблюдаемые счётчики; при недостатке данных аномалия не выводится.
func anomalies(sample operationsSample) []string {
	out := make([]string, 0, 2)
	if sample.numbers.Processed >= operationsMinObservations &&
		sample.numbers.Rejected*100/sample.numbers.Processed >= operationsRejectionShareThreshold {
		out = append(out, campaignreport.AnomalyRejectionSpike)
	}
	if sample.queue >= operationsMinObservations {
		out = append(out, campaignreport.AnomalyStalled)
	}
	sort.Strings(out)
	return out
}

// windowStart возвращает нижнюю границу разбивки в формате YYYY-MM-DD. Пустая
// строка означает «без ограничения по времени».
func windowStart(window time.Duration, now time.Time) string {
	if window <= 0 {
		return ""
	}
	return now.Add(-window).UTC().Format("2006-01-02")
}

// withinWindow проверяет, попадает ли дата (YYYY-MM-DD) в окно разбивки.
func withinWindow(date, from string) bool {
	return from == "" || date >= from
}

// now возвращает момент расчёта.
func (h *OperationsHandler) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}
