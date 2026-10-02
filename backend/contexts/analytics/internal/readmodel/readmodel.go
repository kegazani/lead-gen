// Package readmodel содержит эксплуатационную read-модель дашборда клиента:
// потокобезопасную CQRS-проекцию счётчиков по тенанту.
//
// Проекция не владеет доменным состоянием: она не вводит агрегатов, value
// objects, domain events и policies, а лишь накапливает счётчики из
// нормализованных доменных событий (`lead-workflow`, `engagement-observed`,
// `message-sent`, `delivery-event`). Состояние живёт в памяти процесса и теряется
// при перезапуске — это витрина, а не источник истины.
//
// Маскирование (задача 4.1): текст ответов компаний не хранится и не отдаётся.
// Событие ответа учитывается только счётчиком «ответили». Порог минимальной
// группы (задача 2.2): срез, сводящийся к одной записи, не отдаётся. Метрик
// открытий здесь нет по построению (pixel-free).
package readmodel

import (
	"sort"
	"strings"
	"sync"
	"time"
)

// Kind — вид нормализованного доменного события, питающего проекцию.
type Kind string

// Виды событий-источников read-модели (proposal.md, design.md).
const (
	KindLeadWorkflow       Kind = "lead-workflow"
	KindEngagementObserved Kind = "engagement-observed"
	KindMessageSent        Kind = "message-sent"
	KindDeliveryEvent      Kind = "delivery-event"
)

// Event — нормализованное доменное событие, применяемое к проекции.
//
// ReplyText намеренно присутствует во входе, но никогда не сохраняется и не
// отдаётся: событие ответа отражается только счётчиком. Поле существует, чтобы
// маскирование было проверяемо тестом, а не декларацией.
type Event struct {
	TenantID   string
	Kind       Kind
	CompanyID  string
	Status     string
	Outcome    string
	OccurredAt time.Time
	ReplyText  string
}

// Stage — стадия статусной воронки лида (`lead-workflow`).
type Stage string

// Стадии воронки в нормализованном виде.
const (
	StageNotWarmed Stage = "not_warmed"
	StageEmailSent Stage = "email_sent"
	StageWarming   Stage = "warming"
	StageCold      Stage = "cold"
)

// MinGroup — порог минимальной группы: срез, сводящийся к одной записи, не
// раскрывает личность и потому не отдаётся.
const MinGroup = 2

// Scope — фильтр среза дашборда.
type Scope struct {
	// CompanyID — сузить срез до одной компании. Пусто — весь тенант.
	CompanyID string
}

// GrowthPoint — точка ряда роста по дням.
type GrowthPoint struct {
	Date  string
	Found int
}

// Snapshot — агрегированный срез счётчиков. Visible == false означает, что срез
// не показан (порог минимальной группы): счётчики обнулены, а не отданы как 1.
type Snapshot struct {
	TenantID string
	Scope    Scope
	Visible  bool

	// Метрики прогресса.
	Found        int
	Communicated int
	Replied      int
	Warmed       int
	Growth       []GrowthPoint

	// Операционные метрики.
	InProgress    int
	Waiting       int
	Cold          int
	NotResponding int
	Queue         int
	// WaitingAges — возрасты ожидания компаний (для среднего и максимума).
	WaitingAges []time.Duration
}

// company — состояние одной компании в проекции. Хранит только стадию и
// отметки времени: содержимое ответов сюда не попадает.
type company struct {
	stage     Stage
	foundAt   time.Time
	sentAt    time.Time
	repliedAt time.Time
	hasSent   bool
	hasReply  bool
}

// ReadModel — потокобезопасная проекция «тенант → счётчики по компаниям».
type ReadModel struct {
	mu       sync.Mutex
	tenants  map[string]map[string]*company
	watchers map[string]map[chan struct{}]struct{}
}

// New создаёт пустую проекцию.
func New() *ReadModel {
	return &ReadModel{
		tenants:  make(map[string]map[string]*company),
		watchers: make(map[string]map[chan struct{}]struct{}),
	}
}

// Apply применяет нормализованное событие к проекции и уведомляет подписчиков
// тенанта. Событие без тенанта или компании игнорируется: его нельзя отнести к
// срезу, а выдумывать принадлежность проекция не вправе. ReplyText не сохраняется.
func (m *ReadModel) Apply(event Event) {
	if m == nil {
		return
	}
	tenant := strings.TrimSpace(event.TenantID)
	companyID := strings.TrimSpace(event.CompanyID)
	if tenant == "" || companyID == "" {
		return
	}

	m.mu.Lock()
	tenants := m.tenants[tenant]
	if tenants == nil {
		tenants = make(map[string]*company)
		m.tenants[tenant] = tenants
	}
	c := tenants[companyID]
	if c == nil {
		c = &company{}
		tenants[companyID] = c
	}
	if c.foundAt.IsZero() {
		c.foundAt = event.OccurredAt
	}

	switch event.Kind {
	case KindLeadWorkflow:
		if stage, ok := normalizeStage(event.Status); ok {
			c.stage = stage
		}
	case KindMessageSent:
		c.hasSent = true
		if c.sentAt.IsZero() {
			c.sentAt = event.OccurredAt
		}
		if c.stage == "" || c.stage == StageNotWarmed {
			c.stage = StageEmailSent
		}
	case KindEngagementObserved:
		if isReply(event.Outcome) {
			c.hasReply = true
			if c.repliedAt.IsZero() {
				c.repliedAt = event.OccurredAt
			}
		}
	case KindDeliveryEvent:
		if isBounce(event.Outcome) {
			c.stage = StageCold
		}
	default:
		// Неизвестный вид события проекцией не учитывается.
	}

	notify := make([]chan struct{}, 0, len(m.watchers[tenant]))
	for ch := range m.watchers[tenant] {
		notify = append(notify, ch)
	}
	m.mu.Unlock()

	// Уведомление вне блокировки: подписчик, медленно читающий канал, не держит
	// мьютекс проекции. Канал ёмкостью 1 коалесцирует пропущенные дельты — для
	// счётчиков это безопасно, подписчик всегда пересчитывает свежий срез.
	for _, ch := range notify {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watch подписывает наблюдателя на изменения тенанта и возвращает канал
// уведомлений и функцию отписки. Уведомление не несёт данных: подписчик
// пересчитывает срез сам, поэтому дельта всегда актуальна.
func (m *ReadModel) Watch(tenantID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	if m == nil {
		return ch, func() {}
	}
	tenant := strings.TrimSpace(tenantID)
	m.mu.Lock()
	if m.watchers[tenant] == nil {
		m.watchers[tenant] = make(map[chan struct{}]struct{})
	}
	m.watchers[tenant][ch] = struct{}{}
	m.mu.Unlock()

	cancel := func() {
		m.mu.Lock()
		if set := m.watchers[tenant]; set != nil {
			delete(set, ch)
			if len(set) == 0 {
				delete(m.watchers, tenant)
			}
		}
		m.mu.Unlock()
	}
	return ch, cancel
}

// Snapshot собирает агрегированный срез тенанта на момент now. Если срез
// сводится к одной компании (порог минимальной группы), он не отдаётся:
// возвращается Visible == false с обнулёнными счётчиками.
func (m *ReadModel) Snapshot(tenantID string, scope Scope, now time.Time) Snapshot {
	snapshot := Snapshot{TenantID: strings.TrimSpace(tenantID), Scope: scope}
	if m == nil {
		return snapshot
	}
	companyFilter := strings.TrimSpace(scope.CompanyID)

	m.mu.Lock()
	records := make([]*company, 0, len(m.tenants[tenantID]))
	for id, c := range m.tenants[snapshot.TenantID] {
		if companyFilter != "" && id != companyFilter {
			continue
		}
		records = append(records, c)
	}
	m.mu.Unlock()

	if len(records) < MinGroup {
		// Срез из одной записи не показывается, чтобы не раскрыть личность.
		return snapshot
	}
	snapshot.Visible = true

	growth := make(map[string]int)
	for _, c := range records {
		if c.hasReply {
			snapshot.Replied++
		}
		if c.hasSent {
			snapshot.Communicated++
		}
		if c.stage == StageWarming {
			snapshot.Warmed++
		}
		if c.stage == StageEmailSent || c.stage == StageWarming {
			snapshot.InProgress++
		}
		if c.stage == StageCold {
			snapshot.Cold++
		}
		if c.hasSent && !c.hasReply {
			snapshot.Waiting++
			if !c.sentAt.IsZero() {
				snapshot.WaitingAges = append(snapshot.WaitingAges, now.Sub(c.sentAt))
			}
		}
		if c.stage == StageCold && c.hasSent && !c.hasReply {
			snapshot.Queue++
		}
		if !c.foundAt.IsZero() {
			growth[c.foundAt.UTC().Format("2006-01-02")]++
		}
	}
	snapshot.Found = len(records)

	dates := make([]string, 0, len(growth))
	for date := range growth {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	for _, date := range dates {
		snapshot.Growth = append(snapshot.Growth, GrowthPoint{Date: date, Found: growth[date]})
	}
	return snapshot
}

// normalizeStage переводит внешнюю подпись стадии воронки в нормализованную.
// Принимает варианты с пробелами и дефисами: «not warmed», «not-warmed»,
// «email sent», «warming», «cold».
func normalizeStage(value string) (Stage, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch Stage(normalized) {
	case StageNotWarmed, StageEmailSent, StageWarming, StageCold:
		return Stage(normalized), true
	default:
		return "", false
	}
}

// isReply сообщает, что исход события — ответ компании. Текст ответа
// (Event.ReplyText) для этого не читается: ответ распознаётся по нормализованному
// исходу, а содержимое никогда не сохраняется.
func isReply(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "replied", "reply":
		return true
	default:
		return false
	}
}

// isBounce сообщает, что исход события доставки — отказ доставки.
func isBounce(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "bounced", "bounce", "undelivered":
		return true
	default:
		return false
	}
}
