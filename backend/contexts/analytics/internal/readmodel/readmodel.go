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

	// KindEmailVerification — результат проверки адреса (исход valid/invalid).
	// Питает отчёт по качеству базы.
	KindEmailVerification Kind = "email-verification"

	// KindOutreachOutcome — результат шага последовательности (drafted, approved,
	// rejected и т.д.). Питает показатель простоя — объём неутверждённых шагов.
	KindOutreachOutcome Kind = "outreach-outcome"

	// KindLeadOutcome — исход лида сверх переписки (сделка): исход won/lost.
	KindLeadOutcome Kind = "lead-outcome"

	// KindLeadAssessment — исход классификации компании относительно задания
	// (вердикт и причина). Питает проекцию качества поиска (lead-quality-insights),
	// а не дашборд клиента: отдельный вид, чтобы вердикты не искажали воронку.
	KindLeadAssessment Kind = "lead-assessment"
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

	// Измерения отчётности: источник, отрасль, сегмент, кампания и менеджер.
	// Необязательны; непустое значение уточняет срез, пустое — не затирает ранее
	// известное измерение. Это агрегатные измерения витрины, а не персональные
	// данные: значения принадлежат срезу, а не контакту.
	Source     string
	Industry   string
	Segment    string
	CampaignID string
	ManagerID  string
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

// validityState — состояние проверки контакта для отчёта по качеству базы.
type validityState string

const (
	validityUnknown validityState = ""
	validityValid   validityState = "valid"
	validityInvalid validityState = "invalid"
)

// company — состояние одной компании в проекции. Хранит стадию, измерения среза
// и отметки времени: содержимое ответов сюда не попадает.
type company struct {
	stage     Stage
	foundAt   time.Time
	sentAt    time.Time
	repliedAt time.Time
	hasSent   bool
	hasReply  bool

	// Измерения отчётности.
	source     string
	industry   string
	segment    string
	campaignID string
	managerID  string

	// Факты отчётности: сроки и признаки для дашбордов кампаний и качества базы.
	deliveredAt    time.Time
	transitionedAt time.Time
	unsubscribedAt time.Time
	bouncedAt      time.Time
	wonAt          time.Time

	hasDelivered    bool
	hasTransition   bool
	hasUnsubscribed bool
	hasBounce       bool
	hasWon          bool

	validity      validityState
	duplicate     bool
	pendingDrafts int
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

// Tenants возвращает идентификаторы тенантов проекции в возрастающем порядке.
// Список нужен владельцу, чтобы построить сводные агрегаты по всем клиентам
// (экран операций) без обхода чужих хранилищ. Персональных данных метод не
// раскрывает: это только идентификаторы.
func (m *ReadModel) Tenants() []string {
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
	applyDimensions(c, event)

	switch event.Kind {
	case KindLeadWorkflow:
		// Признак дубля приходит статусом лида и не является стадией воронки:
		// записывается отдельно, а не подменяет стадию.
		if isDuplicate(event.Status) {
			c.duplicate = true
		} else if stage, ok := normalizeStage(event.Status); ok {
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
	case KindDeliveryEvent:
		switch {
		case isBounce(event.Outcome):
			c.hasBounce = true
			if c.bouncedAt.IsZero() {
				c.bouncedAt = event.OccurredAt
			}
			c.stage = StageCold
		case isDelivered(event.Outcome):
			c.hasDelivered = true
			if c.deliveredAt.IsZero() {
				c.deliveredAt = event.OccurredAt
			}
		}
	case KindEngagementObserved:
		switch {
		case isReply(event.Outcome):
			c.hasReply = true
			if c.repliedAt.IsZero() {
				c.repliedAt = event.OccurredAt
			}
		case isUnsubscribe(event.Outcome):
			c.hasUnsubscribed = true
			if c.unsubscribedAt.IsZero() {
				c.unsubscribedAt = event.OccurredAt
			}
			// Отписка останавливает коммуникацию: последующие касания не идут.
			c.stage = StageCold
		case isTransition(event.Outcome):
			c.hasTransition = true
			if c.transitionedAt.IsZero() {
				c.transitionedAt = event.OccurredAt
			}
		}
	case KindEmailVerification:
		switch {
		case isInvalidEmail(event.Outcome):
			c.validity = validityInvalid
		case isDelivered(event.Outcome):
			// Исход valid нормализуется тем же словом, что и доставка.
			c.validity = validityValid
		}
	case KindOutreachOutcome:
		switch {
		case isDrafted(event.Outcome):
			c.pendingDrafts++
		case isDraftResolved(event.Outcome):
			if c.pendingDrafts > 0 {
				c.pendingDrafts--
			}
		case isDraftApproved(event.Outcome):
			// Утверждение снимает ровно один неутверждённый шаг.
			if c.pendingDrafts > 0 {
				c.pendingDrafts--
			}
		}
	case KindLeadOutcome:
		if isWon(event.Outcome) {
			c.hasWon = true
			if c.wonAt.IsZero() {
				c.wonAt = event.OccurredAt
			}
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

// CompanyRecord — снимок состояния одной компании в проекции для хранилища.
//
// Текст ответа сюда не входит: событие ответа отражается только признаком
// HasReply и отметкой времени, поэтому маскирование сохраняется и на носителе
// состояния.
type CompanyRecord struct {
	TenantID  string
	CompanyID string
	Stage     Stage

	FoundAt   time.Time
	SentAt    time.Time
	RepliedAt time.Time

	HasSent  bool
	HasReply bool

	// Измерения среза и факты отчётности, переживающие рестарт проекции.
	Source     string
	Industry   string
	Segment    string
	CampaignID string
	ManagerID  string

	DeliveredAt    time.Time
	TransitionedAt time.Time
	UnsubscribedAt time.Time
	BouncedAt      time.Time
	WonAt          time.Time

	HasDelivered    bool
	HasTransition   bool
	HasUnsubscribed bool
	HasBounce       bool
	HasWon          bool

	Validity      string
	Duplicate     bool
	PendingDrafts int
}

// Record возвращает снимок состояния компании для сохранения в хранилище. ok
// ложно, если такой компании в проекции нет: событие было отброшено
// (пустой тенант или компания) и сохранять нечего.
func (m *ReadModel) Record(tenantID, companyID string) (CompanyRecord, bool) {
	if m == nil {
		return CompanyRecord{}, false
	}
	tenant := strings.TrimSpace(tenantID)
	id := strings.TrimSpace(companyID)
	if tenant == "" || id == "" {
		return CompanyRecord{}, false
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	c := m.tenants[tenant][id]
	if c == nil {
		return CompanyRecord{}, false
	}
	return CompanyRecord{
		TenantID:  tenant,
		CompanyID: id,
		Stage:     c.stage,
		FoundAt:   c.foundAt,
		SentAt:    c.sentAt,
		RepliedAt: c.repliedAt,
		HasSent:   c.hasSent,
		HasReply:  c.hasReply,

		Source:     c.source,
		Industry:   c.industry,
		Segment:    c.segment,
		CampaignID: c.campaignID,
		ManagerID:  c.managerID,

		DeliveredAt:    c.deliveredAt,
		TransitionedAt: c.transitionedAt,
		UnsubscribedAt: c.unsubscribedAt,
		BouncedAt:      c.bouncedAt,
		WonAt:          c.wonAt,

		HasDelivered:    c.hasDelivered,
		HasTransition:   c.hasTransition,
		HasUnsubscribed: c.hasUnsubscribed,
		HasBounce:       c.hasBounce,
		HasWon:          c.hasWon,

		Validity:      string(c.validity),
		Duplicate:     c.duplicate,
		PendingDrafts: c.pendingDrafts,
	}, true
}

// Restore восстанавливает состояние компании из снимка хранилища при тёплом
// старте. Прежнее состояние той же компании заменяется: снимок хранилища —
// источник истины для проекции. Уведомления подписчиков не рассылаются: тёплый
// старт выполняется до появления наблюдателей.
func (m *ReadModel) Restore(record CompanyRecord) {
	if m == nil {
		return
	}
	tenant := strings.TrimSpace(record.TenantID)
	id := strings.TrimSpace(record.CompanyID)
	if tenant == "" || id == "" {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	companies := m.tenants[tenant]
	if companies == nil {
		companies = make(map[string]*company)
		m.tenants[tenant] = companies
	}
	companies[id] = &company{
		stage:     record.Stage,
		foundAt:   record.FoundAt,
		sentAt:    record.SentAt,
		repliedAt: record.RepliedAt,
		hasSent:   record.HasSent,
		hasReply:  record.HasReply,

		source:     record.Source,
		industry:   record.Industry,
		segment:    record.Segment,
		campaignID: record.CampaignID,
		managerID:  record.ManagerID,

		deliveredAt:    record.DeliveredAt,
		transitionedAt: record.TransitionedAt,
		unsubscribedAt: record.UnsubscribedAt,
		bouncedAt:      record.BouncedAt,
		wonAt:          record.WonAt,

		hasDelivered:    record.HasDelivered,
		hasTransition:   record.HasTransition,
		hasUnsubscribed: record.HasUnsubscribed,
		hasBounce:       record.HasBounce,
		hasWon:          record.HasWon,

		validity:      validityState(record.Validity),
		duplicate:     record.Duplicate,
		pendingDrafts: record.PendingDrafts,
	}
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
	case "bounced", "bounce", "undelivered", "failed":
		return true
	default:
		return false
	}
}

// applyDimensions уточняет измерения среза компании непустыми значениями
// события. Пустое значение не затирает ранее известное измерение: событие без
// измерения не отменяет уже установленное.
func applyDimensions(c *company, event Event) {
	if c == nil {
		return
	}
	if value := strings.TrimSpace(event.Source); value != "" {
		c.source = value
	}
	if value := strings.TrimSpace(event.Industry); value != "" {
		c.industry = value
	}
	if value := strings.TrimSpace(event.Segment); value != "" {
		c.segment = value
	}
	if value := strings.TrimSpace(event.CampaignID); value != "" {
		c.campaignID = value
	}
	if value := strings.TrimSpace(event.ManagerID); value != "" {
		c.managerID = value
	}
}

// isDelivered сообщает, что исход — успешная доставка или валидность адреса.
func isDelivered(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "delivered", "delivery", "valid":
		return true
	default:
		return false
	}
}

// isInvalidEmail сообщает, что проверка адреса признала его невалидным.
func isInvalidEmail(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "invalid", "undeliverable", "rejected":
		return true
	default:
		return false
	}
}

// isUnsubscribe сообщает, что исход — отписка получателя.
func isUnsubscribe(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "unsubscribed", "unsubscribe", "opted-out", "opt-out":
		return true
	default:
		return false
	}
}

// isTransition сообщает, что исход — переход по трекинг-ссылке.
func isTransition(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "transitioned", "transition", "clicked", "click":
		return true
	default:
		return false
	}
}

// isDuplicate сообщает, что статус лида — признак дубля.
func isDuplicate(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "duplicate", "duplicated", "dup":
		return true
	default:
		return false
	}
}

// isWon сообщает, что исход лида — заключённая сделка.
func isWon(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "won", "deal", "closed-won", "closed_won":
		return true
	default:
		return false
	}
}

// isDrafted сообщает, что исход шага — сформированный черновик, ожидающий
// утверждения (объём простоя).
func isDrafted(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "drafted", "pending", "held", "stalled":
		return true
	default:
		return false
	}
}

// isDraftApproved сообщает, что черновик шага получил положительное решение.
func isDraftApproved(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "approved":
		return true
	default:
		return false
	}
}

// isDraftResolved сообщает, что шаг более не ждёт утверждения: отклонён,
// удалён или завершён.
func isDraftResolved(outcome string) bool {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "rejected", "removed", "completed":
		return true
	default:
		return false
	}
}
