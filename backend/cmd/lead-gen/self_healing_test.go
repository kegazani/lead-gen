package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

func TestSelfLivenessUsesReachableAddressForWildcardListener(t *testing.T) {
	for address, want := range map[string]string{
		"0.0.0.0:19090":   "http://127.0.0.1:19090/livez",
		"[::]:19090":      "http://[::1]:19090/livez",
		":19090":          "http://127.0.0.1:19090/livez",
		"127.0.0.1:19090": "http://127.0.0.1:19090/livez",
		"[::1]:19090":     "http://[::1]:19090/livez",
	} {
		if got := selfLivenessURL(address); got != want {
			t.Errorf("адрес слушателя %s: %s, ожидался %s", address, got, want)
		}
	}
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/livez" {
			t.Errorf("неверный путь самопроверки: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	defer server.Close()
	if !probeLiveness(context.Background(), selfLivenessURL(listener.Addr().String()), time.Second) {
		t.Fatal("действующий wildcard listener недоступен собственной loopback-пробе")
	}
}

// TestLivenessWatchdogTerminatesOnSustainedLoss проверяет (а): при устойчивой
// потере liveness (threshold подряд неудач) сторож инициирует завершение —
// возвращает ошибку-причину, а не крутится дальше.
func TestLivenessWatchdogTerminatesOnSustainedLoss(t *testing.T) {
	w := livenessWatchdog{
		url:       "http://127.0.0.1:1/livez",
		interval:  time.Millisecond,
		threshold: 3,
		probe:     func(ctx context.Context) bool { return false },
		sleep:     func(ctx context.Context, d time.Duration) bool { return true },
	}
	if err := w.run(context.Background()); err == nil {
		t.Fatal("сторож должен был инициировать завершение при устойчивой потере liveness")
	}
}

// TestLivenessWatchdogHealthyDoesNotTerminate проверяет (б): здоровый liveness не
// приводит к завершению — сторож делает несколько успешных проверок и завершается
// только по отмене контекста (sleep возвращает false), не вернув ошибку.
func TestLivenessWatchdogHealthyDoesNotTerminate(t *testing.T) {
	probes := 0
	w := livenessWatchdog{
		url:       "http://127.0.0.1:1/livez",
		interval:  time.Millisecond,
		threshold: 3,
		probe: func(ctx context.Context) bool {
			probes++
			return true
		},
		sleep: func(ctx context.Context, d time.Duration) bool {
			// Пять успешных проверок, затем отмена контекста.
			return probes < 5
		},
	}
	if err := w.run(context.Background()); err != nil {
		t.Fatalf("здоровый liveness не должен приводить к завершению: %v", err)
	}
	if probes < 5 {
		t.Fatalf("сторож сделал слишком мало проверок: %d", probes)
	}
}

// TestLivenessWatchdogThresholdProtectsAgainstSingleFailure проверяет (в): порог
// N > 1 защищает от единичного сбоя — один неуспешный самопросмотр при последующих
// успешных не приводит к завершению.
func TestLivenessWatchdogThresholdProtectsAgainstSingleFailure(t *testing.T) {
	results := []bool{false, true, true, true}
	probeCalls := 0
	sleepCalls := 0
	w := livenessWatchdog{
		url:       "http://127.0.0.1:1/livez",
		interval:  time.Millisecond,
		threshold: 3,
		probe: func(ctx context.Context) bool {
			idx := probeCalls
			if idx >= len(results) {
				idx = len(results) - 1
			}
			probeCalls++
			return results[idx]
		},
		sleep: func(ctx context.Context, d time.Duration) bool {
			sleepCalls++
			// Проверок — по числу исходов; затем отмена контекста.
			return sleepCalls <= len(results)
		},
	}
	if err := w.run(context.Background()); err != nil {
		t.Fatalf("единичный сбой при пороге 3 не должен приводить к завершению: %v", err)
	}
	if probeCalls < len(results) {
		t.Fatalf("сторож сделал слишком мало проверок: %d", probeCalls)
	}
}

// TestProbeLivenessLiveAndUnreachable проверяет самопроверку liveness: живой
// слушатель даёт true, недоступный адрес — false.
func TestProbeLivenessLiveAndUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer srv.Close()

	if !probeLiveness(context.Background(), srv.URL, time.Second) {
		t.Fatal("живая liveness-проба должна вернуть true")
	}
	if probeLiveness(context.Background(), "http://127.0.0.1:1/livez", 200*time.Millisecond) {
		t.Fatal("недоступная liveness-проба должна вернуть false")
	}
}

// TestStartSelfHealingDisabledWhenNoAddr проверяет, что пустой liveness-адрес
// отключает сторожа (возвращает nil-канал).
func TestStartSelfHealingDisabledWhenNoAddr(t *testing.T) {
	ch := startSelfHealing(context.Background(), nil, nil, "",
		selfHealDefaultInterval, selfHealDefaultTimeout, selfHealDefaultThreshold, selfHealDefaultStartPeriod)
	if ch != nil {
		t.Fatal("пустой liveness-адрес должен отключать сторожа")
	}
}

// TestLivenessWatchdogTerminatesExactlyAtThreshold воспроизводит крайний случай:
// ровно N подряд идущих неудач дают сигнал завершения, причём не раньше N-й
// проверки. Порог N > 1 — защита от единичного сбоя.
func TestLivenessWatchdogTerminatesExactlyAtThreshold(t *testing.T) {
	probes := 0
	w := livenessWatchdog{
		url:       "http://127.0.0.1:1/livez",
		interval:  time.Millisecond,
		threshold: 3,
		probe: func(context.Context) bool {
			probes++
			return false
		},
		sleep: func(context.Context, time.Duration) bool { return true },
	}
	err := w.run(context.Background())
	if err == nil {
		t.Fatal("три подряд идущие неудачи при пороге 3 должны инициировать завершение")
	}
	if probes != 3 {
		t.Fatalf("проверок до завершения = %d, ожидалось ровно 3 (порог)", probes)
	}
	if !strings.Contains(err.Error(), "3 раз подряд") {
		t.Fatalf("ошибка не называет число подряд идущих неудач: %v", err)
	}
}

// TestLivenessWatchdogRecoversBeforeThreshold воспроизводит крайний случай
// восстановления до порога: две неудачи, затем успех (сброс счётчика), затем
// снова две неудачи при пороге 3 — суммарно четыре неудачи, но ни одна серия не
// достигает порога, поэтому сторож НЕ завершается.
func TestLivenessWatchdogRecoversBeforeThreshold(t *testing.T) {
	results := []bool{false, false, true, false, false}
	idx := 0
	w := livenessWatchdog{
		url:       "http://127.0.0.1:1/livez",
		interval:  time.Millisecond,
		threshold: 3,
		probe: func(context.Context) bool {
			if idx >= len(results) {
				return true
			}
			r := results[idx]
			idx++
			return r
		},
		sleep: func(context.Context, time.Duration) bool {
			// Исходы кончились — отмена контекста.
			return idx < len(results)
		},
	}
	if err := w.run(context.Background()); err != nil {
		t.Fatalf("восстановление до порога не должно приводить к завершению: %v", err)
	}
	if idx != len(results) {
		t.Fatalf("сторож сделал %d проверок, ожидалось %d", idx, len(results))
	}
}

// TestStartSelfHealingSignalsAndTracksMetric проверяет наблюдаемый путь
// самозавершения целиком (сценарий спеки «Устойчивая потеря liveness-контракта
// ведёт к самозавершению»): сторож по недоступной собственной пробе доводит
// причину до канала, инкрементит метрику process_self_terminated_total и
// логирует причину.
func TestStartSelfHealingSignalsAndTracksMetric(t *testing.T) {
	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	memMetrics := observ.NewMetrics().(*observ.MemoryMetrics)

	// Адрес с заведомо недоступной пробой: самопроверка не проходит.
	ch := startSelfHealing(context.Background(), logger, memMetrics,
		"127.0.0.1:1", time.Millisecond, 50*time.Millisecond, 2, 0)
	if ch == nil {
		t.Fatal("сторож должен быть включён при заданном адресе и пороге")
	}

	select {
	case err := <-ch:
		if err == nil {
			t.Fatal("при устойчивой потере liveness ожидалась ошибка-причина")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("сторож не довёл самозавершение до сигнала")
	}

	if got := memMetrics.Counter(metricSelfTerminated, nil); got != 1 {
		t.Fatalf("счётчик %s = %d, ожидался 1", metricSelfTerminated, got)
	}
	if !strings.Contains(buf.String(), "самозаверша") {
		t.Fatalf("лог не содержит причину самозавершения: %s", buf.String())
	}
}

// TestStartSelfHealingDisabledOnNonPositive проверяет, что неположительный
// интервал или порог отключает сторожа (нулевой канал), а не даёт бесконечный
// или мгновенный цикл самопроверок.
func TestStartSelfHealingDisabledOnNonPositive(t *testing.T) {
	cases := []struct {
		name      string
		interval  time.Duration
		threshold int
	}{
		{"нулевой порог", time.Millisecond, 0},
		{"отрицательный порог", time.Millisecond, -1},
		{"нулевой интервал", 0, 3},
		{"отрицательный интервал", -time.Second, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ch := startSelfHealing(context.Background(), nil, nil, "127.0.0.1:8091",
				tc.interval, time.Second, tc.threshold, 0)
			if ch != nil {
				t.Fatal("неположительный интервал или порог должен отключать сторожа")
			}
		})
	}
}

// TestStartSelfHealingClampsNonPositiveTimeout проверяет footgun неположительного
// таймаута пробы: timeout <= 0 не отключает сторожа (канал не nil) и не ведёт к
// самозавершению при живом liveness. Таймаут клампится к дефолту, а не даёт
// мгновенно отменённый контекст каждой пробе (иначе каждая проба проваливалась бы
// и сторож самозавершал процесс).
func TestStartSelfHealingClampsNonPositiveTimeout(t *testing.T) {
	addr := freeListenAddr(t)
	srv, err := startLivenessListener(addr, observ.NewLogger())
	if err != nil {
		t.Fatalf("liveness-слушатель не поднят: %v", err)
	}
	defer func() { _ = srv.Close() }()
	waitLiveness(t, "http://"+addr+"/livez")

	var buf bytes.Buffer
	logger := observ.NewLoggerWithWriter(&buf)
	ch := startSelfHealing(context.Background(), logger, nil, addr, time.Millisecond, 0, 2, 0)
	if ch == nil {
		t.Fatal("timeout <= 0 не должен отключать сторожа: таймаут клампится к дефолту")
	}
	select {
	case err := <-ch:
		t.Fatalf("живой liveness при timeout=0 не должен вести к самозавершению: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
}

// TestLivenessWatchdogStartPeriodDelaysFirstProbe проверяет стартовый период
// сторожа: первое ожидание равно startPeriod, и первая самопроверка происходит
// только после него (допуск на медленный старт процесса).
func TestLivenessWatchdogStartPeriodDelaysFirstProbe(t *testing.T) {
	var sleeps []time.Duration
	probes := 0
	w := livenessWatchdog{
		url:         "http://127.0.0.1:1/livez",
		interval:    time.Millisecond,
		threshold:   3,
		startPeriod: 123 * time.Millisecond,
		probe: func(context.Context) bool {
			probes++
			return true
		},
		sleep: func(_ context.Context, d time.Duration) bool {
			sleeps = append(sleeps, d)
			// Первое ожидание — стартовый период; после первой самопроверки
			// завершаем цикл (второе ожидание — interval).
			return len(sleeps) < 2
		},
	}
	if err := w.run(context.Background()); err != nil {
		t.Fatalf("сторож не должен завершаться: %v", err)
	}
	if len(sleeps) == 0 {
		t.Fatal("сторож не сделал ни одного ожидания")
	}
	if sleeps[0] != 123*time.Millisecond {
		t.Fatalf("первое ожидание = %v, ожидался стартовый период 123ms", sleeps[0])
	}
	if probes != 1 {
		t.Fatalf("проб до завершения = %d, ожидалась 1 (первая — после стартового периода)", probes)
	}
}
