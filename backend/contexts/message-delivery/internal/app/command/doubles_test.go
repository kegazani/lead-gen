package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
)

// Дубли портов для тестов прикладного слоя. Пакет app не вправе импортировать
// адаптеры контекста, поэтому порты подменяются собственными реализациями.

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time {
	if c.now.IsZero() {
		return time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	}
	return c.now
}

type fakeMessages struct {
	byID  map[domain.MessageID]domain.MessageSnapshot
	byKey map[string]domain.MessageID
}

func newFakeMessages() *fakeMessages {
	return &fakeMessages{
		byID:  make(map[domain.MessageID]domain.MessageSnapshot),
		byKey: make(map[string]domain.MessageID),
	}
}

func (r *fakeMessages) Save(_ context.Context, message *domain.Message) error {
	snapshot := message.Snapshot()
	if existing, ok := r.byKey[snapshot.IdempotencyKey]; ok && existing != snapshot.ID {
		return fmt.Errorf("ключ %q занят сообщением %s", snapshot.IdempotencyKey, existing)
	}
	r.byID[snapshot.ID] = snapshot
	r.byKey[snapshot.IdempotencyKey] = snapshot.ID
	return nil
}

func (r *fakeMessages) FindByID(_ context.Context, id domain.MessageID) (*domain.Message, error) {
	snapshot, ok := r.byID[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrMessageNotFound, id)
	}
	return domain.RehydrateMessage(snapshot)
}

func (r *fakeMessages) FindByIdempotencyKey(ctx context.Context, key string) (*domain.Message, error) {
	id, ok := r.byKey[key]
	if !ok {
		return nil, fmt.Errorf("%w: ключ %q", ports.ErrMessageNotFound, key)
	}
	return r.FindByID(ctx, id)
}

type fakeReputations struct {
	byDomain map[domain.SenderDomain]domain.SenderReputationSnapshot
}

func newFakeReputations() *fakeReputations {
	return &fakeReputations{byDomain: make(map[domain.SenderDomain]domain.SenderReputationSnapshot)}
}

func (r *fakeReputations) Save(_ context.Context, reputation *domain.SenderReputation) error {
	snapshot := reputation.Snapshot()
	r.byDomain[snapshot.Domain] = snapshot
	return nil
}

func (r *fakeReputations) FindByDomain(_ context.Context, sender domain.SenderDomain) (*domain.SenderReputation, error) {
	snapshot, ok := r.byDomain[sender]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrSenderReputationNotFound, sender)
	}
	return domain.RehydrateSenderReputation(snapshot)
}

type fakeOutbox struct {
	events []domain.DomainEvent
}

func (o *fakeOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

type fakeTx struct {
	messages    *fakeMessages
	reputations *fakeReputations
	outbox      *fakeOutbox
}

func (t *fakeTx) Messages() ports.MessageRepository             { return t.messages }
func (t *fakeTx) Reputations() ports.SenderReputationRepository { return t.reputations }
func (t *fakeTx) Outbox() ports.Outbox                          { return t.outbox }

type fakeUoW struct {
	messages    *fakeMessages
	reputations *fakeReputations
	outbox      *fakeOutbox
}

func newFakeUoW() *fakeUoW {
	return &fakeUoW{
		messages:    newFakeMessages(),
		reputations: newFakeReputations(),
		outbox:      &fakeOutbox{},
	}
}

func (u *fakeUoW) WithinTransaction(ctx context.Context, fn func(tx ports.Transaction) error) error {
	return fn(&fakeTx{messages: u.messages, reputations: u.reputations, outbox: u.outbox})
}

type fakeMailer struct {
	response ports.ProviderResponse
	err      error
	sent     []ports.OutgoingMessage
}

func (m *fakeMailer) Send(_ context.Context, message ports.OutgoingMessage) (ports.ProviderResponse, error) {
	m.sent = append(m.sent, message)
	if m.err != nil {
		return ports.ProviderResponse{}, m.err
	}
	return m.response, nil
}

type fakeSuppression struct {
	suppressed map[domain.DeliveryAddress]struct{}
	err        error
}

func newFakeSuppression() *fakeSuppression {
	return &fakeSuppression{suppressed: make(map[domain.DeliveryAddress]struct{})}
}

func (s *fakeSuppression) IsSuppressed(_ context.Context, address domain.DeliveryAddress) (bool, error) {
	if s.err != nil {
		return false, s.err
	}
	_, ok := s.suppressed[address]
	return ok, nil
}
