package messagedelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	"lead-gen/backend/contexts/message-delivery/internal/adapters/mailer"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
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

func TestApprovedActionBridgeCancelBeforeActionFencesLateEnqueueAndReplay(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cancellation := approvedaction.CampaignCancellation{TenantID: "tenant-1", CampaignID: "campaign-1"}
	if err := bridge.CancelCampaign(ctx, cancellation); err != nil {
		t.Fatalf("CancelCampaign: %v", err)
	}
	if err := bridge.CancelCampaign(ctx, cancellation); err != nil {
		t.Fatalf("replay CancelCampaign: %v", err)
	}
	action := sourceApprovedAction()
	action.TenantID, action.CampaignID = cancellation.TenantID, cancellation.CampaignID
	if err := bridge.Sink()(ctx, action); !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("late action error=%v, ожидался fence", err)
	}
}

func TestApprovedActionBridgeCancellationFencesQueuedMemoryCommand(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	bridge.Bind(&cfg)
	action := sourceApprovedAction()
	action.TenantID, action.CampaignID = "tenant-queued", "campaign-queued"
	if err := bridge.Sink()(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	if err := bridge.CancelCampaign(context.Background(), approvedaction.CampaignCancellation{TenantID: action.TenantID, CampaignID: action.CampaignID}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, ok, err := cfg.Source.Next(ctx); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued cancelled item escaped fence: ok=%v err=%v", ok, err)
	}
}

func TestApprovedActionBridgeCancellationAfterClaimFencesMemoryReservation(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	bridge.Bind(&cfg)
	action := sourceApprovedAction()
	action.TenantID, action.CampaignID = "tenant-claimed", "campaign-claimed"
	if err := bridge.Sink()(context.Background(), action); err != nil {
		t.Fatal(err)
	}
	mailer := mailer.New()
	slice, err := Build(Params{Source: cfg.Source, Mailer: mailer})
	if err != nil {
		t.Fatal(err)
	}
	cmd, ok, err := cfg.Source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if err := bridge.CancelCampaign(context.Background(), approvedaction.CampaignCancellation{TenantID: action.TenantID, CampaignID: action.CampaignID}); err != nil {
		t.Fatal(err)
	}
	if _, err := slice.DeliverMessage.Handle(context.Background(), cmd); !errors.Is(err, ports.ErrCampaignCancelled) {
		t.Fatalf("reservation error=%v", err)
	}
	if got := len(mailer.Sent()); got != 0 {
		t.Fatalf("mailer вызван %d раз после cancellation fence", got)
	}
}

// Вторая кампания к тому же получателю доставляется отдельно: игдентификатор
// утверждённого действия кампанийно-уникален, поэтому мост не считает её повтором
// и создаёт второе сообщение и второе событие доставки.
func TestApprovedActionBridgeDeliversSecondCampaignToSameRecipient(t *testing.T) {
	bridge, err := NewApprovedActionBridge(deliveryTargetStub("info@norbit.ru", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionBridge: %v", err)
	}

	var cfg Config
	bridge.Bind(&cfg)
	sink := bridge.Sink()

	first := sourceApprovedAction()
	first.EventID = "evt-1"
	first.DraftID = "info@norbit.ru"
	first.CampaignID = "camp-1"
	first.CorrelationID = "camp-1"
	first.RecipientRef = "info@norbit.ru"
	first.DraftVersion = 1
	first.ApprovedActionID = approvedaction.ApprovedActionIdentity("camp-1", "info@norbit.ru", 1)
	if err := sink(context.Background(), first); err != nil {
		t.Fatalf("Sink кампании 1: %v", err)
	}

	second := first
	second.EventID = "evt-2"
	second.CampaignID = "camp-2"
	second.CorrelationID = "camp-2"
	second.ApprovedActionID = approvedaction.ApprovedActionIdentity("camp-2", "info@norbit.ru", 1)
	if err := sink(context.Background(), second); err != nil {
		t.Fatalf("Sink кампании 2: %v", err)
	}

	memory := mailer.New()
	events := &captureSink{}
	slice, err := Build(Params{Source: cfg.Source, Mailer: memory, Sink: events})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for i := 0; i < 2; i++ {
		command, ok, err := cfg.Source.Next(context.Background())
		if err != nil || !ok {
			t.Fatalf("Next %d: ok=%v err=%v", i, ok, err)
		}
		if _, err := slice.DeliverMessage.Handle(context.Background(), command); err != nil {
			t.Fatalf("Handle %d: %v", i, err)
		}
	}

	if got := len(memory.Sent()); got != 2 {
		t.Fatalf("отправок %d, ожидалось 2: вторая кампания — не повтор", got)
	}
	if _, err := slice.Relay.PublishPending(context.Background()); err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	if got := len(events.events); got != 2 {
		t.Fatalf("событий доставки %d, ожидалось 2", got)
	}
	seen := map[string]bool{}
	for _, event := range events.events {
		seen[event.CampaignID] = true
	}
	if !seen["camp-1"] || !seen["camp-2"] {
		t.Fatalf("события не покрывают обе кампании: %+v", seen)
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
