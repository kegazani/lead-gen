package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestNeedsRevisionIsKnownAndNonTerminal — статус needs-revision объявлен доменом,
// входит в согласованный набор и не является терминальным.
func TestNeedsRevisionIsKnownAndNonTerminal(t *testing.T) {
	statuses := domain.RequestStatuses()
	found := false
	for _, status := range statuses {
		if status == domain.StatusNeedsRevision {
			found = true
		}
	}
	if !found {
		t.Fatalf("RequestStatuses() не содержит needs-revision: %v", statuses)
	}
	if domain.StatusNeedsRevision.IsTerminal() {
		t.Fatal("needs-revision не должен быть терминальным")
	}
}

// TestNeedsRevisionOfferEditableInPlace — оффер в needs-revision редактируется на
// месте, как до валидации.
func TestNeedsRevisionOfferEditableInPlace(t *testing.T) {
	if !domain.StatusNeedsRevision.OfferEditableInPlace() {
		t.Fatal("needs-revision должен разрешать правку оффера на месте")
	}
}

// TestReturnForRevisionTransitions — возврат на доработку допустим из validating
// и processing и запрещён из submitted и needs-revision.
func TestReturnForRevisionTransitions(t *testing.T) {
	t.Run("из validating", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.StartValidation(input(1)); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		events, err := request.ReturnForRevision(inputReason(2, "уточните цель"))
		if err != nil {
			t.Fatalf("ReturnForRevision: %v", err)
		}
		if len(events) != 1 || events[0].Name() != domain.EventRequestReturnedForRevision {
			t.Fatalf("события = %v", names(events))
		}
		if request.Status() != domain.StatusNeedsRevision {
			t.Fatalf("статус = %q, ожидался needs-revision", request.Status())
		}
	})

	t.Run("из processing", func(t *testing.T) {
		request := newRequest(t)
		toValidated(t, request)
		if _, err := request.StartProcessing(input(3)); err != nil {
			t.Fatalf("StartProcessing: %v", err)
		}
		events, err := request.ReturnForRevision(inputReason(4, "исправьте оффер"))
		if err != nil {
			t.Fatalf("ReturnForRevision: %v", err)
		}
		if len(events) != 1 || events[0].Name() != domain.EventRequestReturnedForRevision {
			t.Fatalf("события = %v", names(events))
		}
		if request.Status() != domain.StatusNeedsRevision {
			t.Fatalf("статус = %q", request.Status())
		}
	})

	t.Run("из submitted запрещён", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.ReturnForRevision(inputReason(1, "нет")); !errors.Is(err, domain.ErrInvalidTransition) {
			t.Fatalf("ошибка = %v, ожидалась ErrInvalidTransition", err)
		}
	})

	t.Run("без причины", func(t *testing.T) {
		request := newRequest(t)
		if _, err := request.StartValidation(input(1)); err != nil {
			t.Fatalf("StartValidation: %v", err)
		}
		if _, err := request.ReturnForRevision(input(2)); !errors.Is(err, domain.ErrReasonRequired) {
			t.Fatalf("ошибка = %v, ожидалась ErrReasonRequired", err)
		}
	})
}

// TestResubmitTransition — повторная подача needs-revision → submitted с событием
// request-resubmitted.
func TestResubmitTransition(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.ReturnForRevision(inputReason(2, "уточните цель")); err != nil {
		t.Fatalf("ReturnForRevision: %v", err)
	}

	events, err := request.Resubmit(input(3))
	if err != nil {
		t.Fatalf("Resubmit: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestResubmitted {
		t.Fatalf("события = %v", names(events))
	}
	if request.Status() != domain.StatusSubmitted {
		t.Fatalf("статус = %q, ожидался submitted", request.Status())
	}
}

// TestResubmitOnlyFromNeedsRevision — повторная подача допустима только из
// needs-revision.
func TestResubmitOnlyFromNeedsRevision(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.Resubmit(input(2)); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("из validating: ошибка = %v, ожидалась ErrInvalidTransition", err)
	}
}

// TestCancelFromNeedsRevision — отмена доступна из needs-revision.
func TestCancelFromNeedsRevision(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.ReturnForRevision(inputReason(2, "уточните цель")); err != nil {
		t.Fatalf("ReturnForRevision: %v", err)
	}
	events, err := request.Cancel(inputReason(3, "клиент отказался"))
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if len(events) != 1 || events[0].Name() != domain.EventRequestCancelled {
		t.Fatalf("события = %v", names(events))
	}
	if request.Status() != domain.StatusCancelled {
		t.Fatalf("статус = %q, ожидался cancelled", request.Status())
	}
}

// TestReviseOfferInNeedsRevision — правка оффера в needs-revision идёт на месте и
// не создаёт новую версию.
func TestReviseOfferInNeedsRevision(t *testing.T) {
	request := newRequest(t)
	if _, err := request.StartValidation(input(1)); err != nil {
		t.Fatalf("StartValidation: %v", err)
	}
	if _, err := request.ReturnForRevision(inputReason(2, "уточните цель")); err != nil {
		t.Fatalf("ReturnForRevision: %v", err)
	}

	offer, err := request.ReviseOffer(input(3), "offer-2", "Доработанный оффер", "SaaS-компании")
	if err != nil {
		t.Fatalf("ReviseOffer: %v", err)
	}
	if offer.Revision() != 1 {
		t.Fatalf("ревизия = %d, ожидалась 1 (правка на месте)", offer.Revision())
	}
	if request.Offer().Content().Text() != "Доработанный оффер" {
		t.Fatalf("содержимое = %q", request.Offer().Content().Text())
	}
	if len(request.OfferHistory()) != 0 {
		t.Fatalf("история = %d версий, ожидалось 0", len(request.OfferHistory()))
	}
}
