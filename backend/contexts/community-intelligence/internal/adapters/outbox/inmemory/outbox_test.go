package inmemory

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// stubEvent — минимальное доменное событие для проверки outbox.
type stubEvent struct{ domain.EventHeader }

func (stubEvent) Name() domain.EventName       { return domain.EventCommunityDigestCreated }
func (e stubEvent) OccurredAt() time.Time      { return e.EventHeader.OccurredAt }
func (e stubEvent) Header() domain.EventHeader { return e.EventHeader }

func TestOutboxAppendsInOrder(t *testing.T) {
	outbox := New()
	first := stubEvent{}
	second := stubEvent{}
	if err := outbox.Append(context.Background(), first, second); err != nil {
		t.Fatalf("Append: %v", err)
	}
	events := outbox.Events()
	if len(events) != 2 {
		t.Fatalf("событий = %d, ожидалось 2", len(events))
	}
	if events[0].Name() != domain.EventCommunityDigestCreated {
		t.Fatalf("имя события %q", events[0].Name())
	}
}
