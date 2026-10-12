package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

// instantSleep — подмена ожидания backoff в тестах: не тратит реальное время,
// но уважает отмену контекста.
func instantSleep(ctx context.Context, _ time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

// testLimits — бюджет перезапуска для тестов: достаточно попыток для проверки
// рестарта, но конечный, чтобы тесты не крутились вечно.
func testLimits() restartLimits {
	return restartLimits{maxAttempts: 5, budget: time.Minute}
}

// waitDone ждёт завершения группы горутин с диагностическим таймаутом.
func waitDone(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("супервизор не завершился после отмены контекста")
	}
}

// fakeService — сервис с подставными раннерами ролей.
type fakeService struct {
	id    string
	roles map[composition.Role]composition.RoleRunner
}

func (f fakeService) ID() string { return f.id }

func (f fakeService) RoleRunner(role composition.Role) (composition.RoleRunner, bool) {
	runner, ok := f.roles[role]
	return runner, ok
}

// TestRoleSupervisorRestartsAfterError проверяет, что транзиентная ошибка роли
// (например, обрыв соединения с БД) не завершает процесс: супервизор поднимает
// роль снова и продолжает до отмены контекста.
func TestRoleSupervisorRestartsAfterError(t *testing.T) {
	var calls atomic.Int32
	restarted := make(chan struct{}, 1)

	runner := composition.RoleFunc(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("terminating connection due to administrator command (SQLSTATE 57P01)")
		}
		restarted <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, testLimits())
	sup.sleep = instantSleep

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		sup.run(ctx, runner)
	}()

	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("роль не перезапущена после ошибки")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("число запусков роли = %d, ожидалось 2", got)
	}

	cancel()
	waitDone(t, &wg)
	if got := calls.Load(); got != 2 {
		t.Fatalf("после отмены роль перезапускалась: запусков %d", got)
	}
}

// TestRoleSupervisorStopsOnContextCancel проверяет, что плановый останов по
// отмене контекста не считается падением и не перезапускает роль — независимо
// от того, вернула роль nil или ошибку отмены.
func TestRoleSupervisorStopsOnContextCancel(t *testing.T) {
	cases := []struct {
		name string
		run  func(ctx context.Context) error
	}{
		{
			name: "штатное завершение nil",
			run: func(ctx context.Context) error {
				<-ctx.Done()
				return nil
			},
		},
		{
			name: "ошибка отмены контекста",
			run: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			started := make(chan struct{}, 1)
			runner := composition.RoleFunc(func(ctx context.Context) error {
				calls.Add(1)
				started <- struct{}{}
				return tc.run(ctx)
			})

			sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "message-delivery", composition.RoleSender, testLimits())
			sup.sleep = instantSleep

			ctx, cancel := context.WithCancel(context.Background())
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				sup.run(ctx, runner)
			}()

			// Дожидаемся первого запуска роли: отмена до старта — тоже корректный
			// останов, но тогда проверка «роль не перезапускалась» была бы вырожденной.
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("роль не запущена")
			}

			cancel()
			waitDone(t, &wg)
			if got := calls.Load(); got != 1 {
				t.Fatalf("после отмены ctx роль запускалась %d раз, ожидался 1", got)
			}
		})
	}
}

// TestStartSelectedServicesReturnsBuildError проверяет, что ошибка сборки
// сервиса не глотается супервизором: она возвращается вызывающему (в main —
// фатальный os.Exit), а не превращается в перезапуск роли.
func TestStartSelectedServicesReturnsBuildError(t *testing.T) {
	want := errors.New("сборка: нет соединения с БД")
	build := func(selectedService) (composition.Service, error) {
		return nil, want
	}

	err := startSelectedServices(
		context.Background(),
		&sync.WaitGroup{},
		[]selectedService{{Process: "core", ID: "lead-management", Roles: []composition.Role{composition.RoleWorker}}},
		build,
		observ.NewLogger(),
		observ.NewMetrics(),
		testLimits(),
		nil,
	)
	if !errors.Is(err, want) {
		t.Fatalf("ошибка сборки не проброшена: err = %v", err)
	}
}

// TestStartSelectedServicesRunsRolesUnderSupervisor проверяет проводку: роли
// выбранного сервиса запускаются, переживают ошибку и завершаются по отмене.
func TestStartSelectedServicesRunsRolesUnderSupervisor(t *testing.T) {
	var calls atomic.Int32
	restarted := make(chan struct{}, 1)
	runner := composition.RoleFunc(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			return errors.New("временный сбой БД")
		}
		restarted <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})
	build := func(sel selectedService) (composition.Service, error) {
		return fakeService{id: sel.ID, roles: map[composition.Role]composition.RoleRunner{
			composition.RoleWorker: runner,
		}}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	sel := []selectedService{{Process: "demand", ID: "request-fulfillment", Roles: []composition.Role{composition.RoleWorker}}}
	if err := startSelectedServices(ctx, &wg, sel, build, observ.NewLogger(), observ.NewMetrics(), testLimits(), nil); err != nil {
		t.Fatalf("запуск сервисов: %v", err)
	}

	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("роль не перезапущена под супервизором startRoles")
	}

	cancel()
	waitDone(t, &wg)
	if got := calls.Load(); got != 2 {
		t.Fatalf("запусков роли = %d, ожидалось 2", got)
	}
}

// TestRoleSupervisorBackoffGrowsAndCaps проверяет экспоненциальный рост задержки
// с jitter и непревышение потолка.
func TestRoleSupervisorBackoffGrowsAndCaps(t *testing.T) {
	sup := roleSupervisor{initialBackoff: 500 * time.Millisecond, maxBackoff: 30 * time.Second}
	cases := []struct {
		attempt int
		min     time.Duration
		max     time.Duration
	}{
		{1, 250 * time.Millisecond, 500 * time.Millisecond},
		{2, 500 * time.Millisecond, time.Second},
		{3, time.Second, 2 * time.Second},
		{20, 15 * time.Second, 30 * time.Second},
	}
	for _, tc := range cases {
		got := sup.backoff(tc.attempt)
		if got < tc.min || got > tc.max {
			t.Errorf("backoff(%d) = %s, ожидался диапазон [%s, %s]", tc.attempt, got, tc.min, tc.max)
		}
	}
}

// TestRoleSupervisorRecoversPanic проверяет, что паника роли не роняет процесс:
// супервизор перехватывает её, перезапускает роль, а параллельная роль продолжает
// выполняться.
func TestRoleSupervisorRecoversPanic(t *testing.T) {
	var panics atomic.Int32
	restarted := make(chan struct{}, 1)
	panicking := composition.RoleFunc(func(ctx context.Context) error {
		if panics.Add(1) == 1 {
			panic("роль упала")
		}
		restarted <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})

	// Параллельная роль той же группы: блокируется до отмены и не падает.
	parallelAlive := make(chan struct{}, 1)
	parallel := composition.RoleFunc(func(ctx context.Context) error {
		parallelAlive <- struct{}{}
		<-ctx.Done()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup

	supPanic := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, testLimits())
	supPanic.sleep = instantSleep
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = supPanic.run(ctx, panicking)
	}()

	supParallel := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "analytics", composition.RoleWorker, testLimits())
	supParallel.sleep = instantSleep
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = supParallel.run(ctx, parallel)
	}()

	// Параллельная роль поднялась и работает.
	select {
	case <-parallelAlive:
	case <-time.After(2 * time.Second):
		t.Fatal("параллельная роль не запущена")
	}
	// Паникующая роль перезапущена после паники.
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("роль не перезапущена после паники")
	}
	if got := panics.Load(); got != 2 {
		t.Fatalf("запусков паникующей роли = %d, ожидалось 2", got)
	}

	cancel()
	waitDone(t, &wg)
}

// TestRoleSupervisorLogsPanicStack проверяет, что лог паники содержит трассировку
// стека, а не только сообщение паники.
func TestRoleSupervisorLogsPanicStack(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)

	var calls atomic.Int32
	restarted := make(chan struct{}, 1)
	runner := composition.RoleFunc(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			panic("boom-panic-marker")
		}
		restarted <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})

	sup := newRoleSupervisor(logger, observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, testLimits())
	sup.sleep = instantSleep

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = sup.run(ctx, runner)
	}()

	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("роль не перезапущена после паники")
	}
	cancel()
	waitDone(t, &wg)

	log := buf.String()
	if !strings.Contains(log, "boom-panic-marker") {
		t.Fatalf("лог паники не содержит сообщение паники: %s", log)
	}
	if !strings.Contains(log, "goroutine") {
		t.Fatalf("лог паники не содержит трассировку стека: %s", log)
	}
}

// TestRoleSupervisorStopsAfterBudget проверяет, что мгновенно падающая роль после
// исчерпания бюджета не перезапускается, а run возвращает ошибку.
func TestRoleSupervisorStopsAfterBudget(t *testing.T) {
	var calls atomic.Int32
	runner := composition.RoleFunc(func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("мгновенный сбой")
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 2, budget: time.Minute})
	sup.sleep = instantSleep

	if err := sup.run(context.Background(), runner); err == nil {
		t.Fatal("ожидалась ошибка исчерпания бюджета перезапусков")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("число запусков роли = %d, ожидалось 2 (после бюджета перезапуска нет)", got)
	}
}

// TestRoleSupervisorStopsOnTimeBudget проверяет бюджет по накопленному времени:
// при подменённых часах перезапуск прекращается, когда время с первой ошибки
// превышает бюджет, даже если лимит попыток не исчерпан.
func TestRoleSupervisorStopsOnTimeBudget(t *testing.T) {
	var calls atomic.Int32
	runner := composition.RoleFunc(func(ctx context.Context) error {
		calls.Add(1)
		return errors.New("мгновенный сбой")
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 100, budget: time.Minute})
	sup.sleep = instantSleep

	current := time.Unix(0, 0)
	sup.now = func() time.Time {
		value := current
		current = current.Add(30 * time.Second)
		return value
	}

	if err := sup.run(context.Background(), runner); err == nil {
		t.Fatal("ожидалась ошибка исчерпания бюджета времени")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("число запусков роли = %d, ожидалось 2 (бюджет времени исчерпан за два запуска)", got)
	}
}

// TestRoleSupervisorScatteredFailuresDoNotExhaustBudget проверяет, что бюджет
// перезапуска считается за «эпизод сбоя», а не за всю жизнь процесса: редкие
// транзиентные сбои, разделённые длинной успешной работой (дольше порога
// здоровья), не суммируются и не исчерпывают бюджет.
func TestRoleSupervisorScatteredFailuresDoNotExhaustBudget(t *testing.T) {
	var calls atomic.Int32
	runner := composition.RoleFunc(func(ctx context.Context) error {
		n := calls.Add(1)
		if n == 1 {
			return errors.New("транзиентный сбой на старте")
		}
		// Успешная работа дольше порога здоровья, затем редкий сбой: каждый такой
		// сбой начинает новый эпизод и не накапливается с предыдущими.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(80 * time.Millisecond):
			return errors.New("редкий сбой после успешной работы")
		}
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 2, budget: time.Minute})
	sup.sleep = instantSleep
	sup.healthyThreshold = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	runErr := make(chan error, 1)
	go func() {
		defer wg.Done()
		runErr <- sup.run(ctx, runner)
	}()

	// Ждём несколько запусков: старт-сбой + несколько успешных прогонов с редкими
	// сбоями. Бюджет (2 попытки) не должен исчерпаться, потому что каждый сбой после
	// успешной работы начинает новый эпизод.
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 5 {
		select {
		case err := <-runErr:
			t.Fatalf("бюджет исчерпан досрочно при разрозненных сбоях: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("роль совершила лишь %d запусков за отведённое время", calls.Load())
		}
		time.Sleep(10 * time.Millisecond)
	}

	// После пяти запусков бюджет по-прежнему не исчерпан: run продолжает
	// перезапускать роль, а не завершился с ошибкой.
	select {
	case err := <-runErr:
		t.Fatalf("бюджет исчерпан при разрозненных сбоях: %v", err)
	default:
	}

	cancel()
	waitDone(t, &wg)
	if got := calls.Load(); got < 5 {
		t.Fatalf("роль совершила лишь %d запусков, ожидалось не меньше 5", got)
	}
}

// TestRoleSupervisorRestartsNilOnLiveContext проверяет, что роль-цикл, вернувшая
// nil при живом контексте, трактуется как сбой и перезапускается с backoff.
func TestRoleSupervisorRestartsNilOnLiveContext(t *testing.T) {
	var calls atomic.Int32
	restarted := make(chan struct{}, 1)
	runner := composition.RoleFunc(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			return nil // цикл завершился при живом контексте — сбой
		}
		restarted <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, testLimits())
	sup.sleep = instantSleep

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = sup.run(ctx, runner)
	}()

	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("роль не перезапущена после завершения при живом контексте")
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("запусков роли = %d, ожидалось 2", got)
	}

	cancel()
	waitDone(t, &wg)
}

// TestRoleSupervisorEscalatesAPIExit проверяет, что серверная роль api, вернувшая
// nil при живом контексте, эскалируется наблюдением: счётчик метрики увеличился,
// в лог записан Error, а не тихая остановка.
func TestRoleSupervisorEscalatesAPIExit(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	memMetrics := observ.NewMetrics().(*observ.MemoryMetrics)

	exited := make(chan struct{}, 1)
	runner := composition.RoleFunc(func(ctx context.Context) error {
		exited <- struct{}{}
		return nil // api-сервер завершился при живом контексте
	})

	sup := newRoleSupervisor(logger, memMetrics, "core", "analytics", composition.RoleAPI, testLimits())
	sup.sleep = instantSleep

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = sup.run(context.Background(), runner)
	}()

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("роль api не завершилась")
	}
	waitDone(t, &wg)

	if got := memMetrics.Counter(metricRoleExitLive, map[string]string{"service": "analytics", "role": "api"}); got != 1 {
		t.Fatalf("счётчик метрики = %d, ожидался 1", got)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("лог не содержит запись Error: %s", buf.String())
	}
}

// TestRoleSupervisorEscalatesIngestExit проверяет сценарий спеки «серверная роль
// завершилась при живом контексте» для второй названной спеки серверной роли —
// ingest: возврат nil при живом ctx эскалируется метрикой и Error-логом, а не
// теряется молча.
func TestRoleSupervisorEscalatesIngestExit(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	memMetrics := observ.NewMetrics().(*observ.MemoryMetrics)

	exited := make(chan struct{}, 1)
	runner := composition.RoleFunc(func(ctx context.Context) error {
		exited <- struct{}{}
		return nil // ingest-сервер завершился при живом контексте
	})

	sup := newRoleSupervisor(logger, memMetrics, "public", "engagement-observation", composition.RoleIngest, testLimits())
	sup.sleep = instantSleep

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = sup.run(context.Background(), runner)
	}()

	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("роль ingest не завершилась")
	}
	waitDone(t, &wg)

	if got := memMetrics.Counter(metricRoleExitLive, map[string]string{"service": "engagement-observation", "role": "ingest"}); got != 1 {
		t.Fatalf("счётчик метрики = %d, ожидался 1", got)
	}
	if !strings.Contains(buf.String(), `"level":"ERROR"`) {
		t.Fatalf("лог не содержит запись Error: %s", buf.String())
	}
}

// TestStartSelectedServicesPropagatesBudgetError проверяет, что исчерпание бюджета
// перезапуска роли доводится до вызывающего через failures (в main — фатальный
// os.Exit), а не глотается.
func TestStartSelectedServicesPropagatesBudgetError(t *testing.T) {
	runner := composition.RoleFunc(func(ctx context.Context) error {
		return errors.New("мгновенный сбой")
	})
	build := func(sel selectedService) (composition.Service, error) {
		return fakeService{id: sel.ID, roles: map[composition.Role]composition.RoleRunner{
			composition.RoleWorker: runner,
		}}, nil
	}

	failures := make(chan error, 1)
	var wg sync.WaitGroup
	sel := []selectedService{{Process: "core", ID: "lead-management", Roles: []composition.Role{composition.RoleWorker}}}
	if err := startSelectedServices(
		context.Background(),
		&wg,
		sel,
		build,
		observ.NewLogger(),
		observ.NewMetrics(),
		restartLimits{maxAttempts: 1, budget: time.Minute},
		failures,
	); err != nil {
		t.Fatalf("запуск сервисов: %v", err)
	}

	select {
	case err := <-failures:
		if err == nil {
			t.Fatal("ожидалась ошибка бюджета, получен nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ошибка бюджета не доведена до вызывающего")
	}
}

// TestRoleSupervisorRestartsNilForCycleRoles проверяет сценарий спеки «роль-цикл
// завершилась при живом контексте» для всех ролей-циклов, названных спекой:
// worker, chains, schedule, sender. Возврат nil не является штатным завершением.
func TestRoleSupervisorRestartsNilForCycleRoles(t *testing.T) {
	roles := []composition.Role{
		composition.RoleWorker,
		composition.RoleChains,
		composition.RoleSchedule,
		composition.RoleSender,
	}
	for _, role := range roles {
		t.Run(string(role), func(t *testing.T) {
			var calls atomic.Int32
			restarted := make(chan struct{}, 1)
			runner := composition.RoleFunc(func(ctx context.Context) error {
				if calls.Add(1) == 1 {
					return nil // цикл завершился при живом ctx — сбой
				}
				restarted <- struct{}{}
				<-ctx.Done()
				return ctx.Err()
			})

			sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "message-delivery", role, testLimits())
			sup.sleep = instantSleep

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				_ = sup.run(ctx, runner)
			}()

			select {
			case <-restarted:
			case <-time.After(2 * time.Second):
				t.Fatalf("роль %s не перезапущена после nil при живом контексте", role)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("роль %s запускалась %d раз, ожидалось 2", role, got)
			}

			cancel()
			waitDone(t, &wg)
		})
	}
}

// TestRoleSupervisorPanicExhaustsBudgetNeighborStaysAlive — крайний случай:
// паникующая роль исчерпывает бюджет перезапусков, run возвращает ошибку (сигнал
// к завершению процесса), а соседняя роль той же группы продолжает работу и не
// затрагивается.
func TestRoleSupervisorPanicExhaustsBudgetNeighborStaysAlive(t *testing.T) {
	var panics atomic.Int32
	panicking := composition.RoleFunc(func(context.Context) error {
		panics.Add(1)
		panic("boom-budget")
	})

	neighborAlive := make(chan struct{}, 1)
	neighbor := composition.RoleFunc(func(ctx context.Context) error {
		neighborAlive <- struct{}{}
		<-ctx.Done()
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup

	supPanic := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 2, budget: time.Minute})
	supPanic.sleep = instantSleep
	failure := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		failure <- supPanic.run(ctx, panicking)
	}()

	supNeighbor := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "analytics", composition.RoleWorker, testLimits())
	supNeighbor.sleep = instantSleep
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = supNeighbor.run(ctx, neighbor)
	}()

	select {
	case <-neighborAlive:
	case <-time.After(2 * time.Second):
		t.Fatal("соседняя роль не запущена")
	}

	select {
	case err := <-failure:
		if err == nil {
			t.Fatal("исчерпание бюджета паникующей роли не вернуло ошибку")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("супервизор паникующей роли не завершился по исчерпании бюджета")
	}
	if got := panics.Load(); got != 2 {
		t.Fatalf("паникующая роль запускалась %d раз, ожидалось 2", got)
	}
	// Соседняя роль жива: её контекст не отменён, она по-прежнему заблокирована в
	// <-ctx.Done(), а не завершилась вместе с паникующей.
	if ctx.Err() != nil {
		t.Fatal("контекст группы отменён из-за паники одной роли")
	}

	cancel()
	waitDone(t, &wg)
}

// TestRoleSupervisorPanicExhaustsBudgetLogsStack проверяет, что стек паники
// логируется и тогда, когда паника происходит на попытке, исчерпывающей бюджет:
// при maxAttempts=1 перезапуска нет, а трассировка попадает в запись исчерпания
// бюджета, а не только в запись рестарта.
func TestRoleSupervisorPanicExhaustsBudgetLogsStack(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)

	runner := composition.RoleFunc(func(context.Context) error {
		panic("boom-exhaust-stack")
	})

	sup := newRoleSupervisor(logger, observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 1, budget: time.Minute})
	sup.sleep = instantSleep

	if err := sup.run(context.Background(), runner); err == nil {
		t.Fatal("ожидалась ошибка исчерпания бюджета перезапусков")
	}

	log := buf.String()
	if !strings.Contains(log, "boom-exhaust-stack") {
		t.Fatalf("лог исчерпания бюджета не содержит сообщение паники: %s", log)
	}
	if !strings.Contains(log, "goroutine") {
		t.Fatalf("лог исчерпания бюджета не содержит трассировку стека: %s", log)
	}
}

// TestRoleSupervisorBudgetErrorWrapsCause проверяет, что при исчерпании бюджета
// (лимит попыток равен 1) run сразу возвращает ошибку, оборачивающую исходную
// причину, — сигнал, который main доводит до ненулевого кода выхода.
func TestRoleSupervisorBudgetErrorWrapsCause(t *testing.T) {
	cause := errors.New("сбой-маркер-источника")
	runner := composition.RoleFunc(func(context.Context) error {
		return cause
	})

	sup := newRoleSupervisor(observ.NewLogger(), observ.NewMetrics(), "core", "lead-management", composition.RoleWorker, restartLimits{maxAttempts: 1, budget: time.Minute})
	sup.sleep = instantSleep

	err := sup.run(context.Background(), runner)
	if err == nil {
		t.Fatal("ожидалась ошибка исчерпания бюджета")
	}
	if !errors.Is(err, cause) {
		t.Fatalf("ошибка бюджета не оборачивает исходную причину: %v", err)
	}
	if !strings.Contains(err.Error(), "бюджет перезапусков исчерпан") {
		t.Fatalf("ошибка не называет исчерпание бюджета: %v", err)
	}
}
