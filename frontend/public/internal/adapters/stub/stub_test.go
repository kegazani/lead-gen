package stub_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/contracts/content-production/renderedartifact"
	"lead-gen/contracts/engagement-observation/readsignal"
	"lead-gen/frontend/public/internal/adapters/stub"
	"lead-gen/frontend/public/internal/ports"
)

const demoRef = ports.PageRef("demo")

func TestLandingSourceServesDemoLanding(t *testing.T) {
	source := stub.NewLandingSource()
	page, err := source.Landing(context.Background(), demoRef)
	if err != nil {
		t.Fatalf("Landing: неожиданная ошибка: %v", err)
	}
	if page.Ref != demoRef || page.Headline == "" || len(page.Sections) == 0 || page.FormRef == "" {
		t.Fatalf("демонстрационный лендинг неполон: %+v", page)
	}
}

func TestLandingSourceReturnsNotFound(t *testing.T) {
	source := stub.NewLandingSource()
	if _, err := source.Landing(context.Background(), "unknown"); !errors.Is(err, renderedartifact.ErrNotFound) {
		t.Fatalf("ожидалась renderedartifact.ErrNotFound, получено %v", err)
	}
}

func TestLandingSourceCopiesSections(t *testing.T) {
	source := stub.NewLandingSource()
	first, err := source.Landing(context.Background(), demoRef)
	if err != nil {
		t.Fatalf("Landing: неожиданная ошибка: %v", err)
	}
	first.Sections[0].Heading = "подменено"
	second, err := source.Landing(context.Background(), demoRef)
	if err != nil {
		t.Fatalf("Landing: неожиданная ошибка: %v", err)
	}
	if second.Sections[0].Heading == "подменено" {
		t.Error("правка результата изменила состояние заглушки")
	}
}

func TestLandingSourceHonoursCancellation(t *testing.T) {
	source := stub.NewLandingSource()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := source.Landing(cancelled, demoRef); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено %v", err)
	}
}

func TestPresentationSourceServesDemoDeck(t *testing.T) {
	source := stub.NewPresentationSource()
	deck, err := source.Presentation(context.Background(), demoRef)
	if err != nil {
		t.Fatalf("Presentation: неожиданная ошибка: %v", err)
	}
	if deck.Title == "" || len(deck.Slides) == 0 || len(deck.Slides[0].Bullets) == 0 {
		t.Fatalf("демонстрационная презентация неполна: %+v", deck)
	}
	if _, err := source.Presentation(context.Background(), "unknown"); !errors.Is(err, renderedartifact.ErrNotFound) {
		t.Fatalf("ожидалась renderedartifact.ErrNotFound, получено %v", err)
	}
}

func TestSubmissionSinkKeepsSubmission(t *testing.T) {
	sink := stub.NewSubmissionSink()
	submission := ports.Submission{
		Form:       "demo",
		Name:       "Иван",
		Contact:    "ivan@example.com",
		Message:    "Здравствуйте",
		Consent:    true,
		ReceivedAt: time.Now().UTC(),
	}
	if err := sink.Submit(context.Background(), submission); err != nil {
		t.Fatalf("Submit: неожиданная ошибка: %v", err)
	}
	received := sink.Submissions()
	if len(received) != 1 || received[0].Name != "Иван" {
		t.Fatalf("заявка не сохранена: %+v", received)
	}

	// Возвращается копия: вызывающий не может править состояние заглушки.
	received[0].Name = "подменено"
	if again := sink.Submissions(); again[0].Name != "Иван" {
		t.Error("правка результата изменила состояние заглушки")
	}
}

func TestSubmissionSinkHonoursCancellation(t *testing.T) {
	sink := stub.NewSubmissionSink()
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if err := sink.Submit(cancelled, ports.Submission{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ожидалась context.Canceled, получено %v", err)
	}
	if len(sink.Submissions()) != 0 {
		t.Error("отменённая заявка всё равно принята")
	}
}

func TestTrackingResolverResolvesAndRecords(t *testing.T) {
	resolver := stub.NewTrackingResolver()
	target, err := resolver.Resolve(context.Background(), ports.LinkRef("demo"))
	if err != nil {
		t.Fatalf("Resolve: неожиданная ошибка: %v", err)
	}
	if target == "" {
		t.Fatal("пустой адрес перехода")
	}

	transition := ports.Transition{
		Link:       ports.LinkRef("demo"),
		Target:     target,
		OccurredAt: time.Now().UTC(),
	}
	if err := resolver.Record(context.Background(), transition); err != nil {
		t.Fatalf("Record: неожиданная ошибка: %v", err)
	}
	recorded := resolver.Transitions()
	if len(recorded) != 1 || recorded[0].Target != target {
		t.Fatalf("переход не записан: %+v", recorded)
	}
}

func TestTrackingResolverReturnsNotFound(t *testing.T) {
	resolver := stub.NewTrackingResolver()
	if _, err := resolver.Resolve(context.Background(), "unknown"); !errors.Is(err, readsignal.ErrNotFound) {
		t.Fatalf("ожидалась readsignal.ErrNotFound, получено %v", err)
	}
}

func TestTrackingResolverAddLink(t *testing.T) {
	resolver := stub.NewTrackingResolver()
	resolver.AddLink("own", "/owner/page")
	target, err := resolver.Resolve(context.Background(), "own")
	if err != nil {
		t.Fatalf("Resolve: неожиданная ошибка: %v", err)
	}
	if target != "/owner/page" {
		t.Fatalf("адрес перехода: получено %q", target)
	}
}

// TestStubImplementsPorts держит совместимость заглушек с входящими портами
// edge: заглушка, переставшая удовлетворять опубликованному контракту, ломает
// сборку до того, как расхождение проявится в поведении.
func TestStubImplementsPorts(t *testing.T) {
	var (
		_ ports.LandingSource      = stub.NewLandingSource()
		_ ports.PresentationSource = stub.NewPresentationSource()
		_ ports.SubmissionSink     = stub.NewSubmissionSink()
		_ ports.TrackingResolver   = stub.NewTrackingResolver()
	)
}
