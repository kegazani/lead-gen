// Команда public-edge — публичный веб-контур платформы (design.md, Решение 4).
//
// Отдельный процесс и отдельный образ на своём домене: сгенерированные лендинги
// и презентации, форма обратной связи, трекинг-редиректы. Служебный образ
// cmd/lead-gen публичный контур не обслуживает.
//
// Зависимости edge: стандартная библиотека, входящие порты (internal/ports,
// поверх published-контрактов lead-gen/contracts/<owner>), точка выбора
// транспорта (internal/assembly) и HTTP-слой (internal/web). Драйверов БД,
// клиента Temporal и брокера здесь нет и быть не может — это внешняя поверхность
// атаки, и её состав зависимостей прямо ограничен (design.md, Решение 4;
// tools/importlint, правило для lead-gen/frontend/public/...).
//
// Аутентификации и сессии оператора нет: посетитель публичной страницы
// анонимен, тенанта у него нет, и различать операторов здесь нечего.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lead-gen/frontend/public/internal/adapters/forwarder"
	"lead-gen/frontend/public/internal/adapters/stub"
	"lead-gen/frontend/public/internal/assembly"
	"lead-gen/frontend/public/internal/ports"
	"lead-gen/frontend/public/internal/web"
)

// Бюджеты сервера. Публичный контур отвечает внешним клиентам, поэтому
// неограниченное чтение заголовков и бесконечное завершение недопустимы.
const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func main() {
	address := flag.String("addr", ":8080", "адрес прослушивания публичного edge")
	defaultLanding := flag.String("landing", "demo",
		"ссылка на лендинг, отдаваемый по корню домена; пустая строка — корень отвечает 404")
	transportFlag := flag.String("transport", string(assembly.TransportInProcess),
		"транспорт связей edge с владельцами: in-process (локальный прогон и тесты) "+
			"или http-json (сетевой; пока не реализован)")
	receiversFlag := flag.String("submission-receivers", "",
		"приёмники заявок клиентов через запятую в виде form=url; форма без своего "+
			"приёмника идёт в транзитный приёмник")
	transitFlag := flag.String("submission-transit", "",
		"транзитный приёмник заявок для клиентов без своего; пусто — нет транзитного контура")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	transport, err := assembly.ParseTransport(*transportFlag)
	if err != nil {
		logger.Error("public-edge: не разобран транспорт", "error", err)
		os.Exit(1)
	}
	submissions, err := submissionSink(*receiversFlag, *transitFlag, logger)
	if err != nil {
		logger.Error("public-edge: не собраны приёмники заявок", "error", err)
		os.Exit(1)
	}
	if err := run(*address, ports.PageRef(*defaultLanding), transport, submissions, logger); err != nil {
		logger.Error("public-edge: остановлен с ошибкой", "error", err)
		os.Exit(1)
	}
}

// run собирает обработчик, поднимает сервер и завершает его по сигналу.
func run(address string, defaultLanding ports.PageRef, transport assembly.Transport, submissions ports.SubmissionSink, logger *slog.Logger) error {
	handler, err := newHandler(defaultLanding, transport, submissions, logger)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// Остановка по сигналу: контейнер получает SIGTERM и должен закрыть
	// соединения, а не оборвать их.
	stop, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	failed := make(chan error, 1)
	go func() {
		logger.Info("public-edge: слушает", "addr", address, "transport", transport)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			failed <- err
			return
		}
		failed <- nil
	}()

	select {
	case err := <-failed:
		return err
	case <-stop.Done():
		logger.Info("public-edge: получен сигнал остановки")
	}

	shutdown, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("public-edge: остановка сервера: %w", err)
	}
	return nil
}

// newHandler собирает HTTP-адаптер публичных страниц и связывает его входящие
// порты с владельцами контрактов выбранным транспортом.
//
// # Точка связывания контрактов
//
// Входящие порты edge держат типы published-контрактов владельцев
// (internal/ports): rendered-artifact контекста content-production для наполнения
// лендинга и презентации, form-submission контекста outreach-orchestration для
// приёма заявки, read-signal контекста engagement-observation для разрешения
// трекинг-перехода. Здесь, и только здесь, выбирается транспорт связи — прямой
// вызов при co-location или сетевой клиент при разделении сервисов
// (internal/assembly). Ни HTTP-слой (internal/web), ни входящие порты при смене
// транспорта не меняются.
//
// Пока сетевой адаптер не реализован, in-process ветвь связывается с
// заглушечными in-memory реализациями (internal/adapters/stub) — это локальный
// прогон и тесты, а не рабочий транспорт. Запуск с -transport=http-json честно
// завершается ошибкой сборки, а не молча подставляет заглушку: см.
// internal/assembly.
func newHandler(defaultLanding ports.PageRef, transport assembly.Transport, submissions ports.SubmissionSink, logger *slog.Logger) (http.Handler, error) {
	landings, err := assembly.LinkLandingSource(transport, stub.NewLandingSource())
	if err != nil {
		return nil, err
	}
	presentations, err := assembly.LinkPresentationSource(transport, stub.NewPresentationSource())
	if err != nil {
		return nil, err
	}
	linkedSubmissions, err := assembly.LinkSubmissionSink(transport, submissions)
	if err != nil {
		return nil, err
	}
	tracking, err := assembly.LinkTrackingResolver(transport, stub.NewTrackingResolver())
	if err != nil {
		return nil, err
	}

	return web.New(web.Config{
		DefaultLanding: defaultLanding,
		Landings:       landings,
		Presentations:  presentations,
		Submissions:    linkedSubmissions,
		Tracking:       tracking,
		Logger:         logger,
	})
}

// submissionSink собирает приёмник заявок публичного edge. Заданные приёмники
// клиентов (`form=url`) и транзитный приёмник дают реальную пересылку без
// сохранения PII; без конфигурации остаётся dev-заглушка: продуктовый путь
// заявок не выдан за рабочий, а явно помечен предупреждением.
func submissionSink(receivers, transit string, logger *slog.Logger) (ports.SubmissionSink, error) {
	if strings.TrimSpace(receivers) == "" && strings.TrimSpace(transit) == "" {
		logger.Warn("public-edge: приёмники заявок не сконфигурированы (submission-receivers/submission-transit пусты), форма на dev-заглушке (dev-режим)")
		return stub.NewSubmissionSink(), nil
	}
	targets, err := parseReceivers(receivers)
	if err != nil {
		return nil, err
	}
	return forwarder.New(forwarder.Config{Receivers: targets, Transit: transit})
}

// parseReceivers разбирает список приёмников `form=url` через запятую. Пустые
// элементы пропускаются; элемент без разделителя или с пустыми частями — ошибка
// сборки, а не форма, которая молча не отправляется.
func parseReceivers(raw string) (map[ports.FormRef]string, error) {
	targets := make(map[ports.FormRef]string)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		form, target, ok := strings.Cut(entry, "=")
		form = strings.TrimSpace(form)
		target = strings.TrimSpace(target)
		if !ok || form == "" || target == "" {
			return nil, fmt.Errorf("приёмник заявок %q: ожидался формат form=url", entry)
		}
		targets[ports.FormRef(form)] = target
	}
	return targets, nil
}
