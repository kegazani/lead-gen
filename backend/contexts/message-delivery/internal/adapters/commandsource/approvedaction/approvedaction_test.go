package approvedaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	contracts "lead-gen/contracts/outreach-orchestration/approvedaction"
)

var testInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

// fakeRecipients — подмена ports.RecipientResolver: возвращает заданный адрес
// или ошибку и считает вызовы.
type fakeRecipients struct {
	address string
	err     error
	calls   int
}

func (f *fakeRecipients) ResolveRecipient(_ context.Context, _, _ string) (string, error) {
	f.calls++
	if f.err != nil {
		return "", f.err
	}
	return f.address, nil
}

// fakeMaterials — подмена ports.MaterialResolver.
type fakeMaterials struct {
	material ports.Material
	err      error
	calls    int
}

func (f *fakeMaterials) ResolveMaterial(_ context.Context, _, _ string) (ports.Material, error) {
	f.calls++
	if f.err != nil {
		return ports.Material{}, f.err
	}
	return f.material, nil
}

func validAction() contracts.ApprovedAction {
	action := contracts.NewApprovedAction(contracts.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		CausationID:   "dec-1",
	})
	action.ApprovedActionID = "act-1"
	action.CampaignID = "camp-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 3
	action.DecisionID = "dec-1"
	action.Channel = contracts.ChannelEmailName
	action.RecipientRef = "recipient-ref-1"
	action.MaterialVersionRef = "mat-ref-1"
	action.DecidedAt = testInstant
	return action
}

func newAdapter(t *testing.T, recipients ports.RecipientResolver, materials ports.MaterialResolver) *Adapter {
	t.Helper()
	adapter, err := New(Config{
		Recipients:   recipients,
		Materials:    materials,
		SenderDomain: "mail.example.com",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

// Успех: валидное действие разрешает ссылки и превращается в команду доставки,
// которую выдаёт роль sender.
func TestPublishBuildsCommandForSender(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	materials := &fakeMaterials{material: ports.Material{Subject: "Тема", Body: "Тело"}}
	adapter := newAdapter(t, recipients, materials)

	action := validAction()
	if err := adapter.Publish(context.Background(), action); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	command, ok, err := adapter.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.CommandID != domain.CommandID("act-1") {
		t.Errorf("CommandID: got %q", command.CommandID)
	}
	if command.ApprovedActionID != domain.ApprovedActionID("act-1") {
		t.Errorf("ApprovedActionID: got %q", command.ApprovedActionID)
	}
	if command.TenantID != domain.TenantID("tenant-1") {
		t.Errorf("TenantID: got %q", command.TenantID)
	}
	if command.RecipientRef != domain.RecipientRef("recipient-ref-1") {
		t.Errorf("RecipientRef: got %q", command.RecipientRef)
	}
	if got := command.Address.String(); got != "user@example.com" {
		t.Errorf("Address: got %q", got)
	}
	if got := command.SenderDomain.String(); got != "mail.example.com" {
		t.Errorf("SenderDomain: got %q", got)
	}
	if command.Subject != "Тема" || command.Body != "Тело" {
		t.Errorf("материал: got (%q, %q)", command.Subject, command.Body)
	}
	if command.CorrelationID != domain.CorrelationID("corr-1") {
		t.Errorf("CorrelationID: got %q", command.CorrelationID)
	}
	if command.CausationID != "dec-1" {
		t.Errorf("CausationID: got %q", command.CausationID)
	}
}

// Идемпотентность: повтор того же действия не создаёт второй команды и виден как
// повтор.
func TestPublishDuplicateDoesNotEnqueueAgain(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	action := validAction()
	if err := adapter.Publish(context.Background(), action); err != nil {
		t.Fatalf("первый Publish: %v", err)
	}
	// Повторная доставка: другой event_id, тот же ключ (tenant, approved_action_id).
	repeat := action
	repeat.EventID = "evt-2"
	result, err := adapter.Enqueue(context.Background(), repeat)
	if err != nil {
		t.Fatalf("повторный Enqueue: %v", err)
	}
	if !result.Duplicate {
		t.Fatal("повтор должен быть виден как повтор")
	}
	if got := adapter.queue.Len(); got != 1 {
		t.Fatalf("в очереди %d команд, ожидалась 1", got)
	}
	if recipients.calls != 1 {
		t.Fatalf("резолвер получателя вызван %d раз, ожидался 1", recipients.calls)
	}
}

// Не-email канал отклоняется без внешнего вызова.
func TestPublishRejectsNonEmailChannel(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	action := validAction()
	action.Channel = contracts.ChannelCallName
	err := adapter.Publish(context.Background(), action)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидался ErrRejected, got %v", err)
	}
	if recipients.calls != 0 {
		t.Fatalf("резолвер вызван %d раз при не-email канале, ожидалось 0", recipients.calls)
	}
	if adapter.queue.Len() != 0 {
		t.Fatal("команда не должна быть создана при не-email канале")
	}
}

// Невалидное действие — явная ошибка, а не молчаливый пропуск.
func TestPublishRejectsInvalidAction(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	err := adapter.Publish(context.Background(), contracts.ApprovedAction{})
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидался ErrRejected, got %v", err)
	}
	if recipients.calls != 0 {
		t.Fatalf("резолвер вызван %d раз для невалидного действия", recipients.calls)
	}
}

// Неразрешимая ссылка получателя — доменный отказ, адрес не выдумывается.
func TestPublishUnresolvableRecipientRejected(t *testing.T) {
	recipients := &fakeRecipients{err: ports.ErrReferenceUnresolved}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	err := adapter.Publish(context.Background(), validAction())
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидался ErrRejected, got %v", err)
	}
	if adapter.queue.Len() != 0 {
		t.Fatal("команда не должна быть создана при неразрешимой ссылке")
	}
}

// Недоступность резолвера — честный отказ (не выдаётся за успех).
func TestPublishResolverUnavailable(t *testing.T) {
	recipients := &fakeRecipients{err: ports.ErrResolverUnavailable}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	err := adapter.Publish(context.Background(), validAction())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, got %v", err)
	}
	if adapter.queue.Len() != 0 {
		t.Fatal("команда не должна быть создана при недоступности резолвера")
	}
}

// Неуспешный приём не «съедает» действие: повтор после восстановления резолвера
// проходит и создаёт ровно одну команду.
func TestPublishRetryAfterFailureSucceeds(t *testing.T) {
	recipients := &fakeRecipients{err: ports.ErrResolverUnavailable}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	action := validAction()
	if err := adapter.Publish(context.Background(), action); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("первый приём: ожидался ErrUnavailable, got %v", err)
	}

	recipients.err = nil
	recipients.address = "user@example.com"
	if err := adapter.Publish(context.Background(), action); err != nil {
		t.Fatalf("повтор после восстановления: %v", err)
	}
	if got := adapter.queue.Len(); got != 1 {
		t.Fatalf("в очереди %d команд, ожидалась 1", got)
	}
}

// Заданная ссылка на материал без резолвера — честная недоступность, а не
// выдуманное содержимое.
func TestPublishMaterialResolverMissing(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	adapter := newAdapter(t, recipients, nil)

	err := adapter.Publish(context.Background(), validAction())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, got %v", err)
	}
}

// Недоступность резолвера материала — честный отказ.
func TestPublishMaterialResolverUnavailable(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	materials := &fakeMaterials{err: ports.ErrResolverUnavailable}
	adapter := newAdapter(t, recipients, materials)

	err := adapter.Publish(context.Background(), validAction())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, got %v", err)
	}
	if adapter.queue.Len() != 0 {
		t.Fatal("команда не должна быть создана при недоступности резолвера материала")
	}
}

// Пустая ссылка на материал — не ошибка: содержимое остаётся пустым.
func TestPublishEmptyMaterialRefLeavesContentEmpty(t *testing.T) {
	recipients := &fakeRecipients{address: "user@example.com"}
	adapter := newAdapter(t, recipients, nil)

	action := validAction()
	action.MaterialVersionRef = ""
	if err := adapter.Publish(context.Background(), action); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	command, ok, err := adapter.Next(context.Background())
	if err != nil || !ok {
		t.Fatalf("Next: ok=%v err=%v", ok, err)
	}
	if command.Subject != "" || command.Body != "" {
		t.Fatalf("содержимое должно остаться пустым, got (%q, %q)", command.Subject, command.Body)
	}
}

// Адрес, не похожий на почтовый, отвергается доменом.
func TestPublishInvalidAddressRejected(t *testing.T) {
	recipients := &fakeRecipients{address: "not-an-address"}
	adapter := newAdapter(t, recipients, &fakeMaterials{})

	err := adapter.Publish(context.Background(), validAction())
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("ожидался ErrRejected, got %v", err)
	}
}

// Сборка без резолвера получателя или отправляющего домена отвергается.
func TestNewRejectsIncompleteConfig(t *testing.T) {
	if _, err := New(Config{SenderDomain: "mail.example.com"}); err == nil {
		t.Fatal("сборка без резолвера получателя должна отвергаться")
	}
	if _, err := New(Config{Recipients: &fakeRecipients{}}); err == nil {
		t.Fatal("сборка без отправляющего домена должна отвергаться")
	}
}
