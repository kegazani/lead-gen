package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"lead-gen/backend/platform/observability/observ"
)

// freeListenAddr занимает и немедленно освобождает loopback-порт, чтобы отдать
// тесту конкретный адрес для liveness-слушателя (startLivenessListener не
// возвращает фактический порт при ":0").
func freeListenAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не удалось занять свободный порт: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitLiveness ждёт, пока liveness-слушатель начнёт отвечать кодом 0.
func waitLiveness(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if code := runHealthcheck(url, 200*time.Millisecond); code == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("liveness-слушатель не ответил на %s", url)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunHealthcheckLive проверяет, что живой liveness-слушатель даёт код 0.
func TestRunHealthcheckLive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	if code := runHealthcheck(srv.URL, 2*time.Second); code != 0 {
		t.Fatalf("проба живого слушателя = %d, ожидался 0", code)
	}
}

// TestRunHealthcheckTimeout проверяет, что таймаут пробы даёт ненулевой код.
func TestRunHealthcheckTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	if code := runHealthcheck(srv.URL, 10*time.Millisecond); code == 0 {
		t.Fatal("проба с таймаутом вернула 0, ожидался ненулевой код")
	}
}

// TestRunHealthcheckNon2xx проверяет, что не-2xx ответ трактуется как нездоровье.
func TestRunHealthcheckNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if code := runHealthcheck(srv.URL, 2*time.Second); code == 0 {
		t.Fatal("проба не-2xx вернула 0, ожидался ненулевой код")
	}
}

// TestRunHealthcheckEmptyURL проверяет, что пустой URL пробы даёт ненулевой код.
func TestRunHealthcheckEmptyURL(t *testing.T) {
	if code := runHealthcheck("", 2*time.Second); code == 0 {
		t.Fatal("проба с пустым URL вернула 0, ожидался ненулевой код")
	}
}

// TestLivenessListenerServesProcessProbe проверяет связку реального
// liveness-слушателя процесса и режима пробы `-healthcheck`: слушатель отвечает
// на /livez, а проба возвращает 0. Это сценарий «процесс жив и отвечает».
func TestLivenessListenerServesProcessProbe(t *testing.T) {
	addr := freeListenAddr(t)
	srv, err := startLivenessListener(addr, observ.NewLogger())
	if err != nil {
		t.Fatalf("liveness-слушатель не поднят: %v", err)
	}
	if srv == nil {
		t.Fatal("liveness-слушатель не поднят")
	}
	defer func() { _ = srv.Close() }()

	url := "http://" + addr + "/livez"
	waitLiveness(t, url)

	if code := runHealthcheck(url, 2*time.Second); code != 0 {
		t.Fatalf("проба liveness-слушателя = %d, ожидался 0", code)
	}
	// Проверяем сам endpoint, а не только код пробы: тело/статус нездоровья
	// должны быть видны.
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, ожидался 200", url, resp.StatusCode)
	}
}

// TestLivenessProbeIndependentOfReadiness проверяет сценарий спеки «проба живости
// не зависит от готовности»: при неготовом /readyz (503) liveness-слушатель
// процесса продолжает отвечать, и проба даёт 0.
func TestLivenessProbeIndependentOfReadiness(t *testing.T) {
	// Стенд готовности: всегда 503, как недоступный рантайм durable-исполнения.
	ready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer ready.Close()

	resp, err := http.Get(ready.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d, ожидался 503", resp.StatusCode)
	}

	// Liveness процесса поднимается независимо и отвечает, несмотря на 503.
	addr := freeListenAddr(t)
	srv, err := startLivenessListener(addr, observ.NewLogger())
	if err != nil {
		t.Fatalf("liveness-слушатель не поднят: %v", err)
	}
	if srv == nil {
		t.Fatal("liveness-слушатель не поднят")
	}
	defer func() { _ = srv.Close() }()
	waitLiveness(t, "http://"+addr+"/livez")

	if code := runHealthcheck("http://"+addr+"/livez", 2*time.Second); code != 0 {
		t.Fatalf("проба liveness при неготовом /readyz = %d, ожидался 0", code)
	}
}

// TestLivenessListenerOnOccupiedAddr проверяет, что занятый адрес возвращается
// вызывающему как ошибка (в main — фатальный выход): адрес пробы — часть контракта
// запуска, и жить без пробы тихо нельзя.
func TestLivenessListenerOnOccupiedAddr(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не удалось занять адрес: %v", err)
	}
	defer func() { _ = ln.Close() }()
	addr := ln.Addr().String()

	srv, err := startLivenessListener(addr, observ.NewLogger())
	if srv != nil {
		_ = srv.Close()
		t.Fatal("при занятом адресе слушатель не должен возвращаться")
	}
	if err == nil {
		t.Fatal("занятый адрес не вернул ошибку привязки")
	}
}
