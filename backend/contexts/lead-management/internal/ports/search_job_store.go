package ports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/backend/platform/jobs"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/llm"
)

// SearchJob — операционная запись задания поиска для витрины журнала.
type SearchJob struct {
	// ID — идентификатор задания.
	ID string
	// Query — запрос оператора.
	Query string
	// Status — состояние задания (queued/running/done/failed/cancelled).
	Status jobs.Status
	// ResultRef — идентификаторы найденных компаний (ссылка на результаты).
	ResultRef []string
	// Error — причина отказа.
	Error string
	// ErrorCode — машинный код отказа (пусто, если отказ без кода).
	ErrorCode string
	// QueuePosition — позиция в очереди (1 — первый в очереди; 0 — не в очереди).
	QueuePosition int
	// Budget — снимок бюджета Firecrawl задачи (nil — бюджет не применялся).
	Budget *budget.Snapshot
	// MaxCredits — бюджет кредитов Firecrawl, заданный на запрос (0 — бюджет не
	// задан: кредитный лимит не применяется).
	MaxCredits int
	// Interpretation — критерии релевантности задания, отдельные от технического
	// плана обхода (nil — интерпретация не задана).
	Interpretation *llm.Interpretation
	// Progress — наблюдаемый прогресс выполнения (nil — прогресс не публиковался).
	Progress *SearchJobProgress
	// CreatedAt — момент постановки.
	CreatedAt time.Time
	// UpdatedAt — момент последнего изменения.
	UpdatedAt time.Time
}

// SearchJobProgress — наблюдаемый прогресс выполнения задания поиска.
type SearchJobProgress struct {
	// Phase — текущая фаза задания (константы Phase* прикладного слоя).
	Phase string
	// CurrentURL — обрабатываемый адрес (пусто, если неприменимо).
	CurrentURL string
	// PlannedTargets — число целей плана (пунктов, на которые LLM разбил запрос).
	PlannedTargets int
	// PlannedURLs — число URL, запланированных к обходу.
	PlannedURLs int
	// PagesCrawled — число обойдённых страниц.
	PagesCrawled int
	// CompaniesFound — число найденных компаний.
	CompaniesFound int
	// TargetCompanies — целевое число компаний задания (0 — цель не задана).
	TargetCompanies int
	// TargetReached — признак достижения целевого числа компаний (при заданной
	// цели); при незаданной цели остаётся false.
	TargetReached bool
	// StopReason — причина остановки задания с недостигнутой целью (пусто, если
	// цель достигнута или не задана).
	StopReason string
	// VerdictFits — число компаний задания с оценкой «Подходит». Это
	// распределение оценок самого задания, а не метрика правильного направления
	// обхода: число компаний и страниц остаётся мониторингом.
	VerdictFits int
	// VerdictHypothesis — число компаний задания с оценкой «Гипотеза».
	VerdictHypothesis int
	// VerdictUnsuitable — число компаний задания с оценкой «Не подходит».
	VerdictUnsuitable int
	// ParseErrors — число неудачных разборов страниц (попытка не засчитывается).
	ParseErrors int
	// AssessmentErrors — число неудачных оценок компаний (вердикт не выдумывается):
	// ошибка оценки не прерывает поиск, но и не теряется молча.
	AssessmentErrors int
	// TokensSpent — потраченные токены LLM.
	TokensSpent int
	// CreditsSpent — подтверждённый расход кредитов задачи.
	CreditsSpent int
	// CreditsAvailable — доступные кредиты задачи.
	CreditsAvailable int
	// UpdatedAt — момент последнего обновления прогресса.
	UpdatedAt time.Time
}

// ErrSearchJobExists — задание с таким идентификатором уже стоит в хранилище.
// Отличает дубликат (идемпотентный повтор постановки) от прочих сбоев записи.
var ErrSearchJobExists = errors.New("ports: задание с таким идентификатором уже существует")

// ErrInvalidBudget — бюджет кредитов задания некорректен.
var ErrInvalidBudget = errors.New("ports: бюджет кредитов не может быть отрицательным")

// ValidateMaxCredits проверяет бюджет кредитов задания. Ноль означает значение по
// умолчанию (бюджет не задан явно), отрицательное значение недопустимо.
func ValidateMaxCredits(maxCredits int) error {
	if maxCredits < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidBudget, maxCredits)
	}
	return nil
}

// ErrInvalidTargetCompanies — целевое число компаний задания некорректно.
var ErrInvalidTargetCompanies = errors.New("ports: целевое число компаний не может быть отрицательным")

// ValidateTargetCompanies проверяет целевое число компаний задания. Ноль означает
// «цель не задана» (прежнее поведение задания), отрицательное значение
// недопустимо.
func ValidateTargetCompanies(targetCompanies int) error {
	if targetCompanies < 0 {
		return fmt.Errorf("%w: %d", ErrInvalidTargetCompanies, targetCompanies)
	}
	return nil
}

// SearchJobStore — хранилище заданий поиска. Реализует общий интерфейс jobs.Store
// (раннер worker: взять queued, обновить статус, запросить отмену) и добавляет
// операции витрины журнала.
type SearchJobStore interface {
	jobs.Store

	// CreateSearchJob ставит задание поиска с запросом и бюджетом кредитов
	// (0 — бюджет не задан) и возвращает его.
	CreateSearchJob(ctx context.Context, query string, maxCredits int) (SearchJob, error)
	// CreateSearchJobWithPlan ставит задание поиска с согласованным планом обхода
	// (цели и лимиты), бюджетом кредитов, целевым числом компаний (0 — цель не
	// задана) и лимитом результатов поиска (0 — лимит адаптера) и возвращает его.
	CreateSearchJobWithPlan(ctx context.Context, query string, plan SearchPlan, maxCredits, targetCompanies, maxResults int) (SearchJob, error)
	// RecordResult фиксирует ссылку на найденные компании (не меняет статус).
	RecordResult(ctx context.Context, id string, resultRef []string) error
	// RecordBudget фиксирует снимок бюджета Firecrawl задачи (не меняет статус).
	RecordBudget(ctx context.Context, id string, b budget.Snapshot) error
	// RecordProgress фиксирует прогресс выполнения задания (не меняет статус).
	RecordProgress(ctx context.Context, id string, progress SearchJobProgress) error
	// SetInterpretation фиксирует критерии релевантности задания отдельным
	// полем (не меняет статус). nil означает «интерпретация не задана».
	SetInterpretation(ctx context.Context, id string, interpretation *llm.Interpretation) error
	// RecordEvent добавляет событие жизненного цикла задания (append-only).
	RecordEvent(ctx context.Context, event SearchJobEvent) error
	// ListEvents возвращает события задания в порядке записи.
	ListEvents(ctx context.Context, jobID string) ([]SearchJobEvent, error)
	// ListSearchJobs возвращает журнал заданий, новые первыми.
	ListSearchJobs(ctx context.Context, limit int) ([]SearchJob, error)
}

// SearchJobPayload — wire-формат непрозрачного Payload задания поиска:
// JSON-объект {"query": "...", "maxCredits": N, "targetCompanies": N,
// "maxResults": N, "plan": {...}, "interpretation": {...}}. Формат фиксирован
// здесь, чтобы кодирование (адаптеры хранилища) и декодирование (прикладной слой)
// не расходились. Поля maxCredits, targetCompanies, maxResults, plan и
// interpretation необязательны: payload без них читается без бюджета, с
// незаданной целью, с лимитом поиска адаптера, без плана и без интерпретации,
// поэтому старые задания остаются совместимыми.
type SearchJobPayload struct {
	// Query — запрос оператора.
	Query string `json:"query"`
	// MaxCredits — бюджет кредитов на запрос; 0 — бюджет не задан.
	MaxCredits int `json:"maxCredits,omitempty"`
	// TargetCompanies — целевое число уникальных компаний; 0 — цель не задана
	// (остановка по исчерпании целей плана).
	TargetCompanies int `json:"targetCompanies,omitempty"`
	// MaxResults — лимит результатов на один поисковый запрос; 0 — значение по
	// умолчанию адаптера.
	MaxResults int `json:"maxResults,omitempty"`
	// Plan — согласованный план обхода (nil — плана нет, работает фолбэк на
	// планирование по запросу).
	Plan *SearchPlan `json:"plan,omitempty"`
	// Interpretation — критерии релевантности задания, отдельные от технического
	// плана обхода (nil — интерпретация не задана).
	Interpretation *llm.Interpretation `json:"interpretation,omitempty"`
}

// SearchPlan — согласованный план обхода задания: цели и технические лимиты,
// выработанные диалогом и подтверждённые оператором. Пустой план (без целей)
// означает фолбэк на планирование по запросу.
type SearchPlan struct {
	// Targets — цели обхода: поисковые запросы и стартовые URL.
	Targets []PlanTarget `json:"targets,omitempty"`
	// Limits — технические лимиты обхода.
	Limits PlanLimits `json:"limits,omitempty"`
}

// PlanTarget — одна цель плана: либо поисковый запрос, либо стартовый URL.
type PlanTarget struct {
	// Query — поисковый запрос.
	Query string `json:"query,omitempty"`
	// URL — стартовый адрес обхода.
	URL string `json:"url,omitempty"`
}

// PlanLimits — операторские лимиты плана обхода: глубина обхода и предельное
// время работы в секундах. Число страниц и include/exclude-пути перестали быть
// операторскими и в план не входят: пределы страниц и путей задаёт внутренняя
// конфигурация сервиса (design.md, Решение 5).
type PlanLimits struct {
	// MaxDepth — максимальная глубина обхода.
	MaxDepth int `json:"maxDepth,omitempty"`
	// MaxTimeSeconds — предельное время работы в секундах.
	MaxTimeSeconds int `json:"maxTimeSeconds,omitempty"`
}

// HasTargets сообщает, задаёт ли план хотя бы одну цель обхода.
func (p SearchPlan) HasTargets() bool {
	return len(p.Targets) > 0
}

// Validate проверяет цели и лимиты плана. Пустой план валиден: он означает
// фолбэк на планирование по запросу.
func (p SearchPlan) Validate() error {
	for i, t := range p.Targets {
		if err := t.Validate(); err != nil {
			return fmt.Errorf("ports: plan: цель %d: %w", i, err)
		}
	}
	if err := p.Limits.Validate(); err != nil {
		return err
	}
	return nil
}

// Validate проверяет, что цель задаёт ровно один способ поиска: либо поисковый
// запрос, либо стартовый URL, но не оба и не ни одного.
func (t PlanTarget) Validate() error {
	hasQuery := strings.TrimSpace(t.Query) != ""
	hasURL := strings.TrimSpace(t.URL) != ""
	if hasQuery == hasURL {
		return errors.New("ports: plan target: задан ровно один из query/url")
	}
	return nil
}

// Validate проверяет неотрицательность операторских лимитов. Согласованный
// лимит страниц и пути в план не входят: их задаёт внутренняя конфигурация.
func (l PlanLimits) Validate() error {
	var invalid []string
	if l.MaxDepth < 0 {
		invalid = append(invalid, "max_depth")
	}
	if l.MaxTimeSeconds < 0 {
		invalid = append(invalid, "max_time_seconds")
	}
	if len(invalid) > 0 {
		return fmt.Errorf("ports: plan limits: некорректные поля: %s", strings.Join(invalid, ", "))
	}
	return nil
}

// EncodeSearchJobPayload кодирует запрос, бюджет, план и интерпретацию в
// непрозрачный Payload задания.
func EncodeSearchJobPayload(p SearchJobPayload) ([]byte, error) {
	return json.Marshal(p)
}

// DecodeSearchJobPayload извлекает запрос, бюджет, план и интерпретацию из
// Payload задания. Payload без поля maxCredits даёт бюджет не заданным, без поля
// interpretation — интерпретацию не заданной. Лишние поля старого формата
// (maxPages/includePaths/excludePaths в плане) игнорируются.
func DecodeSearchJobPayload(payload []byte) (SearchJobPayload, error) {
	var p SearchJobPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return SearchJobPayload{}, err
	}
	return p, nil
}
