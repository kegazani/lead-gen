package publishing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	publishing "lead-gen/backend/contexts/publishing"
	digestadapter "lead-gen/backend/contexts/publishing/internal/adapters/digest"
	"lead-gen/backend/contexts/publishing/internal/adapters/outbox/memory"
	platformadapter "lead-gen/backend/contexts/publishing/internal/adapters/platform"
	"lead-gen/backend/contexts/publishing/internal/app/command"
	appquery "lead-gen/backend/contexts/publishing/internal/app/query"
	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

var now = time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

// fixedClock — часы с фиксированным моментом для порта Clock.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// seqIDs — детерминированный генератор идентификаторов событий.
type seqIDs struct{ n int }

func (s *seqIDs) NewEventID() domain.EventID {
	s.n++
	return domain.EventID("evt-" + string(rune('0'+s.n)))
}

func digestRef() domain.DigestRef {
	return domain.DigestRef{ID: "digest-1", CommunityID: "community-1", Period: "2026-W10"}
}

func newSlice(t *testing.T, platform ports.PlatformPublisher) *publishing.Slice {
	t.Helper()
	digest := digestadapter.NewMemory()
	if err := digest.Put(domain.Digest{Ref: digestRef(), Topics: []string{"тема"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	slice, err := publishing.Build(publishing.Config{
		Platform: platform,
		Digest:   digest,
		Clock:    fixedClock{now: now},
		IDs:      &seqIDs{},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return slice
}

func target(t *testing.T, platform string) domain.PublicationTarget {
	t.Helper()
	parsed, err := domain.NewPublicationTarget(platform, "platform-account")
	if err != nil {
		t.Fatalf("NewPublicationTarget: %v", err)
	}
	return parsed
}

// request строит команду на два материала: LinkedIn и Telegram.
func request(t *testing.T) command.RequestPublication {
	t.Helper()
	return command.RequestPublication{
		TenantID: "tenant-1",
		Actor:    "operator-1",
		Digest:   digestRef(),
		Targets: []command.PublicationRequest{
			{PublicationID: "pub-li", Target: target(t, "linkedin"), Title: "Пост", Body: "Текст поста", IdempotencyKey: "key-li"},
			{PublicationID: "pub-tg", Target: target(t, "telegram"), Title: "Сообщение", Body: "Текст сообщения", IdempotencyKey: "key-tg"},
		},
	}
}

func TestSliceHappyPath(t *testing.T) {
	ctx := context.Background()
	platform := platformadapter.NewDeterministic()
	slice := newSlice(t, platform)

	created, err := slice.RequestPublication.Handle(ctx, request(t))
	if err != nil {
		t.Fatalf("RequestPublication: %v", err)
	}
	if len(created.Publications) != 2 {
		t.Fatalf("создано %d публикаций, ожидалось 2", len(created.Publications))
	}
	for _, state := range created.Publications {
		if state.Status != domain.StatusDraft {
			t.Fatalf("статус = %q, ожидался draft", state.Status)
		}
	}

	// Публикация без утверждения запрещена.
	if _, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"}); !errors.Is(err, domain.ErrApprovalRequired) {
		t.Fatalf("ошибка = %v, ожидалась ErrApprovalRequired", err)
	}

	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-1"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	published, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2", CorrelationID: "corr-1"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if published.State.Status != domain.StatusPublished || published.Address == "" {
		t.Fatalf("результат публикации неверен: %+v", published)
	}
	if len(published.Events) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(published.Events))
	}

	// Повторная публикация терминального состояния запрещена.
	if _, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"}); !errors.Is(err, domain.ErrTerminalState) {
		t.Fatalf("ошибка = %v, ожидалась ErrTerminalState", err)
	}

	// Отклонение второго материала.
	rejected, err := slice.Reject.Handle(ctx, command.Reject{PublicationID: "pub-tg", TenantID: "tenant-1", Actor: "operator-1", Reason: "не по формату"})
	if err != nil {
		t.Fatalf("Reject: %v", err)
	}
	if rejected.Status != domain.StatusRejected {
		t.Fatalf("статус = %q, ожидался rejected", rejected.Status)
	}

	view, err := slice.GetPublication.Handle(ctx, appquery.GetPublication{PublicationID: "pub-li", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if view.Status != domain.StatusPublished || view.ApprovedBy != "operator-1" || view.PublishedAt.IsZero() {
		t.Fatalf("проекция неполна: %+v", view)
	}
}

func TestIdempotentRepeat(t *testing.T) {
	ctx := context.Background()
	slice := newSlice(t, platformadapter.NewDeterministic())

	if _, err := slice.RequestPublication.Handle(ctx, request(t)); err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	repeat, err := slice.RequestPublication.Handle(ctx, request(t))
	if err != nil {
		t.Fatalf("повтор: %v", err)
	}
	if len(repeat.Publications) != 2 {
		t.Fatalf("повтор вернул %d публикаций, ожидалось 2", len(repeat.Publications))
	}
	drafts, err := slice.PublicationQueue.Handle(ctx, appquery.PublicationQueue{TenantID: "tenant-1", Status: domain.StatusDraft})
	if err != nil {
		t.Fatalf("PublicationQueue: %v", err)
	}
	if len(drafts) != 2 {
		t.Fatalf("черновиков = %d, ожидалось 2 (повтор не создал дублей)", len(drafts))
	}
}

func TestDuplicateKeyInBatch(t *testing.T) {
	ctx := context.Background()
	slice := newSlice(t, platformadapter.NewDeterministic())
	cmd := request(t)
	cmd.Targets[1].IdempotencyKey = cmd.Targets[0].IdempotencyKey
	if _, err := slice.RequestPublication.Handle(ctx, cmd); !errors.Is(err, command.ErrDuplicateTarget) {
		t.Fatalf("ошибка = %v, ожидалась ErrDuplicateTarget", err)
	}
}

func TestTenantMismatch(t *testing.T) {
	ctx := context.Background()
	slice := newSlice(t, platformadapter.NewDeterministic())
	if _, err := slice.RequestPublication.Handle(ctx, request(t)); err != nil {
		t.Fatalf("RequestPublication: %v", err)
	}
	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-2", Actor: "operator-1"}); !errors.Is(err, command.ErrTenantMismatch) {
		t.Fatalf("ошибка = %v, ожидалась ErrTenantMismatch", err)
	}
}

func TestSourceNotFound(t *testing.T) {
	ctx := context.Background()
	digest := digestadapter.NewMemory() // выжимка не положена
	slice, err := publishing.Build(publishing.Config{Digest: digest, Platform: platformadapter.NewDeterministic()})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if _, err := slice.RequestPublication.Handle(ctx, request(t)); !errors.Is(err, command.ErrSourceNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrSourceNotFound", err)
	}
}

func TestChannelNotReady(t *testing.T) {
	ctx := context.Background()
	platform := platformadapter.NewDeterministic().WithNotReady("linkedin")
	slice := newSlice(t, platform)
	if _, err := slice.RequestPublication.Handle(ctx, request(t)); err != nil {
		t.Fatalf("RequestPublication: %v", err)
	}
	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-1"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"}); !errors.Is(err, ports.ErrChannelNotReady) {
		t.Fatalf("ошибка = %v, ожидалась ErrChannelNotReady", err)
	}
	// Состояние осталось утверждённым: повтор допустим.
	view, err := slice.GetPublication.Handle(ctx, appquery.GetPublication{PublicationID: "pub-li", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("GetPublication: %v", err)
	}
	if view.Status != domain.StatusApproved {
		t.Fatalf("статус = %q, ожидался approved", view.Status)
	}
}

func TestPlatformRejectionRecorded(t *testing.T) {
	ctx := context.Background()
	platform := platformadapter.NewDeterministic().WithRejected("linkedin", "повторяющаяся публикация")
	slice := newSlice(t, platform)
	if _, err := slice.RequestPublication.Handle(ctx, request(t)); err != nil {
		t.Fatalf("RequestPublication: %v", err)
	}
	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-1"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	result, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if result.State.Status != domain.StatusFailed || result.FailureReason == "" {
		t.Fatalf("отказ площадки не зафиксирован: %+v", result)
	}
	if len(result.Events) != 0 {
		t.Fatal("отказ площадки не должен порождать material-published")
	}
}

func TestWorkerRelayPublishesEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	publisher := memory.NewPublisher()
	slice, err := publishing.Build(publishing.Config{
		Platform:  platformadapter.NewDeterministic(),
		Digest:    digestMemory(t),
		Clock:     fixedClock{now: now},
		IDs:       &seqIDs{},
		Publisher: publisher,
		Interval:  5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if slice.Relay == nil {
		t.Fatal("relay не собран при заданном Publisher")
	}
	if _, err := slice.RequestPublication.Handle(ctx, request(t)); err != nil {
		t.Fatalf("RequestPublication: %v", err)
	}
	if _, err := slice.Approve.Handle(ctx, command.Approve{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-1"}); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := slice.Publish.Handle(ctx, command.Publish{PublicationID: "pub-li", TenantID: "tenant-1", Actor: "operator-2"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	go func() { _ = slice.Relay.Run(ctx) }()

	deadline := time.After(2 * time.Second)
	for {
		if len(publisher.Messages()) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("событие не доставлено relay")
		case <-time.After(5 * time.Millisecond):
		}
	}
	message := publisher.Messages()[0]
	if message.RoutingKey != "material-published.v1" {
		t.Fatalf("ключ маршрутизации = %q, ожидался material-published.v1", message.RoutingKey)
	}
	if message.Headers["event_id"] == "" || message.Headers["tenant_id"] != "tenant-1" {
		t.Fatalf("заголовки конверта неполны: %+v", message.Headers)
	}
}

func digestMemory(t *testing.T) *digestadapter.Memory {
	t.Helper()
	digest := digestadapter.NewMemory()
	if err := digest.Put(domain.Digest{Ref: digestRef(), Topics: []string{"тема"}}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	return digest
}
