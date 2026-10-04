package messagedelivery

import (
	"context"
	"errors"
	"testing"
	"time"

	approvedactionadapter "lead-gen/backend/contexts/message-delivery/internal/adapters/commandsource/approvedaction"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/contracts/lead-management/deliverytarget"
	approvedaction "lead-gen/contracts/outreach-orchestration/approvedaction"
)

var sourceInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// deliveryTargetStub — реализация контракта delivery-target на in-proc адаптере
// владельца: возвращает заданный адрес или ошибку.
func deliveryTargetStub(address string, err error) deliverytarget.Resolver {
	return deliverytarget.InProc(deliverytarget.InProcHandler{
		ResolveFunc: func(context.Context, string, string) (deliverytarget.Recipient, error) {
			if err != nil {
				return deliverytarget.Recipient{}, err
			}
			return deliverytarget.Recipient{Address: address}, nil
		},
	})
}

func sourceApprovedAction() approvedaction.ApprovedAction {
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    sourceInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		CausationID:   "dec-1",
	})
	action.ApprovedActionID = "act-1"
	action.CampaignID = "camp-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 3
	action.DecisionID = "dec-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "recipient-ref-1"
	action.DecidedAt = sourceInstant
	return action
}

// Фабрика собирает один мост в двух ролях: принятое утверждённое действие
// превращается в команду доставки, которую выдаёт источник роли sender.
func TestNewApprovedActionSourceWiresPublisherToSource(t *testing.T) {
	source, publisher, err := NewApprovedActionSource(
		deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionSource: %v", err)
	}
	if source == nil || publisher == nil {
		t.Fatal("источник и приёмник должны быть собраны")
	}

	if err := publisher.Publish(context.Background(), sourceApprovedAction()); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	command, ok, err := source.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.Address.String() != "lead@example.com" {
		t.Fatalf("адрес = %q", command.Address.String())
	}
	if command.SenderDomain.String() != "mail.example.com" {
		t.Fatalf("отправляющий домен = %q", command.SenderDomain.String())
	}
	if command.CommandID != domain.CommandID("act-1") {
		t.Fatalf("CommandID = %q", command.CommandID)
	}
}

// Реальный контракт delivery-target в другом исходе: доменный отказ владельца
// доходит до вызывающего как отказ моста, команда не создаётся.
func TestNewApprovedActionSourcePropagatesOwnerRejection(t *testing.T) {
	source, publisher, err := NewApprovedActionSource(
		deliveryTargetStub("", deliverytarget.ErrRejected), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionSource: %v", err)
	}
	err = publisher.Publish(context.Background(), sourceApprovedAction())
	if !errors.Is(err, approvedactionadapter.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
	// Команды в очереди нет: Next ждёт появления и упирается в дедлайн, а не
	// отдаёт выдуманную команду.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if command, ok, nextErr := source.Next(ctx); ok || !errors.Is(nextErr, context.DeadlineExceeded) {
		t.Fatalf("команда не должна быть создана: %+v ok=%v err=%v", command, ok, nextErr)
	}
}

// Недоступность владельца — честная недоступность, а не выдуманный адрес.
func TestNewApprovedActionSourcePropagatesOwnerUnavailable(t *testing.T) {
	_, publisher, err := NewApprovedActionSource(
		deliveryTargetStub("", deliverytarget.ErrUnavailable), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionSource: %v", err)
	}
	err = publisher.Publish(context.Background(), sourceApprovedAction())
	if !errors.Is(err, approvedactionadapter.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// Нет подходящего резолвера материала: заданный MaterialVersionRef честно
// сообщает о недоступности, а не подменяется пустым содержимым.
func TestNewApprovedActionSourceMaterialUnavailableWithoutResolver(t *testing.T) {
	_, publisher, err := NewApprovedActionSource(
		deliveryTargetStub("lead@example.com", nil), nil, "mail.example.com")
	if err != nil {
		t.Fatalf("NewApprovedActionSource: %v", err)
	}
	action := sourceApprovedAction()
	action.MaterialVersionRef = "mat-ref-1"
	err = publisher.Publish(context.Background(), action)
	if !errors.Is(err, approvedactionadapter.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// Незаданный резолвер получателя или отправляющий домен отвергаются на сборке.
func TestNewApprovedActionSourceRejectsIncompleteConfig(t *testing.T) {
	if _, _, err := NewApprovedActionSource(nil, nil, "mail.example.com"); err == nil {
		t.Fatal("сборка без резолвера получателя должна отвергаться")
	}
	if _, _, err := NewApprovedActionSource(deliveryTargetStub("lead@example.com", nil), nil, ""); err == nil {
		t.Fatal("сборка без отправляющего домена должна отвергаться")
	}
}
