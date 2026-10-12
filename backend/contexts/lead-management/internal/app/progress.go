package app

import (
	"context"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/ports"
	"lead-gen/backend/platform/jobs"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/budget"
)

// Фазы задания поиска, отражаемые в прогрессе.
const (
	// PhasePlanning — разбор запроса и планирование целей.
	PhasePlanning = "planning"
	// PhaseSearching — поиск по цели плана.
	PhaseSearching = "searching"
	// PhaseCrawling — обход найденных URL.
	PhaseCrawling = "crawling"
	// PhaseExtracting — разбор страниц и извлечение контактов.
	PhaseExtracting = "extracting"
	// PhaseExpanding — добор целей: цели плана исчерпаны, а целевое число
	// компаний не достигнуто — идёт повторное планирование новых целей.
	PhaseExpanding = "expanding"
	// PhaseFinalizing — фиксация результата задания.
	PhaseFinalizing = "finalizing"
)

// progressReporter накапливает прогресс задания и публикует его на границах
// шагов. Постраничные счётчики обновляются в памяти и сбрасываются на границе
// (смена фазы или следующий URL), чтобы не писать в хранилище на каждой странице.
// Там же, на границе flush, фиксируется снимок найденных компаний: результат
// задания виден по ходу прогона, а не только после завершения.
type progressReporter struct {
	store             ports.SearchJobStore
	id                string
	now               func() time.Time
	task              budget.Task // nil — бюджет не применяется
	logger            observ.Logger
	failureNormalizer jobs.FailureNormalizer

	// resultSupplier — снимок найденных компаний на момент flush; nil — результат
	// общим репортером не фиксируется.
	resultSupplier func() []string
	// lastResult — последний успешно записанный снимок; запись при неизменном
	// наборе пропускается, чтобы не множить записи на каждой странице.
	lastResult     []string
	resultRecorded bool
	resultErr      error

	p ports.SearchJobProgress
}

// newProgressReporter собирает репортер прогресса задания. task может быть nil,
// logger может быть nil — тогда сбой записи результата не логируется.
func newProgressReporter(store ports.SearchJobStore, id string, now func() time.Time, task budget.Task, logger observ.Logger, normalizer jobs.FailureNormalizer) *progressReporter {
	return &progressReporter{store: store, id: id, now: now, task: task, logger: logger, failureNormalizer: normalizer}
}

// result задаёт поставщика найденных компаний: репортер вызывает его на границе
// flush и фиксирует снимок, если набор изменился.
func (rep *progressReporter) result(f func() []string) {
	rep.resultSupplier = f
}

// phase фиксирует смену фазы, публикует прогресс и пишет событие жизненного цикла.
func (rep *progressReporter) phase(ctx context.Context, phase string) {
	rep.p.Phase = phase
	rep.flush(ctx)
	if rep.store == nil {
		return
	}
	p := rep.p
	_ = rep.store.RecordEvent(ctx, ports.SearchJobEvent{JobID: rep.id, Type: ports.EventPhase, Progress: &p, CreatedAt: rep.now()})
}

// url фиксирует текущий обрабатываемый адрес и публикует прогресс.
func (rep *progressReporter) url(ctx context.Context, u string) {
	rep.p.CurrentURL = u
	rep.flush(ctx)
}

// crawled увеличивает число обойдённых страниц без немедленной публикации.
func (rep *progressReporter) crawled(n int) {
	rep.p.PagesCrawled += n
}

// found устанавливает число найденных компаний без немедленной публикации.
func (rep *progressReporter) found(n int) {
	rep.p.CompaniesFound = n
}

// verdicts устанавливает распределение оценок задания по вердиктам без
// немедленной публикации. Это отдельный от страниц и найденных компаний
// показатель: значения попадают в снимок прогресса и уходят в RecordProgress на
// flush/phase, поэтому итоговое распределение сохраняется после завершения.
func (rep *progressReporter) verdicts(counts ports.VerdictCounts) {
	rep.p.VerdictFits = counts.Fits
	rep.p.VerdictHypothesis = counts.Hypothesis
	rep.p.VerdictUnsuitable = counts.Unsuitable
}

// plannedTargets устанавливает число запланированных целей без публикации.
func (rep *progressReporter) plannedTargets(n int) {
	rep.p.PlannedTargets = n
}

// target устанавливает целевое число компаний задания без публикации. Свойство
// снимка прогресса: значение сохраняется при последующих фазах и доборе.
func (rep *progressReporter) target(n int) {
	rep.p.TargetCompanies = n
}

// stop фиксирует итог задания: достигнуто ли целевое число компаний и причина
// остановки (пусто, если цель достигнута или не задана).
func (rep *progressReporter) stop(reason string, reached bool) {
	rep.p.StopReason = reason
	rep.p.TargetReached = reached
}

// plannedURLs увеличивает число запланированных к обходу URL без публикации.
func (rep *progressReporter) plannedURLs(n int) {
	rep.p.PlannedURLs += n
}

// addParseErrors увеличивает число неудачных разборов без публикации.
func (rep *progressReporter) addParseErrors(n int) {
	rep.p.ParseErrors += n
}

// addAssessmentErrors увеличивает число неудачных оценок компаний без публикации.
// Ошибка оценки не прерывает поиск, но должна быть наблюдаемой: вердикт не
// выдумывается, а сбой вывода оценки фиксируется счётчиком.
func (rep *progressReporter) addAssessmentErrors(n int) {
	rep.p.AssessmentErrors += n
}

// tokens увеличивает число потраченных токенов LLM без публикации.
func (rep *progressReporter) tokens(n int) {
	rep.p.TokensSpent += n
}

// flush публикует накопленный прогресс. Ошибка записи не прерывает задание:
// прогресс — наблюдаемость, а не бизнес-результат. На той же границе фиксируется и
// снимок найденных компаний (если задан поставщик результата).
func (rep *progressReporter) flush(ctx context.Context) {
	if rep == nil || rep.store == nil {
		return
	}
	if rep.task != nil {
		snap := rep.task.Snapshot()
		rep.p.CreditsSpent = snap.ConfirmedSpentCredits
		rep.p.CreditsAvailable = snap.Available
	}
	rep.p.UpdatedAt = rep.now()
	_ = rep.store.RecordProgress(ctx, rep.id, rep.p)
	rep.persistResult(ctx)
}

// resultError возвращает последнюю ошибку durable-записи результата. Неудачная
// промежуточная запись может быть повторена следующим flush; успешная запись
// сбрасывает ошибку.
func (rep *progressReporter) resultError() error {
	if rep == nil {
		return nil
	}
	return rep.resultErr
}

// persistResult фиксирует снимок найденных компаний, если поставщик задан и набор
// изменился. Ошибка записи логируется и не прерывает задание: результат будет
// записан на следующей границе, а сам прогон продолжается.
func (rep *progressReporter) persistResult(ctx context.Context) {
	if rep.resultSupplier == nil {
		return
	}
	ids := rep.resultSupplier()
	if rep.resultRecorded && equalStringSlices(ids, rep.lastResult) {
		return
	}
	if err := rep.store.RecordResult(ctx, rep.id, ids); err != nil {
		rep.resultErr = err
		if rep.logger != nil {
			searchDiagnostic(rep.logger, true, "запись результата задания", "result_record_failed", observ.Fields{TaskID: rep.id})
		}
		return
	}
	rep.resultErr = nil
	rep.lastResult = ids
	rep.resultRecorded = true
}

// equalStringSlices сравнивает два среза строк поэлементно.
func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
