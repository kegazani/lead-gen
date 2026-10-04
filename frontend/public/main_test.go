package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

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
	handler, err := newHandler(ports.PageRef("demo"), assembly.TransportInProcess, discardLogger())
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
	handler, err := newHandler("", assembly.TransportInProcess, discardLogger())
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
	handler, err := newHandler(ports.PageRef("demo"), assembly.TransportNetwork, discardLogger())
	if err == nil {
		t.Fatal("newHandler(http-json): ожидалась явная ошибка сборки, получен рабочий обработчик")
	}
	if handler != nil {
		t.Errorf("newHandler(http-json): при ошибке обработчик должен быть nil, получен %T", handler)
	}
}
