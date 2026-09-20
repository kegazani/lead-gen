package relay_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

var eventTime = time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

// approvedEvent собирает действие с утверждённым событием для публикации.
func approvedEvent(t *testing.T) domain.ActionApproved {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return domain.ActionApproved{
		CampaignID:   "campaign-1",
		TenantID:     "tenant-1",
		DraftID:      "draft-1",
		DraftVersion: 1,
		DecisionID:   "decision-1",
		Actor:        "operator-1",
		Channel:      channel,
		At:           eventTime,
	}
}

// commit публикуемое событие и возвращает журнал, который читает relay.
func commitEvent(t *testing.T, events ...domain.DomainEvent) *inmemory.Journal {
	t.Helper()
	uow := inmemory.NewUnitOfWork()
	err := uow.WithinTransaction(context.Background(), func(tx ports.Transaction) error {
		return tx.Outbox().Append(context.Background(), events...)
	})
	if err != nil {
		t.Fatalf("commit события: %v", err)
	}
	return uow.Journal()
}

// recordingSink имитирует идемпотентного получателя: внешний эффект создаётся
// только при первом появлении ключа идемпотентности. failFirst воспроизводит
// падение сразу после публикации, до подтверждения relay.
type recordingSink struct {
	mu         sync.Mutex
	deliveries int
	effects    map[string]int
	failFirst  bool
	calls      int
}

func newRecordingSink(failFirst bool) *recordingSink {
	return &recordingSink{effects: make(map[string]int), failFirst: failFirst}
}

func (s *recordingSink) Publish(_ context.Context, action contracts.ApprovedAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.deliveries++
	key := action.IdempotencyKey()
	if _, ok := s.effects[key]; !ok {
		s.effects[key] = 1
	}
	if s.failFirst && s.calls == 1 {
		return errors.New("имитация падения после публикации")
	}
	return nil
}

func (s *recordingSink) deliveryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deliveries
}

func (s *recordingSink) effectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.effects)
}

// Sink реализует интерфейс назначения relay.
var _ relay.Sink = (*recordingSink)(nil)

func TestRelayPublishesAfterCommit(t *testing.T) {
	t.Parallel()
	journal := commitEvent(t, approvedEvent(t))
	if got := journal.PendingCount(); got != 1 {
		t.Fatalf("после commit в outbox %d записей, ожидалась 1", got)
	}

	sink := newRecordingSink(false)
	published, err := relay.New(journal, sink).PublishPending(context.Background())
	if err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	if published != 1 {
		t.Fatalf("опубликовано %d сообщений, ожидалось 1", published)
	}
	if got := journal.PendingCount(); got != 0 {
		t.Fatalf("после публикации в outbox %d неподтверждённых записей, ожидалось 0", got)
	}
	if got := sink.effectCount(); got != 1 {
		t.Fatalf("внешних эффектов %d, ожидался 1", got)
	}
}

func TestRelayRedeliversWithoutDuplicateEffect(t *testing.T) {
	t.Parallel()
	journal := commitEvent(t, approvedEvent(t))
	sink := newRecordingSink(true)
	relayInstance := relay.New(journal, sink)

	// Падение после публикации, до подтверждения: сообщение остаётся в outbox.
	if _, err := relayInstance.PublishPending(context.Background()); err == nil {
		t.Fatal("ожидалась ошибка публикации при имитации падения")
	}
	if got := journal.PendingCount(); got != 1 {
		t.Fatalf("после падения в outbox %d неподтверждённых записей, ожидалась 1", got)
	}

	// Повторный проход доставляет событие заново и подтверждает его.
	published, err := relayInstance.PublishPending(context.Background())
	if err != nil {
		t.Fatalf("повторный PublishPending: %v", err)
	}
	if published != 1 {
		t.Fatalf("повторно опубликовано %d сообщений, ожидалось 1", published)
	}
	if got := journal.PendingCount(); got != 0 {
		t.Fatalf("после повторной публикации в outbox %d записей, ожидалось 0", got)
	}
	if got := sink.deliveryCount(); got != 2 {
		t.Fatalf("доставок %d, ожидалось 2 (первая и повторная)", got)
	}
	if got := sink.effectCount(); got != 1 {
		t.Fatalf("внешних эффектов у получателя %d, ожидался 1 (без дублирования)", got)
	}
}

func TestRelayRejectsInvalidContractWithoutPublishing(t *testing.T) {
	t.Parallel()
	journal := commitEvent(t, approvedEvent(t))
	sink := newRecordingSink(false)
	// Mapper отдаёт контракт без обязательных полей: публикация обязана
	// остановиться до вызова назначения.
	invalidMapper := func(domain.DomainEvent) (contracts.ApprovedAction, bool, error) {
		return contracts.ApprovedAction{}, true, nil
	}

	published, err := relay.NewWithMapper(journal, sink, invalidMapper).PublishPending(context.Background())
	if err == nil {
		t.Fatal("ожидалась ошибка валидации контракта")
	}
	if published != 0 {
		t.Fatalf("опубликовано %d контрактов, ожидалось 0", published)
	}
	if got := sink.deliveryCount(); got != 0 {
		t.Fatalf("назначение получило %d доставок, ожидалось 0", got)
	}
	if got := journal.PendingCount(); got != 1 {
		t.Fatalf("невалидный контракт не должен подтверждаться: осталось %d записей, ожидалась 1", got)
	}
}

func TestRelayConfirmsUnpublishedEvents(t *testing.T) {
	t.Parallel()
	stateChanged := domain.CampaignStateChanged{
		CampaignID: "campaign-1",
		TenantID:   "tenant-1",
		From:       domain.CampaignStateDraft,
		To:         domain.CampaignStateActive,
		At:         eventTime,
	}
	journal := commitEvent(t, stateChanged)

	sink := newRecordingSink(false)
	published, err := relay.New(journal, sink).PublishPending(context.Background())
	if err != nil {
		t.Fatalf("PublishPending: %v", err)
	}
	if published != 0 {
		t.Fatalf("опубликовано %d сообщений, ожидалось 0", published)
	}
	if got := sink.deliveryCount(); got != 0 {
		t.Fatalf("назначение получило %d доставок, ожидалось 0", got)
	}
	if got := journal.PendingCount(); got != 0 {
		t.Fatalf("событие без публикуемого контракта не подтверждено: осталось %d", got)
	}
}
