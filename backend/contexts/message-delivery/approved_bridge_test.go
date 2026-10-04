package messagedelivery

import (
	"context"
	"errors"
	"testing"

	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/contracts/lead-management/deliverytarget"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
)

// sinkFunc повторяет сигнатуру outreachorchestration.SinkFunc. Чужой контекст
// сюда импортировать нельзя (importlint), поэтому совместимость доказывается
// совпадением формы: значение Sink() присваивается этому типу.
type sinkFunc func(ctx context.Context, action approvedaction.ApprovedAction) error

// Bind ставит источник в конфигурацию, и опубликованное действие доходит до
// роли sender — вплоть до отправки письма.
func TestApprovedActionBridgeBindDeliversToSender(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}

	var cfg Config
	bridge.Bind(&cfg)
	if cfg.Source == nil {
		t.Fatal("Bind не поставил источник команд в cfg.Source")
	}

	// Действие входит через приёмник (Sink) того же моста.
	if err := bridge.Sink()(context.Background(), sourceApprovedAction()); err != nil {
		t.Fatalf("Sink: %v", err)
	}

	memory := mailer.New()
	slice, err := Build(Params{Source: cfg.Source, Mailer: memory})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	command, ok, err := cfg.Source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if _, err := slice.DeliverMessage.Handle(context.Background(), command); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	sent := memory.Sent()
	if len(sent) != 1 {
		t.Fatalf("отправок %d, ожидалась 1", len(sent))
	}
	if sent[0].To.String() != "lead@example.com" {
		t.Fatalf("адрес отправки = %q", sent[0].To.String())
	}
	if sent[0].SenderDomain.String() != "mail.example.com" {
		t.Fatalf("отправляющий домен = %q", sent[0].SenderDomain.String())
	}
}

// Sink и источник — одна и та же сущность: опубликованное через Sink действие
// читается из источника, который Bind поставил в конфигурацию.
func TestApprovedActionBridgeSinkSharesBridgeInstance(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}

	var cfg Config
	bridge.Bind(&cfg)

	sink := bridge.Sink()
	if sink == nil {
		t.Fatal("Sink вернул nil для собранного моста")
	}
	if err := sink(context.Background(), sourceApprovedAction()); err != nil {
		t.Fatalf("Sink: %v", err)
	}

	command, ok, err := cfg.Source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.CommandID != domain.CommandID("act-1") {
		t.Fatalf("CommandID = %q, ожидался act-1", command.CommandID)
	}
}

// Форма Sink совместима с outreachorchestration.SinkFunc.
func TestApprovedActionBridgeSinkMatchesSinkFuncSignature(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}
	var _ sinkFunc = bridge.Sink()
}

// Отказ владельца получателя проходит через Sink как ошибка, а не как успех.
func TestApprovedActionBridgeSinkPropagatesRejection(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("", deliverytarget.ErrRejected), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}
	err = bridge.Sink()(context.Background(), sourceApprovedAction())
	if !errors.Is(err, approvedactionadapter.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// nil-мост безопасен: Bind — no-op, Sink — nil, паники нет.
func TestApprovedActionBridgeNilIsSafe(t *testing.T) {
	var bridge *ApprovedActionBridge
	cfg := Config{}
	bridge.Bind(&cfg)
	if cfg.Source != nil {
		t.Fatal("nil-мост не должен менять cfg.Source")
	}
	if bridge.Sink() != nil {
		t.Fatal("Sink nil-моста должен быть nil")
	}
}
