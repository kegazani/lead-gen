package observ

import (
	"context"
	"testing"
)

func TestTracerStartCarriesSpan(t *testing.T) {
	tracer := NewTracer("outreach-orchestration")

	ctx, done := tracer.Start(context.Background(), "approved-action.handle")
	if done == nil {
		t.Fatal("Start вернул nil-функцию завершения")
	}
	done(nil)

	span, ok := SpanFromContext(ctx)
	if !ok {
		t.Fatal("спан не найден в контексте")
	}
	if span.ServiceName != "outreach-orchestration" || span.Name != "approved-action.handle" {
		t.Fatalf("спан неверен: %+v", span)
	}

	if _, ok := SpanFromContext(context.Background()); ok {
		t.Fatal("в чистом контексте спана быть не должно")
	}
}

func TestTracerStartNilContext(t *testing.T) {
	// Библиотечный код не паникует на nil-контексте.
	_, done := NewTracer("s").Start(nil, "op")
	done(nil)
}

func TestFieldsRoundTrip(t *testing.T) {
	want := Fields{TenantID: "t", CorrelationID: "c", CausationID: "ca", TaskID: "task"}
	ctx := WithFields(context.Background(), want)

	got, ok := FieldsFromContext(ctx)
	if !ok || got != want {
		t.Fatalf("поля не восстановлены: %+v ok=%v", got, ok)
	}

	if _, ok := FieldsFromContext(context.Background()); ok {
		t.Fatal("в чистом контексте полей быть не должно")
	}
}
