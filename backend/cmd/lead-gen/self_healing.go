package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

// Параметры процессного сторожа по умолчанию (design.md, Вариант C). Интервал и
// порог конечны и не равны нулю: сторож включён по умолчанию, когда задан
// -liveness-addr. Значение 0 у интервала или порога отключает сторожа.
const (
	selfHealDefaultInterval    = 15 * time.Second
	selfHealDefaultTimeout     = 5 * time.Second
	selfHealDefaultThreshold   = 3
	selfHealDefaultStartPeriod = 30 * time.Second
)

// metricSelfTerminated — счётчик самозавершения процесса по устойчивой потере
// собственной liveness-пробы (design.md, Вариант C). Наблюдаемый признак
// автовосстановления вместо тихого зависания.
const metricSelfTerminated = "process_self_terminated_total"

// livenessWatchdog — процессный сторож (Вариант C): периодическая самопроверка
// собственной liveness-пробы. При устойчивой потере (threshold подряд неудач)
// сторож возвращает ошибку-причину, и вызывающий завершает процесс неуспешно,
// чтобы Docker (`restart: unless-stopped`) поднял свежий контейнер.
//
// Сторож НЕ детектирует дедлоки отдельных ролей (это Non-Goal): он проверяет
// только то, что собственный процесс жив и отвечает на собственную liveness-точку.
type livenessWatchdog struct {
	url         string
	interval    time.Duration
	threshold   int
	startPeriod time.Duration

	// probe — одна самопроверка liveness: true, если процесс жив и отвечает.
	// Таймаут пробы живёт внутри probe (замыкание в startSelfHealing), а не в
	// стороже: цикл сторожа отвечает только за интервал и порог. Подменяется в
	// тестах, чтобы не поднимать реальный слушатель.
	probe func(ctx context.Context) bool

	// sleep ожидает задержку или отмену контекста; false означает отмену.
	// Подменяется в тестах, чтобы не ждать реальные интервалы.
	sleep func(ctx context.Context, d time.Duration) bool
}

// run исполняет цикл самопроверок и возвращает nil при плановом останове (отмена
// ctx) либо ошибку-причину при устойчивой потере liveness. Стартовый период даёт
// допуск на медленный старт процесса (подключение к БД/рантайму, миграции), чтобы
// старт не был принят за зависание.
func (w livenessWatchdog) run(ctx context.Context) error {
	if w.startPeriod > 0 && !w.sleep(ctx, w.startPeriod) {
		return nil
	}
	failures := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		if w.probe(ctx) {
			failures = 0
		} else {
			failures++
			if w.threshold > 0 && failures >= w.threshold {
				return fmt.Errorf("собственная liveness-проба %s недоступна %d раз подряд", w.url, failures)
			}
		}
		if !w.sleep(ctx, w.interval) {
			return nil
		}
	}
}

// probeLiveness выполняет одну HTTP-пробу живости: GET url с таймаутом. Возвращает
// true, если процесс жив и отвечает (2xx), false иначе (недоступен, таймаут или
// не-2xx). Отмена ctx даёт false, но сторож завершается по отмене в вызывающем —
// результат пробы в этот момент значения не имеет.
func probeLiveness(ctx context.Context, url string, timeout time.Duration) bool {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
}

// startSelfHealing запускает процессный сторож и возвращает канал, по которому
// придёт причина самозавершения при устойчивой потере liveness. Возвращает nil,
// если сторож отключён (пустой liveness-адрес, нулевой интервал или порог).
//
// Самозавершение выполняется вызывающим (main): сторож только доводит решение до
// ошибки, а `os.Exit` остаётся в точке входа — так решение тестируется без выхода
// из процесса.
func startSelfHealing(
	ctx context.Context,
	logger observ.Logger,
	metrics observ.Metrics,
	livenessAddr string,
	interval, timeout time.Duration,
	threshold int,
	startPeriod time.Duration,
) <-chan error {
	if strings.TrimSpace(livenessAddr) == "" || interval <= 0 || threshold <= 0 {
		return nil
	}
	// Неположительный таймаут — footgun: context.WithTimeout(ctx, timeout<=0) даёт
	// мгновенно отменённый контекст, поэтому каждая проба проваливалась бы и сторож
	// самозавершал бы процесс при живом liveness. Таймаут — настройка одной пробы, а
	// не переключатель «вкл/выкл» (переключатели — interval и threshold), поэтому
	// неположительное значение клампится к дефолту, а не отключает сторожа.
	if timeout <= 0 {
		timeout = selfHealDefaultTimeout
	}
	url := selfLivenessURL(livenessAddr)
	w := livenessWatchdog{
		url:         url,
		interval:    interval,
		threshold:   threshold,
		startPeriod: startPeriod,
		probe:       func(ctx context.Context) bool { return probeLiveness(ctx, url, timeout) },
		sleep:       sleepContext,
	}
	ch := make(chan error, 1)
	go func() {
		if err := w.run(ctx); err != nil {
			logger.Error("lead-gen: процесс самозавершается: потеряна собственная liveness-проба", err, observ.Fields{},
				"url", url, "threshold", threshold)
			if metrics != nil {
				metrics.Incr(metricSelfTerminated, nil)
			}
			ch <- err
		}
	}()
	return ch
}

// Wildcard задаёт интерфейсы слушателя, но не адрес HTTP-клиента. Самопроверка
// обращается к loopback, чтобы адрес 0.0.0.0 не уходил во внешний HTTP proxy.
func selfLivenessURL(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err == nil {
		ip := net.ParseIP(host)
		if host == "" || (ip != nil && ip.IsUnspecified()) {
			host = "127.0.0.1"
			if ip != nil && ip.To4() == nil {
				host = "::1"
			}
			listenAddr = net.JoinHostPort(host, port)
		}
	}
	return "http://" + listenAddr + "/livez"
}
