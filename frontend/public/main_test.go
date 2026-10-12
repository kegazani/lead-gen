package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/frontend/public/internal/adapters/stub"
	"lead-gen/frontend/public/internal/assembly"
	"lead-gen/frontend/public/internal/ports"
)

// discardLogger — журнал, который никуда не пишет: тест проверяет сборку, а не
// наблюдаемость.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestNewHandlerInProcessServesRootLanding проверяет точку связывания контрактов:
// на in-process транспорте newHandler собирает обработчик и доводит настроенный
// корневой лендинг до посетителя. Это покрытие композиционного корня — сборка
// портов, выбор транспорта и передача ссылки на страницу в HTTP-слой.
func TestNewHandlerInProcessServesRootLanding(t *testing.T) {
	handler, err := newHandler(ports.PageRef("demo"), assembly.TransportInProcess, stub.NewSubmissionSink(), discardLogger())
	if err != nil {
		t.Fatalf("newHandler(in-process): неожиданная ошибка сборки: %v", err)
	}
	if handler == nil {
		t.Fatal("newHandler(in-process): обработчик не собран")
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("корневой лендинг: код ответа %d, ожидалось %d", recorder.Code, http.StatusOK)
	}
}

// TestNewHandlerEmptyRootLandingIsNotFound: пустая ссылка корня означает, что
// edge не выбирает главную страницу за владельца, и корень отвечает 404.
func TestNewHandlerEmptyRootLandingIsNotFound(t *testing.T) {
	handler, err := newHandler("", assembly.TransportInProcess, stub.NewSubmissionSink(), discardLogger())
	if err != nil {
		t.Fatalf("newHandler(in-process): неожиданная ошибка сборки: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("пустой корень: код ответа %d, ожидалось %d", recorder.Code, http.StatusNotFound)
	}
}

// TestNewHandlerNetworkTransportIsUnavailable: сетевой транспорт — точка
// расширения, адаптер не реализован. Сборка обязана честно отказать и вернуть
// nil, а не подставить заглушку с ложным успехом (AGENTS.md §3): иначе edge
// выдал бы демонстрационное наполнение за работу с владельцем.
func TestNewHandlerNetworkTransportIsUnavailable(t *testing.T) {
	handler, err := newHandler(ports.PageRef("demo"), assembly.TransportNetwork, stub.NewSubmissionSink(), discardLogger())
	if err == nil {
		t.Fatal("newHandler(http-json): ожидалась явная ошибка сборки, получен рабочий обработчик")
	}
	if handler != nil {
		t.Errorf("newHandler(http-json): при ошибке обработчик должен быть nil, получен %T", handler)
	}
}

// TestParseReceiversRejectsMalformed: элемент без формы или адреса — ошибка сборки,
// а не форма, которая молча не отправляется.
func TestParseReceiversRejectsMalformed(t *testing.T) {
	if _, err := parseReceivers("demo"); err == nil {
		t.Fatal("элемент без разделителя принят")
	}
	if _, err := parseReceivers("=https://example.com/hook"); err == nil {
		t.Fatal("элемент без формы принят")
	}
	if _, err := parseReceivers("demo="); err == nil {
		t.Fatal("элемент без адреса принят")
	}
}

// TestSubmissionSinkConfiguredUsesForwarder: при заданных приёмниках
// композиционный корень собирает реальную пересылку, а не dev-заглушку.
func TestSubmissionSinkConfiguredUsesForwarder(t *testing.T) {
	sink, err := submissionSink("demo=https://client.example/hook", "https://transit.example/intake", discardLogger())
	if err != nil {
		t.Fatalf("submissionSink: %v", err)
	}
	if sink == nil {
		t.Fatal("submissionSink: приёмник не собран")
	}
}

// TestSubmissionSinkEmptyFallsBackToDevStub: без конфигурации остаётся dev-заглушка,
// а не ошибка сборки — локальный прогон не падает, но путь помечен предупреждением.
func TestSubmissionSinkEmptyFallsBackToDevStub(t *testing.T) {
	sink, err := submissionSink("", "", discardLogger())
	if err != nil {
		t.Fatalf("submissionSink: %v", err)
	}
	if sink == nil {
		t.Fatal("submissionSink: пустая конфигурация должна дать dev-заглушку")
	}
}
