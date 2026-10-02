package query

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"lead-gen/backend/contexts/analytics/internal/ports"
	"lead-gen/backend/contexts/analytics/internal/readmodel"
)

// DashboardQuery — запрос среза дашборда клиента.
type DashboardQuery struct {
	TenantID string
	// Role — роль клиента среза; влияет на подписи, но доступ решает порт.
	Role string
	// CompanyID — необязательное сужение среза до одной компании.
	CompanyID string
}

// DashboardView — модель дашборда: два слоя метрик.
type DashboardView struct {
	TenantID string
	Role     string
	Visible  bool
	MinGroup int
	// ThresholdDays — порог «не отвечают дольше N дней».
	ThresholdDays int

	Progress   ProgressView
	Operations OperationsView
}

// ProgressView — ведущий слой: метрики прогресса.
type ProgressView struct {
	Found        int
	Communicated int
	Replied      int
	RepliedShare string
	Warmed       int
	Funnel       []FunnelRow
	Chart        ChartView
}

// FunnelRow — строка воронки: подпись, значение и ширина полосы в процентах.
type FunnelRow struct {
	Key   string
	Label string
	Count int
	Width int
}

// ChartView — area-график роста по дням (координаты для SVG).
type ChartView struct {
	Width  int
	Height int
	Line   string
	Area   string
	Points []readmodel.GrowthPoint
	Empty  bool
}

// OperationsView — операционный слой: метрики обработки и ожидания.
type OperationsView struct {
	InProgress    int
	Waiting       int
	WaitingMaxAge string
	AverageWait   string
	Cold          int
	NotResponding int
	Queue         int
}

// DashboardHandler — use case сборки среза дашборда по read-модели.
type DashboardHandler struct {
	model     *readmodel.ReadModel
	clock     ports.Clock
	staleDays int
}

// NewDashboardHandler собирает use case из проекции, часов и порога «не отвечают
// N+ дней».
func NewDashboardHandler(model *readmodel.ReadModel, clock ports.Clock, staleDays int) *DashboardHandler {
	if staleDays <= 0 {
		staleDays = defaultStaleDays
	}
	return &DashboardHandler{model: model, clock: clock, staleDays: staleDays}
}

// defaultStaleDays — порог «не отвечают N+ дней» по умолчанию.
const defaultStaleDays = 7

// Handle собирает срез дашборда. Скрытый срез (порог минимальной группы) отдаётся
// пустым: счётчики обнулены, а Visible == false.
func (h *DashboardHandler) Handle(_ context.Context, q DashboardQuery) DashboardView {
	now := h.now()
	snapshot := h.model.Snapshot(q.TenantID, readmodel.Scope{CompanyID: q.CompanyID}, now)
	view := DashboardView{
		TenantID:      strings.TrimSpace(q.TenantID),
		Role:          q.Role,
		Visible:       snapshot.Visible,
		MinGroup:      readmodel.MinGroup,
		ThresholdDays: h.staleDays,
	}
	if !snapshot.Visible {
		return view
	}
	view.Progress = progressView(snapshot)
	view.Operations = operationsView(snapshot, h.staleDays)
	return view
}

// Watch подписывает дашборд на изменения тенанта: потоковый слой уведомляется о
// новых счётчиках без ручного обновления страницы.
func (h *DashboardHandler) Watch(tenantID string) (<-chan struct{}, func()) {
	return h.model.Watch(tenantID)
}

// ThresholdDays возвращает порог «не отвечают N+ дней» для отображения.
func (h *DashboardHandler) ThresholdDays() int { return h.staleDays }

func (h *DashboardHandler) now() time.Time {
	if h.clock == nil {
		return time.Now()
	}
	return h.clock.Now()
}

// progressView собирает ведущий слой.
func progressView(snapshot readmodel.Snapshot) ProgressView {
	return ProgressView{
		Found:        snapshot.Found,
		Communicated: snapshot.Communicated,
		Replied:      snapshot.Replied,
		RepliedShare: replyShare(snapshot.Replied, snapshot.Communicated),
		Warmed:       snapshot.Warmed,
		Funnel:       funnelRows(snapshot),
		Chart:        buildChart(snapshot.Growth),
	}
}

// operationsView собирает операционный слой: счётчики, возраст и среднее время
// ожидания.
func operationsView(snapshot readmodel.Snapshot, staleDays int) OperationsView {
	notResponding := 0
	var total, maxAge time.Duration
	for _, age := range snapshot.WaitingAges {
		total += age
		if age > maxAge {
			maxAge = age
		}
		if age >= time.Duration(staleDays)*24*time.Hour {
			notResponding++
		}
	}
	average := time.Duration(0)
	if len(snapshot.WaitingAges) > 0 {
		average = total / time.Duration(len(snapshot.WaitingAges))
	}
	return OperationsView{
		InProgress:    snapshot.InProgress,
		Waiting:       snapshot.Waiting,
		WaitingMaxAge: formatAge(maxAge),
		AverageWait:   formatAge(average),
		Cold:          snapshot.Cold,
		NotResponding: notResponding,
		Queue:         snapshot.Queue,
	}
}

// replyShare форматирует долю ответивших от прокоммуницированных.
func replyShare(replied, communicated int) string {
	if communicated <= 0 {
		return "0.0%"
	}
	return strconv.FormatFloat(float64(replied)/float64(communicated)*100, 'f', 1, 64) + "%"
}

// formatAge форматирует возраст ожидания в днях.
func formatAge(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	return strconv.FormatFloat(age.Hours()/24, 'f', 1, 64) + " дн"
}

// funnelRows собирает воронку «найдено → отправлено → ответили».
func funnelRows(snapshot readmodel.Snapshot) []FunnelRow {
	max := snapshot.Found
	if max <= 0 {
		max = 1
	}
	width := func(count int) int {
		if count <= 0 {
			return 0
		}
		percent := count * 100 / max
		if percent < 2 {
			return 2
		}
		return percent
	}
	return []FunnelRow{
		{Key: "found", Label: "Найдено", Count: snapshot.Found, Width: width(snapshot.Found)},
		{Key: "sent", Label: "Отправлено", Count: snapshot.Communicated, Width: width(snapshot.Communicated)},
		{Key: "replied", Label: "Ответили", Count: snapshot.Replied, Width: width(snapshot.Replied)},
	}
}

// chartWidth и chartHeight — размеры полотна area-графика (SVG viewBox).
const (
	chartWidth  = 320
	chartHeight = 120
	chartPad    = 12
)

// buildChart строит area-график роста по дням: координаты линии и замкнутой
// области. Точки роста — реальные счётчики, ничего не выдумывается.
func buildChart(growth []readmodel.GrowthPoint) ChartView {
	chart := ChartView{Width: chartWidth, Height: chartHeight, Points: growth}
	if len(growth) == 0 {
		chart.Empty = true
		return chart
	}
	max := 1
	for _, point := range growth {
		if point.Found > max {
			max = point.Found
		}
	}
	innerW := chartWidth - 2*chartPad
	innerH := chartHeight - 2*chartPad

	x := func(index int) int {
		if len(growth) == 1 {
			return chartPad + innerW/2
		}
		return chartPad + innerW*index/(len(growth)-1)
	}
	y := func(count int) int {
		return chartHeight - chartPad - innerH*count/max
	}

	coords := make([]string, 0, len(growth))
	for index, point := range growth {
		coords = append(coords, fmt.Sprintf("%d,%d", x(index), y(point.Found)))
	}
	chart.Line = strings.Join(coords, " ")

	area := make([]string, 0, len(coords)+2)
	area = append(area, coords...)
	area = append(area, fmt.Sprintf("%d,%d", x(len(growth)-1), chartHeight-chartPad))
	area = append(area, fmt.Sprintf("%d,%d", x(0), chartHeight-chartPad))
	chart.Area = strings.Join(area, " ")
	return chart
}
