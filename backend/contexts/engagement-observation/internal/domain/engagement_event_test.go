package domain

import (
	"errors"
	"testing"
	"time"
)

func validSignal() RawSignal {
	return RawSignal{
		SignalID:   "sig-1",
		Source:     "provider",
		TenantID:   "tenant-1",
		CompanyID:  "company-1",
		KindHint:   "delivery",
		Outcome:    "delivered",
		OccurredAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestQualifyDelivery(t *testing.T) {
	signal := validSignal()
	event, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1"})
	if err != nil {
		t.Fatalf("Qualify: %v", err)
	}
	if event.Kind() != KindDelivery {
		t.Fatalf("Kind = %q", event.Kind())
	}
	if event.Outcome() != OutcomeDelivered {
		t.Fatalf("Outcome = %q", event.Outcome())
	}
	if event.Confidence() != ConfidenceHigh {
		t.Fatalf("Confidence = %q, ожидалась высокая (источник provider)", event.Confidence())
	}
	if event.Version() != 1 {
		t.Fatalf("Version = %d", event.Version())
	}
	events := event.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(events))
	}
	observed, ok := events[0].(EngagementObserved)
	if !ok {
		t.Fatalf("событие %T, ожидалось EngagementObserved", events[0])
	}
	if observed.Name() != EventEngagementObserved {
		t.Fatalf("имя события = %q", observed.Name())
	}
	if observed.CompanyID != "company-1" {
		t.Fatalf("company_id = %q", observed.CompanyID)
	}
}

func TestQualifyTransitionRequiresLink(t *testing.T) {
	signal := validSignal()
	signal.KindHint = "transition"
	signal.Outcome = "clicked"
	signal.LinkRef = "lnk-1"
	signal.Target = "https://example.com/landing"
	event, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1", Confidence: ConfidenceHigh})
	if err != nil {
		t.Fatalf("Qualify: %v", err)
	}
	if event.Kind() != KindTransition || event.Outcome() != OutcomeTransitioned {
		t.Fatalf("вид/исход = %q/%q", event.Kind(), event.Outcome())
	}
	if event.LinkRef() != "lnk-1" || event.Target() != "https://example.com/landing" {
		t.Fatalf("ссылка/адрес = %q/%q", event.LinkRef(), event.Target())
	}

	missingRef := signal
	missingRef.LinkRef = ""
	if _, err := Qualify(QualifyParams{ID: "evt-2", Signal: missingRef, EventID: "event-2"}); !errors.Is(err, ErrMissingLinkRef) {
		t.Fatalf("без ссылки ошибка = %v, ожидалась ErrMissingLinkRef", err)
	}

	missingTarget := signal
	missingTarget.Target = ""
	if _, err := Qualify(QualifyParams{ID: "evt-3", Signal: missingTarget, EventID: "event-3"}); !errors.Is(err, ErrMissingTarget) {
		t.Fatalf("без адреса ошибка = %v, ожидалась ErrMissingTarget", err)
	}
}

// TestQualifyRejectsPixels проверяет проектное требование: пиксельные сигналы не
// квалифицируются (policy pixel-free-observation).
func TestQualifyRejectsPixels(t *testing.T) {
	for _, hint := range []string{"open", "opened", "pixel", "view", "viewed", "impression", "read"} {
		signal := validSignal()
		signal.KindHint = hint
		_, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1"})
		if !errors.Is(err, ErrPixelBasedKind) {
			t.Fatalf("вид %q: ошибка = %v, ожидалась ErrPixelBasedKind", hint, err)
		}
	}
}

func TestParseEngagementKindRejectsUnknown(t *testing.T) {
	if _, err := ParseEngagementKind("purchase"); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownKind", err)
	}
}

func TestQualifyRejectsUnknownOutcome(t *testing.T) {
	signal := validSignal()
	signal.Outcome = "maybe"
	if _, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1"}); !errors.Is(err, ErrUnknownOutcome) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownOutcome", err)
	}
}

func TestQualifyRejectsUnknownConfidence(t *testing.T) {
	signal := validSignal()
	if _, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1", Confidence: "certain"}); !errors.Is(err, ErrUnknownConfidence) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnknownConfidence", err)
	}
}

func TestConfidenceForSource(t *testing.T) {
	cases := map[string]SignalConfidence{
		"provider":         ConfidenceHigh,
		"message-delivery": ConfidenceMedium,
		"":                 ConfidenceLow,
		"unknown":          ConfidenceLow,
	}
	for source, want := range cases {
		if got := ConfidenceForSource(source); got != want {
			t.Fatalf("ConfidenceForSource(%q) = %q, ожидалось %q", source, got, want)
		}
	}
}

func TestQualifyRequiresTime(t *testing.T) {
	signal := validSignal()
	signal.OccurredAt = time.Time{}
	if _, err := Qualify(QualifyParams{ID: "evt-1", Signal: signal, EventID: "event-1"}); !errors.Is(err, ErrMissingTime) {
		t.Fatalf("ошибка = %v, ожидалась ErrMissingTime", err)
	}
}

func TestRehydrateRoundTrip(t *testing.T) {
	event, err := Qualify(QualifyParams{ID: "evt-1", Signal: validSignal(), EventID: "event-1"})
	if err != nil {
		t.Fatalf("Qualify: %v", err)
	}
	restored, err := Rehydrate(event.Snapshot())
	if err != nil {
		t.Fatalf("Rehydrate: %v", err)
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatalf("восстановление не должно эмитить события")
	}
	if restored.Snapshot() != event.Snapshot() {
		t.Fatalf("снимок изменился: %+v vs %+v", restored.Snapshot(), event.Snapshot())
	}
}

func TestRehydrateRejectsInvalid(t *testing.T) {
	snapshot := EngagementSnapshot{
		ID: "evt-1", TenantID: "tenant-1", CompanyID: "company-1",
		Kind: KindDelivery, Confidence: ConfidenceHigh, Outcome: OutcomeDelivered,
		OccurredAt: time.Now(), Version: 1,
	}
	if _, err := Rehydrate(snapshot); err != nil {
		t.Fatalf("корректный снимок отвергнут: %v", err)
	}
	broken := snapshot
	broken.Version = 0
	if _, err := Rehydrate(broken); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
	pixel := snapshot
	pixel.Kind = "open"
	if _, err := Rehydrate(pixel); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("пиксельный вид: ошибка = %v, ожидалась ErrInvalidSnapshot", err)
	}
}

func TestSignalDedupKey(t *testing.T) {
	withID := validSignal()
	withID.SignalID = "sig-1"
	if got := withID.DedupKey(); got != "signal:sig-1" {
		t.Fatalf("DedupKey = %q", got)
	}
	withoutID := validSignal()
	withoutID.SignalID = ""
	if got := withoutID.DedupKey(); got == "" {
		t.Fatalf("содержательный ключ дедупликации пуст")
	}
}
