package domain

import (
	"errors"
	"testing"
	"time"
)

// testLocale возвращает корректную локаль для тестов.
func testLocale(t *testing.T) ContentLocale {
	t.Helper()
	locale, err := NewContentLocale("ru", "RU")
	if err != nil {
		t.Fatalf("NewContentLocale: %v", err)
	}
	return locale
}

// newTestPackage создаёт пакет с первой версией.
func newTestPackage(t *testing.T) *ContentPackage {
	t.Helper()
	pkg, err := NewContentPackage(NewContentPackageParams{
		ID:        "asset-1",
		TenantID:  "tenant-1",
		Locale:    testLocale(t),
		VersionID: "ver-1",
		Body:      "Первый текст.",
		Actor:     "actor-1",
		At:        time.Unix(1000, 0).UTC(),
		EventID:   "evt-1",
	})
	if err != nil {
		t.Fatalf("NewContentPackage: %v", err)
	}
	return pkg
}

func TestNewContentPackageRejectsEmptyBody(t *testing.T) {
	_, err := NewContentPackage(NewContentPackageParams{
		ID:        "asset-1",
		TenantID:  "tenant-1",
		Locale:    testLocale(t),
		VersionID: "ver-1",
		Body:      "   ",
		Actor:     "actor-1",
		At:        time.Unix(1000, 0).UTC(),
		EventID:   "evt-1",
	})
	if !errors.Is(err, ErrEmptyContent) {
		t.Fatalf("ожидалась ErrEmptyContent, получено %v", err)
	}
}

func TestNewContentPackageEmitsVersionCreated(t *testing.T) {
	pkg := newTestPackage(t)
	if pkg.Version() != 1 {
		t.Fatalf("версия пакета: %d, ожидалось 1", pkg.Version())
	}
	events := pkg.DomainEvents()
	if len(events) != 1 {
		t.Fatalf("событий: %d, ожидалось 1", len(events))
	}
	event, ok := events[0].(ContentVersionCreated)
	if !ok {
		t.Fatalf("тип события: %T", events[0])
	}
	if event.Name() != EventContentVersionCreated {
		t.Fatalf("имя события: %q", event.Name())
	}
	if event.AssetID != "asset-1" || event.VersionID != "ver-1" || event.Revision != 1 {
		t.Fatalf("событие неполно: %+v", event)
	}
	if event.Locale != "ru-RU" {
		t.Fatalf("локаль события: %q", event.Locale)
	}
}

func TestPublishVersionIsImmutable(t *testing.T) {
	pkg := newTestPackage(t)
	before := pkg.Versions()

	version, err := pkg.PublishVersion(PublishVersionInput{
		VersionID: "ver-2",
		Body:      "Второй текст.",
		Actor:     "actor-1",
		At:        time.Unix(2000, 0).UTC(),
		EventID:   "evt-2",
	})
	if err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	if version.Revision() != 2 {
		t.Fatalf("ревизия: %d, ожидалось 2", version.Revision())
	}

	after := pkg.Versions()
	// Политика immutable-content-version: прежние версии не изменились.
	if !PreservesPublishedVersions(before, after) {
		t.Fatal("политика immutable-content-version нарушена")
	}
	// Прежняя версия недоступна для правки: её текст остаётся прежним.
	first, ok := pkg.FindVersion("ver-1")
	if !ok || first.Body() != "Первый текст." {
		t.Fatalf("первая версия изменилась: %+v", first)
	}
	if got := pkg.Version(); got != 2 {
		t.Fatalf("версия пакета: %d, ожидалось 2", got)
	}
	events := pkg.DomainEvents()
	if len(events) != 2 {
		t.Fatalf("событий: %d, ожидалось 2", len(events))
	}
	last, ok := events[len(events)-1].(ContentVersionCreated)
	if !ok || last.VersionID != "ver-2" || last.Revision != 2 {
		t.Fatalf("событие второй версии не зафиксировано: %+v", last)
	}
}

func TestAttachLandingContentRequiresKnownVersion(t *testing.T) {
	pkg := newTestPackage(t)
	section, err := NewLandingSection("Заголовок", "Текст блока.")
	if err != nil {
		t.Fatalf("NewLandingSection: %v", err)
	}
	landing, err := NewLandingContent("lc-1", "ver-1", "Лендинг", "Заголовок", "Вводный абзац", []LandingSection{section})
	if err != nil {
		t.Fatalf("NewLandingContent: %v", err)
	}
	if err := pkg.AttachLandingContent(landing); err != nil {
		t.Fatalf("AttachLandingContent: %v", err)
	}
	if _, ok := pkg.LandingContent(); !ok {
		t.Fatal("наполнение лендинга не закреплено")
	}

	unknown, err := NewLandingContent("lc-2", "ver-unknown", "Лендинг", "Заголовок", "Ввод", []LandingSection{section})
	if err != nil {
		t.Fatalf("NewLandingContent: %v", err)
	}
	if err := pkg.AttachLandingContent(unknown); !errors.Is(err, ErrVersionNotFound) {
		t.Fatalf("ожидалась ErrVersionNotFound, получено %v", err)
	}
}

func TestContentPackageRehydrateRoundTrip(t *testing.T) {
	pkg := newTestPackage(t)
	if _, err := pkg.PublishVersion(PublishVersionInput{
		VersionID: "ver-2", Body: "Второй.", Actor: "actor-1", At: time.Unix(2000, 0).UTC(), EventID: "evt-2",
	}); err != nil {
		t.Fatalf("PublishVersion: %v", err)
	}
	section, _ := NewLandingSection("Блок", "Текст.")
	landing, _ := NewLandingContent("lc-1", "ver-2", "Лендинг", "Заголовок", "Ввод", []LandingSection{section})
	if err := pkg.AttachLandingContent(landing); err != nil {
		t.Fatalf("AttachLandingContent: %v", err)
	}

	restored, err := RehydrateContentPackage(pkg.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateContentPackage: %v", err)
	}
	if len(restored.Versions()) != 2 {
		t.Fatalf("версий после восстановления: %d", len(restored.Versions()))
	}
	if _, ok := restored.LandingContent(); !ok {
		t.Fatal("наполнение лендинга потеряно при восстановлении")
	}
	if restored.Version() != pkg.Version() {
		t.Fatalf("версия пакета: %d ≠ %d", restored.Version(), pkg.Version())
	}
	// Восстановление не эмитит события.
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление эмитило события")
	}
}
