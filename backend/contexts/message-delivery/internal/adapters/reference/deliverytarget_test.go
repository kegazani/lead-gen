package reference_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/adapters/reference"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// fakeResolver — подмена контракта delivery-target: возвращает заданный исход и
// считает вызовы.
type fakeResolver struct {
	recipient deliverytarget.Recipient
	err       error
	calls     int
}

func (f *fakeResolver) ResolveRecipient(context.Context, string, string) (deliverytarget.Recipient, error) {
	f.calls++
	return f.recipient, f.err
}

// Адрес владельца возвращается как есть — адаптер не подменяет значение.
func TestRecipientResolverReturnsOwnerAddress(t *testing.T) {
	owner := &fakeResolver{recipient: deliverytarget.Recipient{Address: "lead@example.com"}}
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	address, err := adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if address != "lead@example.com" {
		t.Fatalf("адрес = %q, ожидался lead@example.com", address)
	}
	if owner.calls != 1 {
		t.Fatalf("владелец вызван %d раз, ожидался 1", owner.calls)
	}
}

// Доменный отказ владельца контракта переводится в неразрешимую ссылку порта.
func TestRecipientResolverMapsRejected(t *testing.T) {
	owner := &fakeResolver{err: deliverytarget.ErrRejected}
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	_, err = adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if !errors.Is(err, ports.ErrReferenceUnresolved) {
		t.Fatalf("ошибка = %v, ожидалась ErrReferenceUnresolved", err)
	}
	if errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatal("доменный отказ не должен выдаваться за недоступность")
	}
}

// Недоступность владельца переводится в недоступность резолвера.
func TestRecipientResolverMapsUnavailable(t *testing.T) {
	owner := &fakeResolver{err: deliverytarget.ErrUnavailable}
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	_, err = adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if !errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrResolverUnavailable", err)
	}
}

// Неизвестная ошибка владельца (например, сбой чтения) — честная недоступность,
// а не неразрешимая ссылка.
func TestRecipientResolverMapsUnknownToUnavailable(t *testing.T) {
	owner := &fakeResolver{err: errors.New("сбой чтения хранилища")}
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	_, err = adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if !errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrResolverUnavailable", err)
	}
	if errors.Is(err, ports.ErrReferenceUnresolved) {
		t.Fatal("сбой транспорта не должен выдаваться за неразрешимую ссылку")
	}
}

// Отмена и дедлайн пробрасываются как есть, чтобы вызывающий различал их.
func TestRecipientResolverPassesContextErrors(t *testing.T) {
	owner := &fakeResolver{err: context.DeadlineExceeded}
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	_, err = adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка = %v, ожидался context.DeadlineExceeded", err)
	}
}

// Реальный in-proc адаптер контракта: доменный отказ владельца (неизвестная
// ссылка) доходит до порта как неразрешимая ссылка.
func TestRecipientResolverWithInProcRejectedRef(t *testing.T) {
	owner := deliverytarget.InProc(deliverytarget.InProcHandler{
		ResolveFunc: func(context.Context, string, string) (deliverytarget.Recipient, error) {
			return deliverytarget.Recipient{}, deliverytarget.ErrRejected
		},
	})
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	_, err = adapter.ResolveRecipient(context.Background(), "tenant-1", "missing")
	if !errors.Is(err, ports.ErrReferenceUnresolved) {
		t.Fatalf("ошибка = %v, ожидалась ErrReferenceUnresolved", err)
	}
}

// Реальный in-proc адаптер контракта: успешное разрешение отдаёт адрес.
func TestRecipientResolverWithInProcResolves(t *testing.T) {
	owner := deliverytarget.InProc(deliverytarget.InProcHandler{
		ResolveFunc: func(_ context.Context, tenantID, ref string) (deliverytarget.Recipient, error) {
			if tenantID != "tenant-1" || ref != "ref-1" {
				return deliverytarget.Recipient{}, deliverytarget.ErrRejected
			}
			return deliverytarget.Recipient{Address: "lead@example.com"}, nil
		},
	})
	adapter, err := reference.NewRecipientResolver(owner)
	if err != nil {
		t.Fatalf("NewRecipientResolver: %v", err)
	}
	address, err := adapter.ResolveRecipient(context.Background(), "tenant-1", "ref-1")
	if err != nil {
		t.Fatalf("ResolveRecipient: %v", err)
	}
	if address != "lead@example.com" {
		t.Fatalf("адрес = %q", address)
	}
}

// Незаданный владелец — ошибка конфигурации, а не молчаливый адаптер.
func TestNewRecipientResolverRejectsNilOwner(t *testing.T) {
	if _, err := reference.NewRecipientResolver(nil); err == nil {
		t.Fatal("сборка без реализации владельца должна отвергаться")
	}
}
