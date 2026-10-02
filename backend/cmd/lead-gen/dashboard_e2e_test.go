package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	analytics "lead-gen/backend/contexts/analytics"
	leadmanagement "lead-gen/backend/contexts/lead-management"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/backend/platform/transport/amqp"
)

// TestDashboardEndToEndOverRabbitMQ проверяет рабочий контур дашборда на живом
// брокере: издатель lead-management публикует нормализованное событие
// (analytics-event) в RabbitMQ, воркер analytics вычитывает его из очереди
// analytics.analytics-event, проекция наполняется, а роль api отдаёт счётчики
// потоком серверных событий. Тест пропускается без RABBITMQ_URL: он требует
// поднятого RabbitMQ.
func TestDashboardEndToEndOverRabbitMQ(t *testing.T) {
	url := strings.TrimSpace(os.Getenv("RABBITMQ_URL"))
	if url == "" {
		t.Skip("RabbitMQ недоступен: задайте RABBITMQ_URL для сквозной проверки дашборда")
	}

	broker, err := amqp.NewRabbitMQBroker(url, 3)
	if err != nil {
		t.Fatalf("подключение к RabbitMQ: %v", err)
	}
	defer broker.Close()

	addr := freeAddr(t)
	service, err := analytics.New(analytics.Config{
		APIAddr:  addr,
		Consumer: broker,
		Logger:   observ.NewLoggerWithWriter(io.Discard),
		Metrics:  observ.NewMetrics(),
		Tracer:   observ.NewTracer(analytics.ServiceID),
	})
	if err != nil {
		t.Fatalf("сборка analytics: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for _, role := range []composition.Role{composition.RoleAPI, composition.RoleWorker} {
		runner, ok := service.RoleRunner(role)
		if !ok {
			t.Fatalf("роль %s не зарегистрирована", role)
		}
		wg.Add(1)
		go func(r composition.RoleRunner) {
			defer wg.Done()
			_ = r.Run(ctx)
		}(runner)
	}
	defer func() {
		cancel()
		wg.Wait()
	}()

	baseURL := "http://" + addr
	waitHealth(t, baseURL)

	const tenant = "e2e-tenant"
	publisher := leadmanagement.NewAnalyticsPublisher(broker, tenant)

	// Две компании проходят порог минимальной группы среза, поэтому дашборд
	// становится видимым и счётчик «отправлено» меняется с 0 на 2.
	for _, company := range []string{"e2e-company-1", "e2e-company-2"} {
		err := publisher.Publish(ctx, leadmanagement.AnalyticsEvent{
			EventID:    "e2e-" + company,
			TenantID:   tenant,
			Kind:       "message-sent",
			CompanyID:  company,
			Status:     "sent",
			Outcome:    "sent",
			OccurredAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("публикация события компании %s: %v", company, err)
		}
	}

	frame := readMetricsFrame(t, baseURL, tenant)
	if !frame.Visible {
		t.Fatalf("срез дашборда невидим: %+v", frame)
	}
	if frame.Communicated != 2 {
		t.Fatalf("счётчик «отправлено» = %d, ожидалось 2 (событие не дошло до проекции)", frame.Communicated)
	}
}

// metricsFrame — подмножество кадра потока, которое проверяет тест.
type metricsFrame struct {
	Visible      bool `json:"visible"`
	Found        int  `json:"found"`
	Communicated int  `json:"communicated"`
}

// readMetricsFrame читает поток серверных событий, пока не увидит видимый срез с
// двумя отправленными письмами, либо не истечёт таймаут.
func readMetricsFrame(t *testing.T, baseURL, tenant string) metricsFrame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/dashboard/stream?tenant_id="+tenant, nil)
	if err != nil {
		t.Fatalf("запрос потока: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("поток дашборда: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("поток дашборда: статус %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var frame metricsFrame
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &frame); err != nil {
			continue
		}
		if frame.Visible && frame.Communicated >= 2 {
			return frame
		}
	}
	t.Fatal("в потоке дашборда не появился срез с двумя отправленными письмами")
	return metricsFrame{}
}

// waitHealth ждёт готовности HTTP-сервера роли api.
func waitHealth(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("HTTP-сервер роли api не поднялся")
}

// freeAddr возвращает свободный TCP-адрес на петле.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("поиск свободного адреса: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}
