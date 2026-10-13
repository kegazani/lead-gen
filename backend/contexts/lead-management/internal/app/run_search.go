package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
	"lead-gen/contracts/integration-platform/search"
)

// CrawlLimits — пределы обхода, задаваемые конфигурацией сервиса.
//
// MaxPages, IncludePaths и ExcludePaths больше не являются операторским
// вводом: их задаёт composition root. MaxPages — внутренний предохранитель
// расхода (не бюджет кредитов), IncludePaths/ExcludePaths — внутренние
// значения конфигурации. Оператор задаёт только глубину (MaxDepth) и время
// (MaxTimeSeconds) — см. ports.PlanLimits.
type CrawlLimits struct {
	// MaxPages — внутренний предохранитель числа страниц обхода.
	MaxPages int
	// MaxDepth — максимальная глубина обхода (операторский лимит).
	MaxDepth int
	// MaxTimeSeconds — предельное время обхода в секундах (операторский лимит).
	MaxTimeSeconds int
	// IncludePaths — внутренние regex-паттерны путей, которые обходятся.
	IncludePaths []string
	// ExcludePaths — внутренние regex-паттерны путей, которые отсекаются.
	ExcludePaths []string
}

// RunSearch — use case запуска поиска и обогащения.
type RunSearch struct {
	Jobs     ports.SearchJobStore
	Profiles ports.LeadProfileRepository
	Contacts ports.ContactRepository
	Search   search.Port
	Crawl    crawl.Port
	LLM      llm.Port // nil — без LLM-планирования
	Clock    ports.Clock
	Limits   CrawlLimits

	// RefineIterations — предел итераций добора целей (вариант B). 0 —
	// DefaultRefineIterations. Ограничивает повторные вызовы LLM.Plan, когда цели
	// плана исчерпаны, а целевое число компаний не достигнуто.
	RefineIterations int

	// Assessments — хранилище оценок компании относительно задания (nil —
	// оценка отключена). Оценка — дополнительный результат: её отсутствие или
	// ошибка не прерывают поиск и не подменяются вердиктом.
	Assessments ports.LeadAssessmentStore

	// Budget — фабрика бюджета задачи (nil — бюджет не применяется). Бюджет
	// создаётся для каждого задания, включая задание без явного кредитного лимита
	// (payload.MaxCredits == 0): безлимитный случай обрабатывает сам бюджет —
	// лимит не применяется, но фактический расход измеряется. Значение по
	// умолчанию не подставляется.
	Budget budget.Factory
	// MaxCredits — настроенный предел FirecrawlMaxCredits, применяемый как жёсткий
	// per-task cap поверх лимита, сохранённого в задании; 0 cap не задаёт.
	MaxCredits int

	// Signals — реестр отмены выполняющихся заданий (nil — сигнал отмены
	// недоступен; отмена queued-заданий всё равно работает через хранилище).
	Signals *jobs.CancelRegistry

	// Наблюдаемость: логгер, метрики и трейсер впрыскиваются composition root'ом.
	Logger            observ.Logger
	Metrics           observ.Metrics
	Tracer            observ.Tracer
	FailureNormalizer jobs.FailureNormalizer
	ServiceName       string

	// TenantID — тенант-владелец пула: задания воркера сохраняют и читают лиды в
	// этом тенанте (см. ports.WithTenant). Пусто — тенант по умолчанию.
	TenantID string
}

// errEmptyQuery — запрос поиска не может быть пустым.
var errEmptyQuery = errors.New("app: запрос поиска обязателен")

// cancelledDrainLimit ограничивает число уже полученных страниц, обрабатываемых
// после отмены. Гарантия «отмена с сохранением результата» не должна
// оборачиваться долгим домечиванием снимка: обрабатываются первые страницы от
// движка, остальные пропускаются.
const cancelledDrainLimit = 20

// DefaultRefineIterations — предел итераций добора целей по умолчанию
// (защита от зацикливания повторного планирования).
const DefaultRefineIterations = 3

// refineMinCredits — минимальная стоимость следующего шага добора (поиск и хотя
// бы одна страница), ниже которой добор не запускается при заданном бюджете.
// Бюджет задачи задан, когда Snapshot.MaxCredits > 0.
const refineMinCredits = 3

// Причины остановки задания с недостигнутой целью. Пишутся в снимок прогресса
// терминального задания; пустая причина означает достигнутую или незаданную
// цель.
const (
	// stopReasonTargetsExhausted — добор перестал давать новые цели.
	stopReasonTargetsExhausted = "цели исчерпаны"
	// stopReasonRefineLimit — исчерпан предел итераций добора.
	stopReasonRefineLimit = "предел добора целей"
	// stopReasonBudgetExhausted — исчерпан заданный кредитный бюджет.
	stopReasonBudgetExhausted = "бюджет исчерпан"
	// stopReasonRefineFailed — добор целей завершился ошибкой Plan.
	stopReasonRefineFailed = "добор целей не удался"
)

// Enqueue ставит задание поиска по запросу с бюджетом кредитов (0 — значение по
// умолчанию из конфигурации). Отрицательный бюджет отклоняется.
func (r *RunSearch) Enqueue(ctx context.Context, query string, maxCredits int) (ports.SearchJob, error) {
	if strings.TrimSpace(query) == "" {
		return ports.SearchJob{}, errEmptyQuery
	}
	if err := ports.ValidateMaxCredits(maxCredits); err != nil {
		return ports.SearchJob{}, err
	}
	return r.Jobs.CreateSearchJob(ctx, query, maxCredits)
}

// EnqueueMany ставит в очередь поиска набор запросов с общим бюджетом и
// возвращает созданные задания. Используется экраном ассистента для постановки
// выбранных seed-запросов через тот же RunSearch.Enqueue.
func (r *RunSearch) EnqueueMany(ctx context.Context, queries []string, maxCredits int) ([]ports.SearchJob, error) {
	created := make([]ports.SearchJob, 0, len(queries))
	for _, query := range queries {
		job, err := r.Enqueue(ctx, query, maxCredits)
		if err != nil {
			return nil, err
		}
		created = append(created, job)
	}
	return created, nil
}

// EnqueuePlanned ставит задание с согласованным планом обхода: при непустых
// planTargets цели берутся дословно, иначе собираются планированием по запросу
// (при отказе — единственная цель-запрос). Лимиты и целевое число компаний
// приходят от оператора, план и цель валидируются до постановки. Интерпретация не
// задана: планирование получает явную неопределённость, а не выдуманные критерии.
func (r *RunSearch) EnqueuePlanned(ctx context.Context, criteria string, planTargets []ports.PlanTarget, limits ports.PlanLimits, maxCredits, targetCompanies, maxResults int) (ports.SearchJob, error) {
	criteria = strings.TrimSpace(criteria)
	if criteria == "" {
		return ports.SearchJob{}, errEmptyQuery
	}
	if err := ports.ValidateMaxCredits(maxCredits); err != nil {
		return ports.SearchJob{}, err
	}
	if err := ports.ValidateTargetCompanies(targetCompanies); err != nil {
		return ports.SearchJob{}, err
	}
	return r.enqueueWithPlan(ctx, criteria, planTargets, limits, maxCredits, targetCompanies, maxResults, llm.Interpretation{Uncertainty: true})
}

// buildSearchPlan собирает план обхода задания. При непустых planTargets цели
// берутся ДОСЛОВНО — подтверждённый оператором план не перевыводится из текста
// запроса, и планирование по запросу не вызывается. Каждая цель проверяется
// PlanTarget.Validate, а итоговый план — SearchPlan.Validate (их делает
// вызывающий до постановки): невалидная цель отвергается, а не подменяется
// перевыводом. При пустом плане работает фолбэк на планирование по запросу с
// переданной интерпретацией. Второе значение — потраченные токены LLM.
func (r *RunSearch) buildSearchPlan(ctx context.Context, criteria string, planTargets []ports.PlanTarget, limits ports.PlanLimits, interpretation llm.Interpretation) (ports.SearchPlan, int) {
	if len(planTargets) > 0 {
		targets := make([]ports.PlanTarget, 0, len(planTargets))
		for _, t := range planTargets {
			if err := t.Validate(); err != nil {
				// Невалидная цель остаётся в плане: её отвергнет SearchPlan.Validate
				// до постановки, и планирование по запросу не подменит её.
				return ports.SearchPlan{Targets: append(targets, t), Limits: limits}, 0
			}
			targets = append(targets, t)
		}
		return ports.SearchPlan{Targets: targets, Limits: limits}, 0
	}
	targets, tokens := r.planTargets(ctx, criteria, interpretation)
	return ports.SearchPlan{Targets: planTargetsFrom(targets), Limits: limits}, tokens
}

// enqueueWithPlan собирает и валидирует план задания, затем ставит его в очередь.
// Общий путь постановки для EnqueuePlanned и EnqueueInterpreted: различается
// только интерпретация, которая уходит в планирование по запросу.
func (r *RunSearch) enqueueWithPlan(ctx context.Context, criteria string, planTargets []ports.PlanTarget, limits ports.PlanLimits, maxCredits, targetCompanies, maxResults int, interpretation llm.Interpretation) (ports.SearchJob, error) {
	plan, _ := r.buildSearchPlan(ctx, criteria, planTargets, limits, interpretation)
	if err := plan.Validate(); err != nil {
		return ports.SearchJob{}, fmt.Errorf("app: план задания: %w", err)
	}
	if r.Logger != nil {
		r.Logger.Info("запуск с согласованным планом", observ.Fields{},
			"targets", len(plan.Targets), "max_depth", plan.Limits.MaxDepth, "max_time_seconds", plan.Limits.MaxTimeSeconds,
			"target_companies", targetCompanies, "max_results", maxResults)
	}
	return r.Jobs.CreateSearchJobWithPlan(ctx, criteria, plan, maxCredits, targetCompanies, maxResults)
}

// planTargetsFrom переводит цели планирования в цели плана задания.
func planTargetsFrom(targets []llm.Target) []ports.PlanTarget {
	out := make([]ports.PlanTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, ports.PlanTarget{Query: t.Query, URL: t.URL})
	}
	return out
}

// Cancel отменяет задание поиска: фиксирует запрос отмены в хранилище и
// сигнализирует выполняющемуся заданию остановиться (если оно выполняется в этом
// процессе). Операция идемпотентна: повторная отмена не меняет исход.
func (r *RunSearch) Cancel(ctx context.Context, jobID string) error {
	if err := r.Jobs.RequestCancel(ctx, jobID); err != nil {
		return err
	}
	if r.Signals != nil {
		r.Signals.Cancel(jobID)
	}
	return nil
}

// Handle обрабатывает одно задание поиска (jobs.Handler для раннера worker).
// Обвязка наблюдаемости живёт здесь, а не в jobs.Runner: task_id — идентификатор
// задания, и его смысл знает только прикладной слой.
func (r *RunSearch) Handle(ctx context.Context, job jobs.Job) error {
	// Лиды задания принадлежат тенанту-владельцу пула: область хранения
	// проносится до адаптеров (порт читает её из контекста).
	ctx = ports.WithTenant(ctx, r.TenantID)
	f := observ.Fields{TaskID: job.ID}
	ctx = observ.WithFields(ctx, f)
	ctx, finish := r.Tracer.Start(ctx, "job.search")

	r.Logger.Info("задание взято", f)

	start := time.Now()
	err := r.handle(ctx, job)
	duration := time.Since(start)

	if err != nil {
		// Отмена — не ошибка задания: она фиксируется отдельным исходом и не
		// записывается как событие ошибки.
		if errors.Is(err, jobs.ErrCancelled) {
			r.Logger.Info("задание отменено", f)
			r.Metrics.Incr("jobs_total", map[string]string{"service": r.ServiceName, "outcome": "cancelled"})
			r.Metrics.Observe("jobs_duration_seconds", duration.Seconds(), map[string]string{"service": r.ServiceName})
			finish(err)
			return err
		}
		var se *SearchJobError
		if errors.As(err, &se) {
			_ = r.Jobs.RecordEvent(context.WithoutCancel(ctx), ports.SearchJobEvent{JobID: job.ID, Type: ports.EventError, Code: se.Code(), CreatedAt: time.Now().UTC()})
		}
		failure := normalizeSearchFailure(r.FailureNormalizer, err)
		r.Logger.Error("задание упало", nil, f,
			"error_code", failure.Code,
			"reason", failure.Message,
			"diagnostic", failure.Diagnostic)
		r.Metrics.Incr("jobs_total", map[string]string{"service": r.ServiceName, "outcome": "failed"})
		r.Metrics.Observe("jobs_duration_seconds", duration.Seconds(), map[string]string{"service": r.ServiceName})
		finish(err)
		return err
	}
	r.Logger.Info("задание готово", f)
	r.Metrics.Incr("jobs_total", map[string]string{"service": r.ServiceName, "outcome": "done"})
	r.Metrics.Observe("jobs_duration_seconds", duration.Seconds(), map[string]string{"service": r.ServiceName})
	finish(nil)
	return nil
}

// handle исполняет тело задания: разбор payload, план, поиск, обход и запись
// результата.
func (r *RunSearch) handle(ctx context.Context, job jobs.Job) error {
	payload, err := ports.DecodeSearchJobPayload(job.Payload)
	if err != nil {
		return newSearchJobError(ErrorCodePayloadInvalid, fmt.Errorf("app: разбор payload задания %s: %w", job.ID, err))
	}
	query := strings.TrimSpace(payload.Query)
	if query == "" {
		return newSearchJobError(ErrorCodePayloadInvalid, fmt.Errorf("app: задание %s без запроса", job.ID))
	}

	// Операторские параметры задания: целевое число компаний (0 — цель не
	// задана) и лимит результатов на поисковый запрос (0 — лимит адаптера).
	targetCompanies := payload.TargetCompanies
	maxResults := payload.MaxResults

	s, c := r.Search, r.Crawl
	var task budget.Task
	// Бюджет создаётся всегда, когда задана фабрика, — включая задание без явного
	// кредитного лимита (payload.MaxCredits == 0). Безлимитный случай обрабатывает
	// сам бюджет: лимит не применяется, но фактический расход измеряется. Значение
	// по умолчанию не подставляется.
	if r.Budget != nil {
		maxCredits := payload.MaxCredits
		if r.MaxCredits > 0 && (maxCredits == 0 || maxCredits > r.MaxCredits) {
			maxCredits = r.MaxCredits
		}
		t, err := r.Budget.Begin(job.ID, maxCredits)
		if err != nil {
			return newSearchJobError(ErrorCodeBudgetExhausted, fmt.Errorf("app: бюджет задания %s: %w", job.ID, err))
		}
		task = t
		s, c = t, t
		// Снимок бюджета фиксируется при любом исходе задания (успех или отказ),
		// чтобы журнал показывал расход и резерв даже для упавшего задания.
		defer func() {
			if err := r.Jobs.RecordBudget(context.WithoutCancel(ctx), job.ID, task.Snapshot()); err != nil {
				searchDiagnostic(r.Logger, true, "запись бюджета задания", "budget_record_failed", observ.Fields{TaskID: job.ID}, "service", r.ServiceName)
			}
		}()
		// Terminal settlement выполняется после всех операций, в том числе при
		// ошибке, отмене или panic. Unknown reservation освобождается только если
		// сам Guard подтвердит reconciliation свежим account balance.
		defer func() {
			finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			defer cancel()
			if err := r.Budget.Finish(finishCtx, job.ID); err != nil {
				searchDiagnostic(r.Logger, true, "завершение бюджета задания", "budget_finish_failed", observ.Fields{TaskID: job.ID}, "service", r.ServiceName)
			}
		}()
	}

	// Критерии релевантности задания: nil означает «интерпретация не задана» —
	// оценка опирается на явную неопределённость, а не на выдуманные критерии.
	interpretation := llm.Interpretation{Uncertainty: true}
	if payload.Interpretation != nil {
		interpretation = *payload.Interpretation
	}
	state := &assessmentState{jobID: job.ID, interpretation: interpretation, verdicts: make(map[string]domain.AssessmentVerdict)}
	companyIDs := make(map[string]struct{})
	// Восстановленный job использует уже durable result refs. Они не очищаются и
	// не засчитываются повторно при продолжении того же задания.
	if reader, ok := r.Jobs.(interface {
		GetSearchJob(context.Context, string) (ports.SearchJob, error)
	}); ok {
		previous, err := reader.GetSearchJob(ctx, job.ID)
		if err != nil {
			return newSearchJobError(ErrorCodeResultSaveFailed, fmt.Errorf("чтение прежнего результата задания %s: %w", job.ID, err))
		}
		for _, id := range previous.ResultRef {
			if id = strings.TrimSpace(id); id != "" {
				companyIDs[id] = struct{}{}
			}
		}
	}

	rep := newProgressReporter(r.Jobs, job.ID, r.now, task, r.Logger, r.FailureNormalizer)
	rep.target(targetCompanies)
	rep.result(func() []string { return sortedCompanyIDs(companyIDs) })
	rep.phase(ctx, PhasePlanning)

	// План из payload — согласованный диалогом и подтверждённый оператором;
	// пустые цели означают фолбэк на планирование по запросу.
	limits := r.effectiveLimits(payload.Plan)
	var targets []llm.Target
	if targetReached(targetCompanies, companyIDs) {
		// Восстановленный результат уже достиг квоты — не запускаем повторный
		// платный план и не начинаем краулинг.
		rep.plannedTargets(0)
	} else if payload.Plan != nil && payload.Plan.HasTargets() {
		targets = targetsFromPlan(payload.Plan)
		rep.plannedTargets(len(targets))
		rep.tokens(0)
	} else {
		var tokens int
		targets, tokens = r.planTargets(ctx, query, interpretation)
		rep.plannedTargets(len(targets))
		rep.tokens(tokens)
	}

	// crawledURLs — множество нормализованных уже обойдённых URL: повторный обход
	// того же адреса (в том числе из другой цели) пропускается и не тратит
	// кредиты.
	crawledURLs := make(map[string]struct{})
	// usedTargets/usedList — нормализованное множество и исходные подписи уже
	// задействованных целей: добор не должен повторять их.
	usedTargets := make(map[string]struct{}, len(targets))
	usedList := make([]string, 0, len(targets))
	for _, t := range targets {
		key := targetKey(t)
		if key == "" {
			continue
		}
		if _, seen := usedTargets[key]; seen {
			continue
		}
		usedTargets[key] = struct{}{}
		usedList = append(usedList, targetLabel(t))
	}
	// stopReason — причина остановки задания с недостигнутой целью; пусто, если
	// цель достигнута или не задана.
	stopReason := ""
	targetMet := func() bool { return targetReached(targetCompanies, companyIDs) }

	// finishCancelled фиксирует итоговый прогресс (найденные компании) через
	// отсоединённый контекст и завершает задание отменённым. Снимок найденных
	// компаний записывает сам репортер на границе flush.
	finishCancelled := func() error {
		ids := sortedCompanyIDs(companyIDs)
		rep.found(len(ids))
		rep.stop(stopReason, targetMet())
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), searchLeadsDrainBudget)
		defer cancel()
		rep.flush(flushCtx)
		return jobs.ErrCancelled
	}

	refinesDone := 0
	for i := 0; ; i++ {
		if ctx.Err() != nil {
			return finishCancelled()
		}
		// Остановка по цели проверяется между целями.
		if targetMet() {
			break
		}
		if i < len(targets) {
			if err := r.processTarget(ctx, targets[i], companyIDs, crawledURLs, s, c, rep, limits, state, targetCompanies, maxResults); err != nil {
				if errors.Is(err, jobs.ErrCancelled) {
					return finishCancelled()
				}
				flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), searchLeadsDrainBudget)
				rep.flush(flushCtx)
				cancel()
				if saveErr := rep.resultError(); saveErr != nil {
					return newSearchJobError(ErrorCodeResultSaveFailed, errors.Join(err, saveErr))
				}
				return err
			}
			continue
		}
		// Все цели текущего плана обработаны. Без заданной цели задание завершается;
		// иначе — добор целей (вариант B): повторно запрашиваем план, исключая уже
		// использованные цели, и продолжаем цикл. Остановка добора — по пустому
		// приросту, ошибке Plan, пределу итераций или исчерпании бюджета.
		if targetCompanies <= 0 {
			break
		}
		if refinesDone >= r.refineLimit() {
			stopReason = stopReasonRefineLimit
			break
		}
		// Перед итерацией добора при заданном бюджете проверяем снимок: если
		// доступного остатка меньше стоимости следующего шага, добор не запускается.
		if task != nil {
			snap := task.Snapshot()
			if snap.MaxCredits > 0 && snap.Available < refineMinCredits {
				stopReason = stopReasonBudgetExhausted
				break
			}
		}
		rep.phase(ctx, PhaseExpanding)
		candidates, tokens, err := r.planMoreTargets(ctx, query, interpretation, usedList)
		rep.tokens(tokens)
		if err != nil {
			stopReason = stopReasonRefineFailed
			break
		}
		more := make([]llm.Target, 0, len(candidates))
		for _, cand := range candidates {
			key := targetKey(cand)
			if key == "" {
				continue
			}
			if _, seen := usedTargets[key]; seen {
				continue
			}
			usedTargets[key] = struct{}{}
			usedList = append(usedList, targetLabel(cand))
			more = append(more, cand)
		}
		if len(more) == 0 {
			stopReason = stopReasonTargetsExhausted
			break
		}
		// Продолжаем цикл с первой добавленной цели: индекс переводится на
		// элемент перед ней, шаг цикла сдвигает его на саму цель.
		start := len(targets)
		targets = append(targets, more...)
		refinesDone++
		i = start - 1
	}

	if ctx.Err() != nil {
		return finishCancelled()
	}
	rep.phase(ctx, PhaseFinalizing)

	ids := sortedCompanyIDs(companyIDs)
	rep.found(len(ids))
	rep.stop(stopReason, targetMet())
	rep.flush(ctx)
	if err := rep.resultError(); err != nil {
		return newSearchJobError(ErrorCodeResultSaveFailed, err)
	}
	return nil
}

// sortedCompanyIDs возвращает отсортированные идентификаторы найденных компаний.
func sortedCompanyIDs(m map[string]struct{}) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// planTargets раскладывает запрос в серию целей и возвращает потраченные токены
// LLM: с LLM — через Plan с откатом на исходный запрос при ошибке; без LLM —
// единственный поисковый запрос и нулевой расход. Переданная интерпретация
// уходит в PlanRequest: генерация плана опирается на критерии задания, а не
// только на текст запроса.
func (r *RunSearch) planTargets(ctx context.Context, query string, interpretation llm.Interpretation) ([]llm.Target, int) {
	if r.LLM == nil {
		return []llm.Target{{Query: query}}, 0
	}
	targets, usage, err := r.LLM.Plan(ctx, llm.PlanRequest{Query: query, Interpretation: interpretation})
	if err != nil {
		r.Metrics.Incr("downstream_errors_total", map[string]string{"service": r.ServiceName, "engine": "llm"})
		return []llm.Target{{Query: query}}, 0
	}
	return targets, usage.TotalTokens
}

// targetsFromPlan переводит цели плана задания в цели планирования.
func targetsFromPlan(plan *ports.SearchPlan) []llm.Target {
	targets := make([]llm.Target, 0, len(plan.Targets))
	for _, t := range plan.Targets {
		targets = append(targets, llm.Target{Query: t.Query, URL: t.URL})
	}
	return targets
}

// effectiveLimits совмещает пределы конфигурации с операторскими лимитами
// плана задания: план перекрывает конфигурацию только по глубине и времени.
// Число страниц и include/exclude-пути берутся только из конфигурации — они
// больше не операторский ввод.
func (r *RunSearch) effectiveLimits(plan *ports.SearchPlan) CrawlLimits {
	limits := r.Limits
	if plan == nil {
		return limits
	}
	if plan.Limits.MaxDepth > 0 {
		limits.MaxDepth = plan.Limits.MaxDepth
	}
	if plan.Limits.MaxTimeSeconds > 0 {
		limits.MaxTimeSeconds = plan.Limits.MaxTimeSeconds
	}
	return limits
}

// refineLimit возвращает предел итераций добора целей: значение поля
// RefineIterations либо DefaultRefineIterations.
func (r *RunSearch) refineLimit() int {
	if r.RefineIterations > 0 {
		return r.RefineIterations
	}
	return DefaultRefineIterations
}

// planMoreTargets повторно запрашивает план у LLM, передавая перечень уже
// использованных целей, чтобы получить новые. Второе значение — потраченные
// токены. Ошибка Plan возвращается вызывающему: добор останавливается, но задание
// не падает.
func (r *RunSearch) planMoreTargets(ctx context.Context, query string, interpretation llm.Interpretation, used []string) ([]llm.Target, int, error) {
	if r.LLM == nil {
		return nil, 0, nil
	}
	targets, usage, err := r.LLM.Plan(ctx, llm.PlanRequest{Query: refineQuery(query, used), Interpretation: interpretation})
	if err != nil {
		r.Metrics.Incr("downstream_errors_total", map[string]string{"service": r.ServiceName, "engine": "llm"})
		return nil, 0, err
	}
	return targets, usage.TotalTokens, nil
}

// refineQuery дополняет исходный запрос перечнем уже использованных целей: LLM
// должен вернуть новые цели, а не повторить прежние. Перечень приходит в
// порядке использования; пустой перечень оставляет запрос без изменений.
func refineQuery(query string, used []string) string {
	if len(used) == 0 {
		return query
	}
	var b strings.Builder
	b.WriteString(query)
	b.WriteString("\n\nУже использованные цели (не повторяй их, предложи новые):\n")
	for _, item := range used {
		b.WriteString("- ")
		b.WriteString(item)
		b.WriteString("\n")
	}
	return b.String()
}

// targetKey возвращает нормализованный ключ цели для дедупликации; запрос и URL
// различаются префиксом. Пустая цель даёт пустой ключ.
func targetKey(t llm.Target) string {
	if q := normalizeSearchQuery(t.Query); q != "" {
		return "q:" + q
	}
	if u := normalizeTargetURL(t.URL); u != "" {
		return "u:" + u
	}
	return ""
}

// targetLabel возвращает человекочитаемую подпись цели (её исходный текст).
func targetLabel(t llm.Target) string {
	if strings.TrimSpace(t.Query) != "" {
		return strings.TrimSpace(t.Query)
	}
	return strings.TrimSpace(t.URL)
}

// targetReached сообщает, достигнуто ли целевое число компаний: цель задана и
// найдено не меньше уникальных компаний.
func targetReached(targetCompanies int, companyIDs map[string]struct{}) bool {
	return targetCompanies > 0 && len(companyIDs) >= targetCompanies
}

// normalizeSearchQuery приводит поисковый запрос цели к канонической форме для
// дедупликации: trim и нижний регистр.
func normalizeSearchQuery(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

// normalizeTargetURL приводит стартовый URL цели к канонической форме для
// дедупликации обхода: нижний регистр, без схемы и завершающего слэша.
func normalizeTargetURL(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "//")
	s = strings.TrimSuffix(s, "/")
	return s
}

// processTarget исполняет одну цель плана: поиск и обход результатов либо обход
// стартового URL напрямую. maxResults — лимит результатов поиска (0 — лимит
// адаптера); targetCompanies — целевое число компаний (0 — цель не задана),
// обход прекращается при его достижении.
func (r *RunSearch) processTarget(ctx context.Context, target llm.Target, companyIDs, crawledURLs map[string]struct{}, s search.Port, c crawl.Port, rep *progressReporter, limits CrawlLimits, state *assessmentState, targetCompanies, maxResults int) error {
	if strings.TrimSpace(target.Query) != "" {
		if ctx.Err() != nil {
			return jobs.ErrCancelled
		}
		rep.phase(ctx, PhaseSearching)
		results, err := s.Search(ctx, search.Query{Text: target.Query, MaxResults: maxResults})
		if err != nil {
			if ctx.Err() != nil {
				return jobs.ErrCancelled
			}
			// Неудачный поисковый запрос не должен ронять всё задание: остальные цели
			// ещё могут дать компании. Сбой фиксируется в логе и метрике, а обход
			// продолжается со следующей цели; повторной попытки по этому запросу нет.
			r.Metrics.Incr("downstream_errors_total", map[string]string{"service": r.ServiceName, "engine": "search"})
			if r.Logger != nil {
				searchDiagnostic(r.Logger, false, "поиск: запрос не удался", "search_request_failed", observ.Fields{TaskID: rep.id}, "service", r.ServiceName)
			}
			return nil
		}
		rep.plannedURLs(len(results))
		for _, res := range results {
			if strings.TrimSpace(res.URL) == "" {
				continue
			}
			// Остановка по цели проверяется между страницами обхода.
			if targetReached(targetCompanies, companyIDs) {
				return nil
			}
			if err := r.crawlURL(ctx, res.URL, companyIDs, crawledURLs, c, rep, limits, state, targetCompanies); err != nil {
				return err
			}
		}
		return nil
	}
	if strings.TrimSpace(target.URL) != "" {
		rep.plannedURLs(1)
		if targetReached(targetCompanies, companyIDs) {
			return nil
		}
		return r.crawlURL(ctx, target.URL, companyIDs, crawledURLs, c, rep, limits, state, targetCompanies)
	}
	return nil
}

// crawlURL обходит один стартовый URL и обрабатывает каждую обойдённую страницу.
// Отмена на границе обхода прерывает работу: уже сохранённые карточки остаются.
// Повторный обход уже обойдённого URL пропускается (crawledURLs). targetCompanies —
// целевое число компаний (0 — цель не задана): при достижении обработка страниц
// прекращается.
func (r *RunSearch) crawlURL(ctx context.Context, startURL string, companyIDs, crawledURLs map[string]struct{}, c crawl.Port, rep *progressReporter, limits CrawlLimits, state *assessmentState, targetCompanies int) error {
	if ctx.Err() != nil {
		return jobs.ErrCancelled
	}
	// Дедупликация обойдённых URL: повторный обход того же адреса не добавит
	// компаний, но потратит кредиты.
	if key := normalizeTargetURL(startURL); key != "" {
		if _, done := crawledURLs[key]; done {
			return nil
		}
		crawledURLs[key] = struct{}{}
	}

	// Дедлайн времени ставится app-слоем только при заданном MaxTimeSeconds и
	// ограничивает обход ОДНОГО сайта, а не всё задание: его истечение
	// обрабатывает частично собранные страницы и переходит к следующей цели, не
	// отменяя задание. Реальная отмена — только по родительскому контексту
	// (оператор или остановка процесса).
	crawlCtx := ctx
	if limits.MaxTimeSeconds > 0 {
		var cancel context.CancelFunc
		crawlCtx, cancel = context.WithTimeout(ctx, time.Duration(limits.MaxTimeSeconds)*time.Second)
		defer cancel()
	}

	rep.phase(crawlCtx, PhaseCrawling)
	rep.url(crawlCtx, startURL)

	outcome, err := c.Crawl(crawlCtx, crawl.Request{
		StartURL:       startURL,
		MaxPages:       limits.MaxPages,
		MaxDepth:       limits.MaxDepth,
		MaxTimeSeconds: limits.MaxTimeSeconds,
		IncludePaths:   limits.IncludePaths,
		ExcludePaths:   limits.ExcludePaths,
	})
	// Остановка обхода — таймаут (crawl.ErrTimeout) или отмена (crawl.ErrCancelled) —
	// не отменяет задание: это ограниченная остановка одного сайта. Уже добытые
	// страницы обрабатываются, а цикл продолжает следующие цели.
	bounded := errors.Is(err, crawl.ErrCancelled) || errors.Is(err, crawl.ErrTimeout)
	if err != nil && !bounded {
		if ctx.Err() != nil {
			return jobs.ErrCancelled
		}
		// Сбой обхода одного сайта не должен ронять всё задание: остальные цели ещё
		// могут дать компании. Ошибка фиксируется в логе и метрике, а обход
		// продолжается со следующей цели — «ничего не упускать» важнее строгости.
		r.Metrics.Incr("downstream_errors_total", map[string]string{"service": r.ServiceName, "engine": "crawl"})
		if r.Logger != nil {
			searchDiagnostic(r.Logger, false, "поиск: обход сайта не удался", "crawl_request_failed", observ.Fields{TaskID: rep.id}, "service", r.ServiceName)
		}
		return nil
	}

	// Уже добытые страницы обрабатываются даже при исчерпании времени сайта или
	// отмене: карточки по ним сохраняются. При этом запись идёт через отсоединённый
	// контекст, чтобы сохранение не сорвалось из-за истёкшего дедлайна, а число
	// дренируемых страниц ограничено, чтобы не оборачиваться долгим домечиванием.
	pageCtx := crawlCtx
	if bounded {
		pageCtx = context.WithoutCancel(ctx)
	}
	rep.crawled(len(outcome.Pages))
	rep.phase(pageCtx, PhaseExtracting)
	for i, page := range outcome.Pages {
		// Остановка по цели проверяется между страницами обхода.
		if targetReached(targetCompanies, companyIDs) {
			break
		}
		if bounded {
			if i >= cancelledDrainLimit {
				break
			}
		} else if pageCtx.Err() != nil {
			break
		}
		if err := r.processPage(pageCtx, page, companyIDs, c, rep, state); err != nil {
			return err
		}
	}
	// Итоговый сброс прогресса (в том числе при исчерпании времени сайта) — с
	// отсоединённым контекстом.
	rep.flush(pageCtx)
	// Отмена — только по родительскому контексту. Истечение дедлайна одного сайта
	// задание не отменяет: цикл задач обработает следующую цель.
	if ctx.Err() != nil {
		return jobs.ErrCancelled
	}
	return nil
}

// processPage обрабатывает одну страницу: слои экстракции 1–2, оценка качества,
// эскалация на crawl.Port.Extract при низком качестве, слияние и сохранение.
func (r *RunSearch) processPage(ctx context.Context, page crawl.Page, companyIDs map[string]struct{}, c crawl.Port, rep *progressReporter, state *assessmentState) error {
	if ctx.Err() != nil {
		return jobs.ErrCancelled
	}
	at := r.now()

	name, description, socials := r.extractLayer1(page)
	if !companySource(page.URL) || !usableCompanyName(name.Value) {
		rep.addParseErrors(1)
		return nil
	}
	cheapContacts := r.extractLayer2(page)
	cheapEmails, cheapPhones := contactCounts(cheapContacts)

	contacts := cheapContacts
	escalated := false
	extractedContacts := 0
	if !isFullCard(name, description, contacts) {
		escalated = true
		candidates, err := c.Extract(ctx, crawl.ExtractRequest{URL: page.URL})
		if err == nil {
			extracted := contactCandidatesFromExtract(candidates, page, at)
			extractedContacts = len(extracted)
			contacts = mergeContacts(cheapContacts, extracted)
		} else {
			r.Metrics.Incr("downstream_errors_total", map[string]string{"service": r.ServiceName, "engine": "crawl"})
			// Ошибка эскалации не фатальна, но раньше была не видна: страница
			// молча оставалась без контактов, и причина «email не нашёлся» не
			// всплывала. Теперь она пишется в лог.
			if r.Logger != nil {
				searchDiagnostic(r.Logger, false, "поиск: эскалация на extract не удалась", "extract_request_failed", observ.Fields{TaskID: rep.id}, "service", r.ServiceName)
			}
		}
	}

	identity := NormalizeDomain(page.URL)
	// Видимость извлечения: сколько контактов дал дешёвый regex-слой, сработала
	// ли эскалация и что она вернула. Без этой строки провал «email не ищется»
	// неотличим от «на странице его нет».
	if r.Logger != nil {
		r.Logger.Info("поиск: страница обработана", observ.Fields{TaskID: rep.id},
			"service", r.ServiceName,
			"cheap_contacts", len(cheapContacts),
			"cheap_emails", cheapEmails,
			"cheap_phones", cheapPhones,
			"escalated", escalated,
			"extracted_contacts", extractedContacts,
			"contacts", len(contacts),
		)
	}

	if !usableCrawlPage(page, name, contacts) {
		// Страница не дала ни названия, ни домена: разбор не удался, попытка не
		// засчитывается в найденные компании.
		rep.addParseErrors(1)
		return nil
	}
	if err := r.saveCompany(ctx, identity, name, description, socials, contacts, at); err != nil {
		return err
	}
	companyIDs[identity] = struct{}{}
	rep.found(len(companyIDs))

	// Оценка привязывается к паре (задание, компания) и сохраняется после
	// успешного сохранения компании. Она не превращает технику разбора в
	// вердикт: страница без названия и домена уже отсеяна выше, а ошибка оценки
	// не прерывает поиск.
	r.assessCompany(ctx, state, identity, name, description, page, rep)
	return nil
}

// assessmentState — состояние оценки компаний в рамках одного задания: его
// идентификатор, критерии релевантности и накопленное распределение вердиктов.
// Не хранится в RunSearch: один экземпляр обслуживает несколько заданий
// параллельно, поэтому состояние живёт в обработчике конкретного задания.
type assessmentState struct {
	jobID          string
	interpretation llm.Interpretation
	// verdicts — текущий вердикт каждой оценённой компании в этом задании. Служит
	// источником распределения и защищает от двойного счёта при повторной оценке
	// одной компании (дозагрузка страниц).
	verdicts map[string]domain.AssessmentVerdict
}

// assessCompany вычисляет и сохраняет оценку компании относительно задания и
// обновляет распределение вердиктов в прогрессе. Если оценка не настроена (нет
// хранилища оценок или LLM), она пропускается без ошибки. Ошибка LLM или записи
// логируется и не прерывает поиск: вердикт не выдумывается, а ошибка извлечения
// не выдаётся за бизнес-оценку.
func (r *RunSearch) assessCompany(ctx context.Context, state *assessmentState, companyID string, name, description domain.AttributedValue, page crawl.Page, rep *progressReporter) {
	if r.Assessments == nil || r.LLM == nil {
		return
	}
	at := r.now()
	assessment, _, err := (&AssessCompany{LLM: r.LLM, Clock: r.Clock}).Assess(ctx, AssessInput{
		JobID:              state.jobID,
		CompanyID:          companyID,
		CompanyName:        name.Value,
		CompanyDescription: description.Value,
		Interpretation:     state.interpretation,
		Material:           []llm.AssessMaterial{{URL: page.URL, Text: page.Markdown}},
	})
	if err != nil {
		// Ошибка оценки не прерывает поиск и не превращается в вердикт, но и не
		// теряется: она фиксируется счётчиком прогресса и структурным логом с
		// task_id и безопасным кодом диагностики.
		rep.addAssessmentErrors(1)
		if r.Logger != nil {
			searchDiagnostic(r.Logger, true, "поиск: оценка компании не удалась", "assessment_compute_failed", observ.Fields{TaskID: state.jobID}, "service", r.ServiceName)
		}
		return
	}

	// Повторная оценка компании в том же задании (компания встретилась на
	// нескольких страницах) заменяет прежнюю и фиксирует ревизию событием
	// lead-assessment-revised вместо второй записи. Оценка остаётся привязанной
	// к заданию. Распределение вердиктов пересчитывается из карты, поэтому не
	// двоится.
	stored, getErr := r.Assessments.GetByJobAndCompany(ctx, state.jobID, companyID)
	switch {
	case getErr == nil:
		if err := stored.ChangeVerdict(assessment.Verdict(), assessment.SignalKind(), assessment.Evidence(), at); err != nil {
			r.logAssessmentWarn(state.jobID, "ревизия оценки компании", "assessment_revision_failed")
			return
		}
		if err := r.Assessments.Save(ctx, stored); err != nil {
			r.logAssessmentError(state.jobID, "assessment_save_failed")
			return
		}
		assessment = stored
	case errors.Is(getErr, ports.ErrAssessmentNotFound):
		if err := r.Assessments.Save(ctx, assessment); err != nil {
			r.logAssessmentError(state.jobID, "assessment_save_failed")
			return
		}
	default:
		r.logAssessmentWarn(state.jobID, "чтение прежней оценки компании", "assessment_read_failed")
		return
	}

	state.verdicts[companyID] = assessment.Verdict()
	rep.verdicts(countVerdicts(state.verdicts))
}

// countVerdicts считает распределение оценок задания по текущим вердиктам
// компаний. Пересчёт из карты (а не инкремент) исключает двойной счёт при
// повторной оценке одной компании.
func countVerdicts(verdicts map[string]domain.AssessmentVerdict) ports.VerdictCounts {
	var counts ports.VerdictCounts
	for _, v := range verdicts {
		switch v {
		case domain.VerdictFits:
			counts.Fits++
		case domain.VerdictHypothesis:
			counts.Hypothesis++
		case domain.VerdictUnsuitable:
			counts.Unsuitable++
		}
	}
	return counts
}

// logAssessmentWarn пишет предупреждение оценки; оценка — дополнительный
// результат и не прерывает поиск.
func (r *RunSearch) logAssessmentWarn(jobID, what, diagnosticCode string) {
	if r.Logger == nil {
		return
	}
	searchDiagnostic(r.Logger, false, "поиск: "+what, diagnosticCode, observ.Fields{TaskID: jobID}, "service", r.ServiceName)
}

// logAssessmentError пишет ошибку записи оценки, не прерывая поиск.
func (r *RunSearch) logAssessmentError(jobID, diagnosticCode string) {
	if r.Logger == nil {
		return
	}
	searchDiagnostic(r.Logger, true, "поиск: сохранение оценки компании", diagnosticCode, observ.Fields{TaskID: jobID}, "service", r.ServiceName)
}

// saveCompany создаёт карточку компании либо дополняет существующую (дедупликация
// по нормализованному домену), затем сохраняет её контакты.
func (r *RunSearch) saveCompany(ctx context.Context, identity string, name, description domain.AttributedValue, socials []domain.AttributedValue, contacts []contactCandidate, at time.Time) error {
	return persistCompany(ctx, r.Profiles, r.Contacts, identity, name, description, socials, contacts, at)
}

// persistCompany создаёт карточку компании либо дополняет существующую
// (дедупликация по нормализованному домену), затем сохраняет её контакты. Общий
// код планового обхода (RunSearch) и синхронного поиска под заявку
// (SearchLeadsForRequest).
func persistCompany(ctx context.Context, profiles ports.LeadProfileRepository, contactsRepo ports.ContactRepository, identity string, name, description domain.AttributedValue, socials []domain.AttributedValue, contacts []contactCandidate, at time.Time) error {
	companyID := domain.CompanyID(identity)

	existing, err := profiles.FindByID(ctx, companyID)
	switch {
	case err == nil:
		existing.Enrich(name, description, socials, at)
		if err := profiles.Save(ctx, existing); err != nil {
			return err
		}
	case errors.Is(err, ports.ErrProfileNotFound):
		company, err := domain.CreateCompany(domain.CreateCompanyParams{
			Domain:      identity,
			Name:        name,
			Description: description,
			Socials:     socials,
			CreatedAt:   at,
		})
		if err != nil {
			return err
		}
		if err := profiles.Save(ctx, company); err != nil {
			return err
		}
	default:
		return err
	}

	return persistContacts(ctx, contactsRepo, companyID, contacts, at)
}

// persistContacts сохраняет контакты компании, пропуская уже существующие
// (дедупликация по нормализованному email либо телефону в пределах компании).
func persistContacts(ctx context.Context, repo ports.ContactRepository, companyID domain.CompanyID, contacts []contactCandidate, at time.Time) error {
	existing, err := repo.ListByCompany(ctx, companyID)
	if err != nil {
		return err
	}
	seen := make(map[string]*domain.Contact, len(existing))
	for _, c := range existing {
		if id := contactIdentityFromDomain(c); id != "" {
			seen[id] = c
		}
	}
	for _, cand := range contacts {
		id := cand.identity()
		if id == "" {
			continue
		}
		contact, err := cand.toDomain(companyID, at)
		if err != nil {
			return err
		}
		if previous := seen[id]; previous != nil {
			if cand.phone.prov.Evidence == "" && cand.email.prov.Evidence == "" {
				continue
			}
			// Старые записи могут иметь ID, отличный от канонического адреса.
			contact, err = domain.CreateContact(domain.CreateContactParams{ID: previous.ID().String(), CompanyID: companyID, Email: contact.Email(), Phone: contact.Phone(), Role: contact.Role(), CreatedAt: at})
			if err != nil {
				return err
			}
			if err := previous.Enrich(contact, at); err != nil {
				return err
			}
			contact = previous
		}
		if err := repo.Save(ctx, contact); err != nil {
			return err
		}
		seen[id] = contact
	}
	return nil
}

// now возвращает текущий момент часов либо системное время, если часы не заданы.
func (r *RunSearch) now() time.Time {
	if r.Clock != nil {
		return r.Clock.Now()
	}
	return time.Now().UTC()
}

// isFullCard — «полная карточка»: есть название, описание и хотя бы один контакт
// с email или телефоном.
func isFullCard(name, description domain.AttributedValue, contacts []contactCandidate) bool {
	if name.Value == "" || description.Value == "" {
		return false
	}
	for _, c := range contacts {
		if c.hasContact() {
			return true
		}
	}
	return false
}

// contactCounts считает, сколько кандидатов несут email и сколько — телефон.
// Используется для диагностической строки об извлечении: она отличает «email
// не ищется» (пусто на всех страницах) от «на странице его нет».
func contactCounts(contacts []contactCandidate) (emails, phones int) {
	for _, c := range contacts {
		if strings.TrimSpace(c.email.value) != "" {
			emails++
		}
		if strings.TrimSpace(c.phone.value) != "" {
			phones++
		}
	}
	return emails, phones
}

// contactIdentityFromDomain — идентификатор контакта: нормализованный email,
// иначе нормализованный телефон.
func contactIdentityFromDomain(c *domain.Contact) string {
	if e := c.Email(); e != nil {
		if id := NormalizeEmail(e.Value); id != "" {
			return id
		}
	}
	if p := c.Phone(); p != nil {
		if id := NormalizePhone(p.Value); id != "" {
			return id
		}
	}
	return ""
}
