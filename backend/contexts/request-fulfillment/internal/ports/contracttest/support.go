// Package contracttest содержит общие contract suite портов контекста
// request-fulfillment: репозитория заявок, unit-of-work и хранилища черновиков
// писем. Suite запускается против любой реализации через фабрику и потому не
// зависит от адаптера: in-memory и PostgreSQL проверяются одним и тем же набором
// проверок (design.md, Решение 8).
//
// Пакет предназначен для использования из тестов адаптеров и импортирует только
// сам ports, его domain и журнал outbox платформы.
package contracttest

import (
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

// ManualClock — потокобезопасные внедрённые часы для общих storage-контрактов.
type ManualClock struct {
	mu  sync.RWMutex
	now time.Time
}

func NewManualClock(now time.Time) *ManualClock { return &ManualClock{now: now} }
func (c *ManualClock) Now() time.Time           { c.mu.RLock(); defer c.mu.RUnlock(); return c.now }
func (c *ManualClock) Advance(d time.Duration)  { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

// at возвращает момент времени, сдвинутый на minutes минут от baseTime.
func at(minutes int) time.Time {
	return baseTime.Add(time.Duration(minutes) * time.Minute)
}

// mustRequest собирает заявку в статусе submitted с детерминированными полями
// либо завершает тест. Идентификатор события выводится из идентификатора заявки,
// чтобы каждая заявка несла различимый event_id.
func mustRequest(t *testing.T, id domain.RequestID) *domain.Request {
	t.Helper()
	request, err := domain.NewRequest(domain.NewRequestParams{
		ID:       id,
		OfferID:  domain.OfferID("offer-" + id.String()),
		TenantID: "tenant-1",
		Content:  "Предлагаем автоматизацию найма",
		Target:   "IT-компании",
		Actor:    "actor-1",
		At:       baseTime,
		EventID:  domain.EventID("evt-" + id.String()),
	})
	if err != nil {
		t.Fatalf("создание заявки %s: %v", id, err)
	}
	return request
}

// assertRequest сверяет загруженную заявку с ожидаемой по наблюдаемому через
// публичный API состоянию. Буфер неподтверждённых событий не сравнивается:
// события вычитываются use case и пишутся в outbox, а не являются частью
// сохраняемого состояния агрегата.
func assertRequest(t *testing.T, got, want *domain.Request) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.Status() != want.Status() {
		t.Fatalf("Status: получено %q, ожидалось %q", got.Status(), want.Status())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}
	if got.Offer().ID() != want.Offer().ID() {
		t.Fatalf("Offer.ID: получено %q, ожидалось %q", got.Offer().ID(), want.Offer().ID())
	}
	if got.Offer().Content().Text() != want.Offer().Content().Text() {
		t.Fatalf("Offer.Content: получено %q, ожидалось %q", got.Offer().Content().Text(), want.Offer().Content().Text())
	}
	if got.Offer().Target().Description() != want.Offer().Target().Description() {
		t.Fatalf("Offer.Target: получено %q, ожидалось %q", got.Offer().Target().Description(), want.Offer().Target().Description())
	}
	if got.Offer().Revision() != want.Offer().Revision() {
		t.Fatalf("Offer.Revision: получено %d, ожидалось %d", got.Offer().Revision(), want.Offer().Revision())
	}
	gotTransitions, wantTransitions := got.Transitions(), want.Transitions()
	if len(gotTransitions) != len(wantTransitions) {
		t.Fatalf("число переходов: получено %d, ожидалось %d", len(gotTransitions), len(wantTransitions))
	}
	for i := range wantTransitions {
		assertTransition(t, i, gotTransitions[i], wantTransitions[i])
	}
}

// assertTransition сверяет запись журнала переходов поэлементно. Время
// сравнивается по моменту (Equal), а не по структуре, поэтому сравнение не
// зависит от JSON-сериализации таймстампов.
func assertTransition(t *testing.T, index int, got, want domain.Transition) {
	t.Helper()
	if got.From() != want.From() {
		t.Fatalf("Transitions[%d].From: получено %q, ожидалось %q", index, got.From(), want.From())
	}
	if got.To() != want.To() {
		t.Fatalf("Transitions[%d].To: получено %q, ожидалось %q", index, got.To(), want.To())
	}
	if got.Actor() != want.Actor() {
		t.Fatalf("Transitions[%d].Actor: получено %q, ожидалось %q", index, got.Actor(), want.Actor())
	}
	if !got.At().Equal(want.At()) {
		t.Fatalf("Transitions[%d].At: получено %s, ожидалось %s", index, got.At(), want.At())
	}
	if got.Reason() != want.Reason() {
		t.Fatalf("Transitions[%d].Reason: получено %q, ожидалось %q", index, got.Reason(), want.Reason())
	}
}
