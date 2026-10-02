package command

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
	"lead-gen/backend/contexts/community-intelligence/internal/ports"
)

// fakeRegistry — реестр разрешённых сообществ.
type fakeRegistry struct {
	source domain.CommunitySource
	err    error
}

func (f fakeRegistry) FindByID(context.Context, domain.TenantID, domain.CommunityID) (domain.CommunitySource, error) {
	if f.err != nil {
		return domain.CommunitySource{}, f.err
	}
	return f.source, nil
}

// readerFunc — источник чтения, задаваемый функцией.
type readerFunc func(context.Context, ports.ReadRequest) (ports.ReadResult, error)

func (f readerFunc) ReadCommunity(ctx context.Context, req ports.ReadRequest) (ports.ReadResult, error) {
	return f(ctx, req)
}

// fakeDigests — хранилище выжимок в памяти.
type fakeDigests struct {
	saved []*domain.CommunityDigest
}

func (f *fakeDigests) Save(_ context.Context, digest *domain.CommunityDigest) error {
	f.saved = append(f.saved, digest)
	return nil
}

func (f *fakeDigests) FindByID(context.Context, domain.DigestID) (*domain.CommunityDigest, error) {
	return nil, ports.ErrDigestNotFound
}

func (f *fakeDigests) ListByCommunity(context.Context, domain.CommunityID) ([]*domain.CommunityDigest, error) {
	return nil, nil
}

// fakeOutbox — outbox событий в памяти.
type fakeOutbox struct {
	events []domain.DomainEvent
}

func (f *fakeOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	f.events = append(f.events, events...)
	return nil
}

// fixedClock — часы с фиксированным моментом.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

// seqIDs — детерминированный генератор идентификаторов.
type seqIDs struct{ n int }

func (g *seqIDs) NewEventID() domain.EventID { return domain.EventID("e-" + itoa(g.next())) }

func (g *seqIDs) NewDigestID() domain.DigestID { return domain.DigestID("d-" + itoa(g.next())) }

func (g *seqIDs) next() int {
	g.n++
	return g.n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func newTestSource(t *testing.T) domain.CommunitySource {
	t.Helper()
	source, err := domain.NewCommunitySource(domain.NewCommunitySourceParams{
		ID:         "c-1",
		Title:      "Сообщество",
		Kind:       domain.SourceUserAccount,
		Authorized: true,
	})
	if err != nil {
		t.Fatalf("NewCommunitySource: %v", err)
	}
	return source
}

func testWindow() (time.Time, time.Time) {
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	return from, from.Add(24 * time.Hour)
}

func TestReadCommunityBuildsAnonymizedDigest(t *testing.T) {
	source := newTestSource(t)
	digests := &fakeDigests{}
	outbox := &fakeOutbox{}
	ids := &seqIDs{}
	from, to := testWindow()

	reader := readerFunc(func(_ context.Context, req ports.ReadRequest) (ports.ReadResult, error) {
		if req.Source.ID() != "c-1" {
			t.Fatalf("источник запроса = %q", req.Source.ID())
		}
		return ports.ReadResult{
			CommunitySize:         120,
			ParticipantsAvailable: true,
			Messages: []ports.ReadMessage{
				{MessageID: "m-1", Author: "author-1", Text: "нужна цена, пишите @ivan_petrov"},
				{MessageID: "m-2", Author: "author-2", Text: "нужна цена, пишите @ivan_petrov"},
			},
		}, nil
	})

	handler := NewReadCommunityHandler(
		fakeRegistry{source: source}, reader, digests, outbox, fixedClock{now: to}, ids, 10,
	)
	result, err := handler.Handle(context.Background(), ReadCommunity{
		CommunityID: "c-1",
		TenantID:    "t-1",
		From:        from,
		To:          to,
	})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if result.TopicCount != 1 {
		t.Fatalf("тем = %d, ожидалось 1", result.TopicCount)
	}
	if result.DigestID == "" || len(digests.saved) != 1 {
		t.Fatal("выжимка не сохранена")
	}
	if len(outbox.events) != 1 {
		t.Fatalf("событий в outbox = %d, ожидалось 1", len(outbox.events))
	}
	if result.Events[0].Name() != domain.EventCommunityDigestCreated {
		t.Fatalf("имя события %q", result.Events[0].Name())
	}

	quote := digests.saved[0].Topics()[0].Quotes()[0].Text()
	if strings.Contains(quote, "ivan_petrov") {
		t.Fatalf("цитата содержит автора: %q", quote)
	}
}

func TestReadCommunityRejectsUnknownSource(t *testing.T) {
	handler := NewReadCommunityHandler(
		fakeRegistry{err: ports.ErrCommunityNotFound},
		readerFunc(func(context.Context, ports.ReadRequest) (ports.ReadResult, error) {
			t.Fatal("чтение не должно вызываться")
			return ports.ReadResult{}, nil
		}),
		&fakeDigests{}, &fakeOutbox{}, fixedClock{now: time.Now()}, &seqIDs{}, 10,
	)
	from, to := testWindow()
	_, err := handler.Handle(context.Background(), ReadCommunity{CommunityID: "c-1", TenantID: "t-1", From: from, To: to})
	if !errors.Is(err, ports.ErrCommunityNotFound) {
		t.Fatalf("ожидался ErrCommunityNotFound, получено %v", err)
	}
}

func TestReadCommunityRejectsNoMessages(t *testing.T) {
	source := newTestSource(t)
	from, to := testWindow()
	handler := NewReadCommunityHandler(
		fakeRegistry{source: source},
		readerFunc(func(context.Context, ports.ReadRequest) (ports.ReadResult, error) {
			return ports.ReadResult{CommunitySize: 120}, nil
		}),
		&fakeDigests{}, &fakeOutbox{}, fixedClock{now: to}, &seqIDs{}, 10,
	)
	_, err := handler.Handle(context.Background(), ReadCommunity{CommunityID: "c-1", TenantID: "t-1", From: from, To: to})
	if !errors.Is(err, domain.ErrNoMessages) {
		t.Fatalf("ожидался ErrNoMessages, получено %v", err)
	}
}

func TestReadCommunityPropagatesSessionRequired(t *testing.T) {
	source := newTestSource(t)
	from, to := testWindow()
	handler := NewReadCommunityHandler(
		fakeRegistry{source: source},
		readerFunc(func(context.Context, ports.ReadRequest) (ports.ReadResult, error) {
			return ports.ReadResult{}, ports.ErrSessionRequired
		}),
		&fakeDigests{}, &fakeOutbox{}, fixedClock{now: to}, &seqIDs{}, 10,
	)
	_, err := handler.Handle(context.Background(), ReadCommunity{CommunityID: "c-1", TenantID: "t-1", From: from, To: to})
	if !errors.Is(err, ports.ErrSessionRequired) {
		t.Fatalf("ожидался ErrSessionRequired, получено %v", err)
	}
}

func TestReadCommunityRejectsInvalidPeriod(t *testing.T) {
	source := newTestSource(t)
	from, to := testWindow()
	handler := NewReadCommunityHandler(
		fakeRegistry{source: source},
		readerFunc(func(context.Context, ports.ReadRequest) (ports.ReadResult, error) {
			t.Fatal("чтение не должно вызываться при некорректном периоде")
			return ports.ReadResult{}, nil
		}),
		&fakeDigests{}, &fakeOutbox{}, fixedClock{now: to}, &seqIDs{}, 10,
	)
	_, err := handler.Handle(context.Background(), ReadCommunity{CommunityID: "c-1", TenantID: "t-1", From: to, To: from})
	if !errors.Is(err, domain.ErrInvalidPeriod) {
		t.Fatalf("ожидался ErrInvalidPeriod, получено %v", err)
	}
}
