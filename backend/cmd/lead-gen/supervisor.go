package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// Задержки перезапуска роли: экспонента от 0.5s с потолком 30s (design.md,
// Решение 3). Потолок ограничивает ожидание при устойчивом отказе зависимости,
// jitter разводит одновременные перезапуски ролей одного процесса.
const (
	roleRestartInitialBackoff = 500 * time.Millisecond
	roleRestartMaxBackoff     = 30 * time.Second
)

// Бюджет перезапуска роли по умолчанию (design.md, Решение 2): лимит попыток и
// накопленное время. Оба задаются флагами точки входа и по умолчанию конечны,
// чтобы устойчивый отказ не превращался в вечный backoff внутри процесса.
const (
	roleRestartMaxAttempts = 5
	roleRestartBudget      = 5 * time.Minute
)

// roleRestartHealthyThreshold — порог «здоровой» работы роли: если роль
// проработала дольше порога до сбоя, прежние сбои прощаются, и бюджет
// перезапуска считается с начала нового эпизода, а не за всю жизнь процесса.
// Редкие транзиентные сбои, разделённые длинной успешной работой, не суммируются.
const roleRestartHealthyThreshold = time.Minute

// metricRoleExitLive — счётчик эскалации: роль-сервер (api/ingest) завершилась
// при живом контексте. Наблюдаемый признак аномального завершения вместо тихой
// остановки (design.md, Решение 4).
const metricRoleExitLive = "role_exit_live_total"

// restartLimits — бюджет перезапуска роли: предельное число неудачных запусков и
// накопленное время с первой ошибки. Рестарт разрешён, пока не исчерпан ни один
// из лимитов; неположительное значение отключает соответствующий лимит. В точке
// входа лимиты конечны (флаги `-role-restart-max-attempts`/`-role-restart-budget`);
// отключение (`<= 0`) нужно только тестам, поэтому супервизор не подменяет его
// дефолтом — иначе ветки `> 0` в restartExhausted были бы недостижимы.
type restartLimits struct {
	maxAttempts int
	budget      time.Duration
}

// roleSupervisor исполняет роль и перезапускает её при ошибке в пределах бюджета.
// Супервизор оборачивает **исполнение** роли, а не её сборку: ошибки
// сборки/конфигурации обрабатываются в main как фатальные (proposal.md).
type roleSupervisor struct {
	logger  observ.Logger
	metrics observ.Metrics
	process string
	service string
	role    composition.Role

	initialBackoff time.Duration
	maxBackoff     time.Duration
	limits         restartLimits

	// healthyThreshold — порог здоровой работы роли, после которого прежние сбои
	// прощаются и бюджет считается с начала нового эпизода (design.md, Решение 2).
	// Подменяется в тестах, чтобы не ждать реальных минут.
	healthyThreshold time.Duration

	// sleep ожидает задержку или отмену контекста; false означает отмену.
	// Подменяется в тестах, чтобы не ждать реальные задержки.
	sleep func(ctx context.Context, d time.Duration) bool

	// now — источник времени бюджета. Подменяется в тестах, чтобы детерминированно
	// исчерпывать бюджет по времени, не дожидаясь реальных минут.
	now func() time.Time
}

// newRoleSupervisor собирает супервизор роли со значениями задержек и бюджета.
// Лимиты передаются как есть: неположительное значение отключает лимит (нужно
// только тестам); точка входа задаёт конечные значения флагами.
func newRoleSupervisor(logger observ.Logger, metrics observ.Metrics, process, service string, role composition.Role, limits restartLimits) roleSupervisor {
	return roleSupervisor{
		logger:           logger,
		metrics:          metrics,
		process:          process,
		service:          service,
		role:             role,
		initialBackoff:   roleRestartInitialBackoff,
		maxBackoff:       roleRestartMaxBackoff,
		limits:           limits,
		healthyThreshold: roleRestartHealthyThreshold,
		sleep:            sleepContext,
		now:              time.Now,
	}
}

// run исполняет роль и возвращает nil только при плановом останове по отмене ctx.
// Ошибка роли (включая панику и завершение при живом контексте у роли-цикла)
// перезапускает роль с экспоненциальным backoff в пределах бюджета; по исчерпании
// бюджета run возвращает ошибку, и вызывающий завершает процесс неуспешно.
func (s roleSupervisor) run(ctx context.Context, runner composition.RoleRunner) error {
	attempt := 0
	var budgetStart time.Time
	for {
		if ctx.Err() != nil {
			return nil
		}
		if attempt == 0 {
			s.logger.Info("роль запущена", observ.Fields{},
				"process", s.process, "service", s.service, "role", s.role)
		}
		started := s.now()
		err := s.runOnce(ctx, runner)
		if ctx.Err() != nil {
			// Контекст отменён: возврат роли — следствие остановки, а не падение.
			return nil
		}
		if err == nil {
			// Роль завершилась при живом контексте — аномалия, а не штатный останов
			// (design.md, Решение 4).
			if isServerRole(s.role) {
				// Серверная роль api/ingest: рестарт мгновенно зациклился бы, поэтому
				// сбой эскалируется наблюдением, без тихой остановки.
				exitErr := s.unexpectedExitError()
				s.logger.Error("роль завершилась при активном процессе", exitErr, observ.Fields{},
					"process", s.process, "service", s.service, "role", s.role)
				if s.metrics != nil {
					s.metrics.Incr(metricRoleExitLive, map[string]string{"service": s.service, "role": string(s.role)})
				}
				return nil
			}
			// Роль-цикл: завершение при живом контексте — сбой, идёт в перезапуск.
			err = s.unexpectedExitError()
		}
		// Эпизод сбоя: роль, проработавшая дольше порога здоровья, «прощает»
		// прежние сбои — бюджет перезапуска считается с начала нового эпизода, а не
		// за всю жизнь процесса (design.md, Решение 2). Редкие транзиентные сбои,
		// разделённые длинной успешной работой, не суммируются и не убивают роль.
		if s.healthyThreshold > 0 && s.now().Sub(started) >= s.healthyThreshold {
			attempt = 0
			budgetStart = time.Time{}
		}
		attempt++
		if budgetStart.IsZero() {
			budgetStart = s.now()
		}
		if s.restartExhausted(attempt, budgetStart) {
			s.logger.Error("бюджет перезапусков роли исчерпан, супервизор завершается", err, observ.Fields{},
				append([]any{"process", s.process, "service", s.service, "role", s.role, "attempt", attempt}, panicStackAttr(err)...)...)
			return fmt.Errorf("супервизор роли %s/%s/%s: бюджет перезапусков исчерпан: %w", s.process, s.service, s.role, err)
		}
		delay := s.backoff(attempt)
		s.logRestart(err, attempt, delay)
		if !s.sleep(ctx, delay) {
			return nil
		}
	}
}

// runOnce исполняет роль и перехватывает панику на границе роли: паника
// превращается в ошибку rolePanicError с трассировкой стека и идёт в общий путь
// backoff-рестарта, поэтому процесс и соседние роли не затрагиваются (design.md,
// Решение 1). Сама паника здесь не логируется — это делает logRestart одним
// сообщением, чтобы не дублировать запись о панике.
func (s roleSupervisor) runOnce(ctx context.Context, runner composition.RoleRunner) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = &rolePanicError{
				process: s.process,
				service: s.service,
				role:    s.role,
				value:   r,
				stack:   debug.Stack(),
			}
		}
	}()
	return runner.Run(ctx)
}

// logRestart логирует остановку роли с ошибкой одним сообщением. Для паники к
// записи добавляется трассировка стека, чтобы не дублировать логирование паники
// (ранее она писалась отдельно в runOnce и повторно при рестарте).
func (s roleSupervisor) logRestart(err error, attempt int, delay time.Duration) {
	attrs := []any{
		"process", s.process, "service", s.service, "role", s.role,
		"attempt", attempt, "backoff", delay.String(),
	}
	attrs = append(attrs, panicStackAttr(err)...)
	s.logger.Error("роль остановлена с ошибкой, перезапуск", err, observ.Fields{}, attrs...)
}

// panicStackAttr возвращает атрибут "stack" с трассировкой стека, если err — это
// паника роли (rolePanicError), иначе пустой набор атрибутов. Общий для logRestart
// и ветки исчерпания бюджета: стек логируется всегда, в том числе когда паника
// происходит на попытке, исчерпывающей бюджет.
func panicStackAttr(err error) []any {
	if pe, ok := err.(*rolePanicError); ok {
		return []any{"stack", string(pe.stack)}
	}
	return nil
}

// rolePanicError описывает панику роли с трассировкой стека. Ошибка несёт причину
// паники в тексте (для Error-атрибута лога), а трассировка — отдельным полем.
type rolePanicError struct {
	process string
	service string
	role    composition.Role
	value   any
	stack   []byte
}

func (e *rolePanicError) Error() string {
	return fmt.Sprintf("паника роли %s/%s/%s: %v", e.process, e.service, e.role, e.value)
}

// unexpectedExitError описывает завершение роли при живом контексте.
func (s roleSupervisor) unexpectedExitError() error {
	return fmt.Errorf("роль %s/%s/%s завершилась при активном процессе", s.process, s.service, s.role)
}

// restartExhausted сообщает, исчерпан ли бюджет перезапуска: по числу попыток или
// по накопленному времени с первой ошибки.
func (s roleSupervisor) restartExhausted(attempt int, budgetStart time.Time) bool {
	if s.limits.maxAttempts > 0 && attempt >= s.limits.maxAttempts {
		return true
	}
	if s.limits.budget > 0 && !budgetStart.IsZero() && s.now().Sub(budgetStart) >= s.limits.budget {
		return true
	}
	return false
}

// backoff возвращает задержку перед попыткой attempt (нумерация с 1):
// экспонента от initialBackoff с потолком maxBackoff и jitter в [d/2, d].
func (s roleSupervisor) backoff(attempt int) time.Duration {
	initial := s.initialBackoff
	if initial <= 0 {
		initial = roleRestartInitialBackoff
	}
	limit := s.maxBackoff
	if limit <= 0 {
		limit = roleRestartMaxBackoff
	}
	if initial > limit {
		initial = limit
	}
	d := initial
	for i := 1; i < attempt; i++ {
		if d >= limit/2 {
			d = limit
			break
		}
		d *= 2
	}
	if d > limit {
		d = limit
	}
	half := d / 2
	if half <= 0 {
		return d
	}
	// Полный jitter: равномерно в [half, d], потолок не превышается.
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// sleepContext ожидает d или отмену ctx; возвращает false, если контекст отменён.
func sleepContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isServerRole сообщает, является ли роль серверной точкой входа (api, ingest):
// завершение при живом контексте эскалируется наблюдением, а не перезапуском.
func isServerRole(role composition.Role) bool {
	return role == composition.RoleAPI || role == composition.RoleIngest
}

// startRoles запускает супервизор на каждую выбранную роль выбранного сервиса.
// Незарегистрированная роль не пропускается молча: о ней логируется честное
// предупреждение. Ошибка исполнения роли в пределах бюджета перезапускается
// супервизором; исчерпание бюджета доводится до вызывающего через failures.
func startRoles(
	ctx context.Context,
	wg *sync.WaitGroup,
	sel selectedService,
	service composition.Service,
	logger observ.Logger,
	metrics observ.Metrics,
	limits restartLimits,
	failures chan<- error,
) {
	for _, role := range sel.Roles {
		runner, ok := service.RoleRunner(role)
		if !ok {
			logger.Warn("роль не зарегистрирована у сервиса", observ.Fields{},
				"process", sel.Process, "service", sel.ID, "role", role)
			continue
		}
		supervisor := newRoleSupervisor(logger, metrics, sel.Process, sel.ID, role, limits)
		wg.Add(1)
		go func(runner composition.RoleRunner, supervisor roleSupervisor) {
			defer wg.Done()
			if err := supervisor.run(ctx, runner); err != nil {
				reportFailure(failures, err)
			}
		}(runner, supervisor)
	}
}

// reportFailure неблокирующе доводит ошибку супервизора до вызывающего: первый
// отказ важен, последующие избыточны. nil-канал (тесты без надзора) игнорируется.
func reportFailure(failures chan<- error, err error) {
	if failures == nil {
		return
	}
	select {
	case failures <- err:
	default:
	}
}

// startSelectedServices собирает сервисы и запускает их роли под супервизором.
// Ошибка сборки сервиса возвращается вызывающему (фатальна в main, §1.4 AGENTS.md)
// и SHALL NOT превращаться в перезапуск: супервизор оборачивает только исполнение
// роли, а не её сборку. Исчерпание бюджета перезапуска роли доводится до
// вызывающего асинхронно через failures.
func startSelectedServices(
	ctx context.Context,
	wg *sync.WaitGroup,
	selected []selectedService,
	build func(selectedService) (composition.Service, error),
	logger observ.Logger,
	metrics observ.Metrics,
	limits restartLimits,
	failures chan<- error,
) error {
	for _, sel := range selected {
		service, err := build(sel)
		if err != nil {
			return fmt.Errorf("сборка сервиса %s: %w", sel.ID, err)
		}
		startRoles(ctx, wg, sel, service, logger, metrics, limits, failures)
	}
	return nil
}
