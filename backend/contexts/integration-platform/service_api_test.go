package integrationplatform

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/integration-platform/adapterstatus"
)

// staticStatusReader — источник состояния для теста роли api.
type staticStatusReader struct{ statuses []adapterstatus.Status }

func (s staticStatusReader) AdapterStatuses(context.Context, string) ([]adapterstatus.Status, error) {
	return s.statuses, nil
}

// TestAPIRoleServesAdapterStatus проверяет, что роль api отдаёт через HTTP
// реальные статусы источника: отсутствие Probe остаётся unknown, а секрет из
// ошибки Probe не раскрывается.
func TestAPIRoleServesAdapterStatus(t *testing.T) {
	addr := freeAddr(t)
	const secret = "api_key=supersecret-provider-value"

	service, err := New(Config{
		APIAddr: addr,
		Adapters: NewAdapterStatusSource(AdapterStatusConfig{Adapters: []AdapterSpec{
			{Name: "firecrawl", Configured: true},
			{
				Name:       "smtp",
				Configured: true,
				Probe:      func(context.Context) error { return errors.New("provider failed: " + secret) },
			},
		}}),
		Logger:  observ.NewLogger(),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка сервиса: %v", err)
	}
	defer func() { _ = service.Close() }()

	runner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok {
		t.Fatal("роль api не объявлена")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	base := "http://" + addr
	if err := waitForStatus(t, base+"/healthz"); err != nil {
		cancel()
		<-done
		t.Fatalf("сервер роли api не поднялся: %v", err)
	}

	resp, err := http.Get(base + "/adapter-status")
	if err != nil {
		cancel()
		<-done
		t.Fatalf("запрос состояния: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		<-done
		t.Fatalf("ожидался 200, получен %d", resp.StatusCode)
	}
	var body adapterstatus.Response
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		cancel()
		<-done
		t.Fatalf("разбор adapter-status: %v", err)
	}
	if strings.Contains(bodyString(body), secret) {
		cancel()
		<-done
		t.Fatalf("секрет провайдера попал в ответ: %+v", body)
	}
	if len(body.Adapters) != 2 || body.Adapters[0].State != adapterstatus.StateUnknown || body.Adapters[0].Reason != AdapterReasonProbeNotConfigured {
		cancel()
		<-done
		t.Fatalf("неожиданный статус адаптера без Probe: %+v", body.Adapters)
	}
	if body.Adapters[1].State != adapterstatus.StateDown || body.Adapters[1].Reason != AdapterReasonUnreachable {
		cancel()
		<-done
		t.Fatalf("неожиданный статус при ошибке Probe: %+v", body.Adapters[1])
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("останов роли api: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("роль api не остановилась по отмене контекста")
	}
}

// bodyString возвращает JSON-представление HTTP-контракта для проверки утечки.
func bodyString(body adapterstatus.Response) string {
	raw, _ := json.Marshal(body)
	return string(raw)
}

// freeAddr возвращает свободный адрес localhost для тестового сервера.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поиск свободного адреса: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("освобождение адреса: %v", err)
	}
	return addr
}

// waitForStatus опрашивает URL, пока сервер не ответит 200 или не истечёт время.
func waitForStatus(t *testing.T, url string) error {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = errors.New("сервер ответил " + resp.Status)
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = errors.New("истекло время ожидания")
	}
	return lastErr
}
