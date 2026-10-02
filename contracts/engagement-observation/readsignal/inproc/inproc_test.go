package inproc_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/contracts/engagement-observation/readsignal/inproc"
)

func TestHandlerDelegates(t *testing.T) {
	var gotRef readsignal.LinkRef
	var gotTransition readsignal.Transition
	handler := inproc.InProc(inproc.Handler{
		ResolveFunc: func(_ context.Context, ref readsignal.LinkRef) (string, error) {
			gotRef = ref
			return "https://example.com", nil
		},
		RecordFunc: func(_ context.Context, transition readsignal.Transition) error {
			gotTransition = transition
			return nil
		},
	})

	target, err := handler.Resolve(context.Background(), "lnk-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if target != "https://example.com" || gotRef != "lnk-1" {
		t.Fatalf("target = %q, ref = %q", target, gotRef)
	}
	if err := handler.Record(context.Background(), readsignal.Transition{Link: "lnk-1"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if gotTransition.Link != "lnk-1" {
		t.Fatalf("переход = %+v", gotTransition)
	}
}

func TestHandlerUnavailable(t *testing.T) {
	handler := inproc.InProc(inproc.Handler{})
	if _, err := handler.Resolve(context.Background(), "lnk-1"); !errors.Is(err, readsignal.ErrNotFound) {
		t.Fatalf("Resolve без функции: ошибка = %v, ожидалась ErrNotFound", err)
	}
	if err := handler.Record(context.Background(), readsignal.Transition{}); !errors.Is(err, inproc.ErrUnavailable) {
		t.Fatalf("Record без функции: ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
