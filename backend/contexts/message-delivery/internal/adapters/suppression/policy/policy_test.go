package policy

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/contracts/data-governance/suppressionpolicy"
)

// fakePort — подмена контракта suppression-policy: фиксирует запрос и отвечает
// заданным решением или ошибкой.
type fakePort struct {
	query    suppressionpolicy.SuppressionQuery
	decision suppressionpolicy.SuppressionDecision
	err      error
}

func (p *fakePort) Check(_ context.Context, q suppressionpolicy.SuppressionQuery) (suppressionpolicy.SuppressionDecision, error) {
	p.query = q
	return p.decision, p.err
}

func testAddress(t *testing.T) domain.DeliveryAddress {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return address
}

func TestNew_RequiresPort(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("без порта suppression-policy сборка должна отвергаться")
	}
}

func TestIsSuppressed_PassesTenantAndContact(t *testing.T) {
	port := &fakePort{decision: suppressionpolicy.SuppressionDecision{Suppressed: true}}
	list, err := New(port)
	if err != nil {
		t.Fatal(err)
	}
	suppressed, err := list.IsSuppressed(context.Background(), domain.TenantID("tenant-1"), testAddress(t))
	if err != nil {
		t.Fatalf("IsSuppressed: %v", err)
	}
	if !suppressed {
		t.Fatal("решение о подавлении должно быть передано")
	}
	if port.query.TenantID != "tenant-1" || port.query.Contact != "lead@example.com" {
		t.Fatalf("запрос к владельцу: %+v", port.query)
	}
}

func TestIsSuppressed_UnavailableIsError(t *testing.T) {
	port := &fakePort{err: suppressionpolicy.ErrUnavailable}
	list, err := New(port)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := list.IsSuppressed(context.Background(), domain.TenantID("tenant-1"), testAddress(t)); err == nil {
		t.Fatal("недоступность стоп-листа должна быть ошибкой, а не «не заблокирован»")
	} else if !errors.Is(err, suppressionpolicy.ErrUnavailable) {
		t.Fatalf("ошибка должна оборачивать причину: %v", err)
	}
}
