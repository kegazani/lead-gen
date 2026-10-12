package inproc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"lead-gen/contracts/outreach-orchestration/approvedaction"
)

var testInstant = time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)

func validAction() approvedaction.ApprovedAction {
	action := approvedaction.NewApprovedAction(approvedaction.Envelope{
		EventID:       "evt-1",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "camp-1",
		CausationID:   "dec-1",
	})
	action.ApprovedActionID = "draft-1@3"
	action.CampaignID = "camp-1"
	action.DraftID = "draft-1"
	action.DraftVersion = 3
	action.DecisionID = "dec-1"
	action.Channel = approvedaction.ChannelEmailName
	action.RecipientRef = "draft-1"
	action.DecidedAt = testInstant
	return action
}

// recorder — реализация владельца: считает вызовы и, по желанию, блокируется,
// игнорируя контекст. Так проверяется, что границу вызова держит адаптер, а не
// только реализация.
type recorder struct {
	mu      sync.Mutex
	calls   []approvedaction.ApprovedAction
	err     error
	release chan struct{}
}

func (r *recorder) Publish(_ context.Context, action approvedaction.ApprovedAction) error {
	r.mu.Lock()
	r.calls = append(r.calls, action)
	r.mu.Unlock()
	if r.release != nil {
		<-r.release
	}
	return r.err
}

func (r *recorder) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// Связывание отвергает отсутствующую реализацию владельца, а полный вызов
// доходит до реализации владельца без изменений.
func TestBindDelegatesToOwnerImplementation(t *testing.T) {
	if _, err := Bind(nil); err == nil {
		t.Fatal("Bind с nil-реализацией вернул nil-ошибку")
	}

	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	action := validAction()
	if err := publisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if impl.callCount() != 1 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидался 1", impl.callCount())
	}
	if got := impl.calls[0]; got != action {
		t.Fatalf("реализация получила %+v, ожидалось %+v", got, action)
	}
}

// Невалидный контракт отвергается адаптером и не доходит до владельца.
func TestPublishRejectsInvalidContract(t *testing.T) {
	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if err := publisher.Publish(context.Background(), approvedaction.ApprovedAction{}); err == nil {
		t.Fatal("пустой контракт не отвергнут")
	}
	if impl.callCount() != 0 {
		t.Fatalf("невалидный контракт дошёл до владельца: вызовов %d", impl.callCount())
	}
}

// Отмена по контексту прерывает вызов: адаптер возвращает ошибку контекста,
// даже если реализация владельца контекст не проверяет.
func TestPublishInterruptedByContextCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	impl := &recorder{release: release}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	started := time.Now()
	err = publisher.Publish(ctx, validAction())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, context.Canceled)", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("вызов прерван через %s, ожидалось быстро", elapsed)
	}
}

// Дедлайн вызова соблюдается адаптером: блокирующаяся реализация не держит
// вызывающего дольше бюджета.
func TestPublishInterruptedByDeadline(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	impl := &recorder{release: release}
	publisher, err := Bind(impl, WithTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	err = publisher.Publish(context.Background(), validAction())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, context.DeadlineExceeded)", err)
	}
}

// Повторная доставка с тем же ключом идемпотентности применяется один раз:
// второй вызов не доходит до реализации владельца.
func TestPublishAppliesOnceByIDempotencyKey(t *testing.T) {
	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	action := validAction()
	if err := publisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("первый Publish: %v", err)
	}

	// Повторная доставка: другой event_id, тот же ключ (tenant, campaign, draft, version).
	repeat := action
	repeat.EventID = "evt-2"
	if err := publisher.Publish(context.Background(), repeat); err != nil {
		t.Fatalf("повторный Publish: %v", err)
	}
	if impl.callCount() != 1 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидался 1", impl.callCount())
	}

	// Новая версия черновика — другое действие: применяется отдельно.
	next := action
	next.EventID = "evt-3"
	next.DraftVersion = 4
	next.ApprovedActionID = "draft-1@4"
	if err := publisher.Publish(context.Background(), next); err != nil {
		t.Fatalf("Publish новой версии: %v", err)
	}
	if impl.callCount() != 2 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидалось 2", impl.callCount())
	}
}

// Действие другой кампании к тому же получателю — другое действие: не считается
// повтором и применяется отдельно.
func TestPublishAppliesPerCampaign(t *testing.T) {
	impl := &recorder{}
	publisher, err := Bind(impl)
	if err != nil {
		t.Fatalf("Bind: %v", err)
	}

	first := validAction()
	if err := publisher.Publish(context.Background(), first); err != nil {
		t.Fatalf("первый Publish: %v", err)
	}

	second := validAction()
	second.EventID = "evt-2"
	second.CampaignID = "camp-2"
	second.CorrelationID = "camp-2"
	second.ApprovedActionID = approvedaction.ApprovedActionIdentity("camp-2", "draft-1", 3)
	if err := publisher.Publish(context.Background(), second); err != nil {
		t.Fatalf("Publish другой кампании: %v", err)
	}
	if impl.callCount() != 2 {
		t.Fatalf("реализация владельца вызвана %d раз, ожидалось 2", impl.callCount())
	}
}
