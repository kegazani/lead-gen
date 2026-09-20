package relay_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/inmemory"
	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
)

// batchSize — размер пакета в проверке атомарности: заведомо больше единичного
// решения и не меньше порога пропускной способности.
const batchSize = 50

// idempotentBatchSink — идемпотентный получатель пакета: внешний эффект по ключу
// идемпотентности создаётся один раз, повторная доставка второго эффекта не даёт.
// failAt воспроизводит падение процесса после доставки, но до подтверждения
// relay: сообщение остаётся в outbox и будет выдано повторно.
type idempotentBatchSink struct {
	mu         sync.Mutex
	calls      int
	deliveries int
	effects    map[string]struct{}
	failAt     int
}

func newIdempotentBatchSink(failAt int) *idempotentBatchSink {
	return &idempotentBatchSink{effects: make(map[string]struct{}), failAt: failAt}
}

func (s *idempotentBatchSink) Publish(_ context.Context, action contracts.ApprovedAction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.deliveries++
	s.effects[action.IdempotencyKey()] = struct{}{}
	if s.failAt != 0 && s.calls == s.failAt {
		return errors.New("имитация падения после доставки")
	}
	return nil
}

func (s *idempotentBatchSink) deliveryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deliveries
}

func (s *idempotentBatchSink) effectCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.effects)
}

// Sink реализует интерфейс назначения relay.
var _ relay.Sink = (*idempotentBatchSink)(nil)

// TestBatchApprovalOutboxAtomicityAndRedelivery подтверждает задачу 4.2:
// изменение агрегата (пакетное решение) и запись всех N событий выполняются в
// одной транзакции Outbox, публикация идёт после commit, а падение после commit
// приводит к повторной публикации всех N событий — без потери и без дублирования
// внешнего эффекта у идемпотентного получателя.
func TestBatchApprovalOutboxAtomicityAndRedelivery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	uow := inmemory.NewUnitOfWork()

	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	window, err := domain.NewCampaignWindow(eventTime, eventTime.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-batch",
		TenantID: "tenant-1",
		Name:     "Пакетная кампания",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}

	events := make([]domain.DomainEvent, 0, batchSize)
	for i := 0; i < batchSize; i++ {
		draftID := domain.DraftID(fmt.Sprintf("draft-%03d", i))
		spec := domain.DraftSpec{
			Material:  "material-a",
			Segment:   "segment-1",
			Recipient: domain.RecipientID(fmt.Sprintf("recipient-%03d", i)),
			Channel:   channel,
		}
		if _, _, err := campaign.DraftAction(draftID, spec, eventTime); err != nil {
			t.Fatalf("DraftAction %s: %v", draftID, err)
		}
		events = append(events, domain.ActionApproved{
			CampaignID:   "campaign-batch",
			TenantID:     "tenant-1",
			DraftID:      draftID,
			DraftVersion: 1,
			DecisionID:   domain.DecisionID(fmt.Sprintf("decision-%03d", i)),
			Actor:        "operator-1",
			Channel:      channel,
			At:           eventTime,
		})
	}

	// Одна транзакция: сохранение агрегата с решениями и запись всех N событий.
	err = uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		// До commit ни агрегат, ни события не видны снаружи транзакции.
		if _, err := uow.Repository().FindByID(ctx, "campaign-batch"); !errors.Is(err, ports.ErrCampaignNotFound) {
			return fmt.Errorf("до commit кампания видна: %v", err)
		}
		if got := uow.Journal().PendingCount(); got != 0 {
			return fmt.Errorf("до commit в outbox %d записей, ожидалось 0", got)
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, events...)
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	// После commit закоммичены и агрегат, и ровно N событий.
	loaded, err := uow.Repository().FindByID(ctx, "campaign-batch")
	if err != nil {
		t.Fatalf("после commit кампания не найдена: %v", err)
	}
	if got := len(loaded.Snapshot().Drafts); got != batchSize {
		t.Fatalf("после commit в кампании %d черновиков, ожидалось %d", got, batchSize)
	}
	if got := uow.Journal().PendingCount(); got != batchSize {
		t.Fatalf("после commit в outbox %d записей, ожидалось %d", got, batchSize)
	}

	// Падение после commit: relay доставляет 9 событий, на 10-м спотыкается и не
	// подтверждает его. Все N событий обязаны дойти повторной публикацией.
	sink := newIdempotentBatchSink(10)
	relayInstance := relay.New(uow.Journal(), sink)
	published, err := relayInstance.PublishPending(ctx)
	if err == nil {
		t.Fatal("ожидалась ошибка на имитации падения после доставки")
	}
	if published != 9 {
		t.Fatalf("до падения опубликовано %d сообщений, ожидалось 9", published)
	}
	if got := uow.Journal().PendingCount(); got != batchSize-9 {
		t.Fatalf("после падения в outbox %d неподтверждённых записей, ожидалось %d", got, batchSize-9)
	}

	// Повторный проход публикует остаток: ни одно событие не потеряно.
	rest, err := relayInstance.PublishPending(ctx)
	if err != nil {
		t.Fatalf("повторный PublishPending: %v", err)
	}
	if rest != batchSize-9 {
		t.Fatalf("повторно опубликовано %d сообщений, ожидалось %d", rest, batchSize-9)
	}
	if got := uow.Journal().PendingCount(); got != 0 {
		t.Fatalf("после повторной публикации в outbox %d записей, ожидалось 0", got)
	}

	// У идемпотентного получателя ровно N внешних эффектов: повторная доставка
	// десятого события второго эффекта не создала.
	if got := sink.effectCount(); got != batchSize {
		t.Fatalf("внешних эффектов %d, ожидалось %d (потеря или дублирование)", got, batchSize)
	}
	if got := sink.deliveryCount(); got != batchSize+1 {
		t.Fatalf("доставок %d, ожидалось %d (N плюс одна повторная)", got, batchSize+1)
	}
}
