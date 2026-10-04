package messagedelivery

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer/provider"
	suppressionpolicyadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/suppression/policy"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/integration-platform/mail"
)

// stubMailPort — подмена контракта mail для проверок сборки.
type stubMailPort struct{}

func (stubMailPort) Send(context.Context, mail.Message) error { return nil }

// stubSuppressionPort — подмена контракта suppression-policy для проверок сборки.
type stubSuppressionPort struct{}

func (stubSuppressionPort) Check(context.Context, suppressionpolicy.SuppressionQuery) (suppressionpolicy.SuppressionDecision, error) {
	return suppressionpolicy.SuppressionDecision{}, nil
}

func testConfig() Config {
	return Config{
		Logger:  observ.NewLogger(),
		Metrics: observ.NewMetrics(),
		Tracer:  observ.NewTracer(ServiceID),
	}
}

// Dev-режим: без DSN, провайдера и стоп-листа сервис собирается на in-memory
// адаптерах, но об этом честно предупреждает (проверяется отсутствием ошибки и
// наличием журнала/повтора).
func TestNew_DevModeBuilds(t *testing.T) {
	service, err := New(testConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if service.Journal() == nil {
		t.Fatal("dev-режим должен собрать in-memory журнал outbox")
	}
	if service.slice.RetryPending == nil {
		t.Fatal("повторные окна должны быть собраны со значением по умолчанию")
	}
}

// Реальная сборка: почтовый адаптер и стоп-лист строятся из контрактов.
func TestNew_BuildsRealAdapters(t *testing.T) {
	cfg := testConfig()
	cfg.MailPort = stubMailPort{}
	cfg.MailFrom = "sender@example.com"
	cfg.SuppressionPort = stubSuppressionPort{}

	service, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := service.slice.Mailer.(*provider.Mailer); !ok {
		t.Fatalf("ожидался реальный почтовый адаптер, got %T", service.slice.Mailer)
	}
	if _, ok := service.slice.Suppressions.(*suppressionpolicyadapter.List); !ok {
		t.Fatalf("ожидался адаптер suppression-policy, got %T", service.slice.Suppressions)
	}
}

// Провайдер задан, но адрес отправителя пуст — ошибка конфигурации, а не
// молчаливая подмена.
func TestNew_RejectsIncompleteMailer(t *testing.T) {
	cfg := testConfig()
	cfg.MailPort = stubMailPort{}
	if _, err := New(cfg); err == nil {
		t.Fatal("сборка без адреса отправителя должна отвергаться")
	}
}
