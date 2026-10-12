package messagedelivery

import (
	"context"
	"net"
	"net/http/httptest"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/message-delivery/internal/adapters/http"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/platform/transport/amqp"
	httpport "lead-gen/contracts/outreach-orchestration/approvedaction/httpport"
)

// TestApprovedActionIntakeTransportRoundTrip проверяет сквозной сетевой путь
// контракта: опубликованное через httpport-клиент действие принимается приёмной
// HTTP-поверхностью и доходит до моста (источника команд роли sender).
func TestApprovedActionIntakeTransportRoundTrip(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}
	var cfg Config
	bridge.Bind(&cfg)

	server := httptest.NewServer(httpapi.NewServer(bridge.Publisher()))
	defer server.Close()

	client, err := httpport.New(server.URL, httpport.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if err := client.Publish(context.Background(), sourceApprovedAction()); err != nil {
		t.Fatalf("Publish по сети: %v", err)
	}

	command, ok, err := cfg.Source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.CommandID != domain.CommandID("act-1") {
		t.Fatalf("CommandID = %q, ожидался act-1", command.CommandID)
	}
	if command.Address.String() != "lead@example.com" {
		t.Fatalf("адрес = %q", command.Address.String())
	}
}

// TestApprovedActionIntakeBridgePublisherNil безопасен: мост без приёмника
// отдаёт nil, а приёмная поверхность на nil честно отвечает 503.
func TestApprovedActionIntakeBridgePublisherNil(t *testing.T) {
	var bridge *ApprovedActionBridge
	if bridge.Publisher() != nil {
		t.Fatal("Publisher nil-моста должен быть nil")
	}
}

// TestRunWorkerServesIntake проверяет, что роль worker поднимает слушатель приёма
// по заданному адресу, действие доходит по сети, а по отмене контекста слушатель
// останавливается.
func TestRunWorkerServesIntake(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}
	var bound Config
	bridge.Bind(&bound)

	addr := freeAddr(t)

	cfg := testConfig()
	cfg.IntakeAddr = addr
	cfg.Intake = bridge.Publisher()
	cfg.Publisher = amqp.NewInMemoryBroker(1)

	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- service.runWorker(ctx) }()

	waitForTCP(t, addr)

	client, err := httpport.New("http://" + addr)
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if err := client.Publish(context.Background(), sourceApprovedAction()); err != nil {
		t.Fatalf("Publish по сети: %v", err)
	}
	command, ok, nextErr := bound.Source.Next(context.Background())
	if nextErr != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, nextErr)
	}
	if command.CommandID != domain.CommandID("act-1") {
		t.Fatalf("CommandID = %q, ожидался act-1", command.CommandID)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runWorker: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runWorker не остановился по отмене контекста")
	}
}

// TestRunWorkerWithoutIntakeKeepsPublishLoop проверяет, что пустой IntakeAddr
// сохраняет прежнее поведение: без Publisher роль завершается ошибкой.
func TestRunWorkerWithoutIntakeKeepsPublishLoop(t *testing.T) {
	service, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := service.runWorker(context.Background()); err == nil {
		t.Fatal("worker без Publisher и без адреса приёма должен вернуть ошибку")
	}
}

// freeAddr резервирует свободный TCP-адрес на loopback.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("закрытие пробного слушателя: %v", err)
	}
	return addr
}

// waitForTCP ждёт, пока слушатель по адресу начнёт принимать соединения.
func waitForTCP(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("слушатель %s не поднялся: %v", addr, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
