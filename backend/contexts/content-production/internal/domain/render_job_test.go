package domain

import (
	"errors"
	"testing"
	"time"
)

func newTestRenderJob(t *testing.T) *RenderJob {
	t.Helper()
	job, err := NewRenderJob(NewRenderJobParams{
		ID:         "job-1",
		TenantID:   "tenant-1",
		AssetID:    "asset-1",
		VersionID:  "ver-1",
		TemplateID: "tmpl-1",
		Format:     RenderFormatLandingPage,
	})
	if err != nil {
		t.Fatalf("NewRenderJob: %v", err)
	}
	return job
}

func landingArtifact(t *testing.T) RenderedArtifact {
	t.Helper()
	section, err := NewLandingSection("Заголовок", "Текст блока.")
	if err != nil {
		t.Fatalf("NewLandingSection: %v", err)
	}
	landing, err := NewLandingContent("lc-1", "ver-1", "Лендинг", "Заголовок", "Ввод", []LandingSection{section})
	if err != nil {
		t.Fatalf("NewLandingContent: %v", err)
	}
	return NewLandingArtifact("asset-1", landing)
}

func TestNewRenderJobRequiresVersionAndTemplate(t *testing.T) {
	_, err := NewRenderJob(NewRenderJobParams{
		ID: "job-1", TenantID: "tenant-1", AssetID: "asset-1", TemplateID: "tmpl-1", Format: RenderFormatLandingPage,
	})
	if !errors.Is(err, ErrVersionRequired) {
		t.Fatalf("ожидалась ErrVersionRequired, получено %v", err)
	}
	_, err = NewRenderJob(NewRenderJobParams{
		ID: "job-1", TenantID: "tenant-1", AssetID: "asset-1", VersionID: "ver-1", Format: RenderFormatLandingPage,
	})
	if !errors.Is(err, ErrTemplateRequired) {
		t.Fatalf("ожидалась ErrTemplateRequired, получено %v", err)
	}
	_, err = NewRenderJob(NewRenderJobParams{
		ID: "job-1", TenantID: "tenant-1", AssetID: "asset-1", VersionID: "ver-1", TemplateID: "tmpl-1", Format: "unknown",
	})
	if !errors.Is(err, ErrUnknownRenderFormat) {
		t.Fatalf("ожидалась ErrUnknownRenderFormat, получено %v", err)
	}
}

func TestRenderJobLifecycle(t *testing.T) {
	job := newTestRenderJob(t)
	if job.State() != RenderStateRequested {
		t.Fatalf("начальное состояние: %s", job.State())
	}
	// Рендер ссылается на точную версию и шаблон.
	if job.VersionID() != "ver-1" || job.TemplateID() != "tmpl-1" {
		t.Fatalf("задание не зафиксировало версию и шаблон: %+v", job)
	}
	if err := job.Complete(RenderTransitionInput{Actor: "a", At: time.Unix(1, 0).UTC(), EventID: "evt"}, landingArtifact(t)); !errors.Is(err, ErrInvalidRenderTransition) {
		t.Fatalf("завершение без старта: %v", err)
	}
	if err := job.Start(RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := job.Complete(RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC(), EventID: "evt-1"}, landingArtifact(t)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if job.State() != RenderStateRendered || !job.State().IsTerminal() {
		t.Fatalf("состояние после завершения: %s", job.State())
	}
	events := job.DomainEvents()
	if len(events) != 1 || events[0].Name() != EventContentRendered {
		t.Fatalf("событие завершения: %+v", events)
	}
	rendered, ok := events[0].(ContentRendered)
	if !ok {
		t.Fatalf("тип события: %T", events[0])
	}
	if rendered.VersionID != "ver-1" || rendered.TemplateID != "tmpl-1" || rendered.ArtifactRef != "asset-1" {
		t.Fatalf("событие неполно: %+v", rendered)
	}
	// Терминальное состояние не переигрывается.
	if err := job.Start(RenderTransitionInput{Actor: "worker", At: time.Unix(4, 0).UTC()}); !errors.Is(err, ErrTerminalRenderState) {
		t.Fatalf("переигровка терминального задания: %v", err)
	}
}

func TestRenderJobCompleteRejectsFormatMismatch(t *testing.T) {
	job := newTestRenderJob(t)
	if err := job.Start(RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	presentation, _ := NewPresentation("Презентация", []Slide{mustSlide(t)})
	artifact := NewPresentationArtifact("job-1", presentation)
	if err := job.Complete(RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC(), EventID: "evt-1"}, artifact); !errors.Is(err, ErrArtifactFormatMismatch) {
		t.Fatalf("ожидалась ErrArtifactFormatMismatch, получено %v", err)
	}
}

func TestRenderJobFailRequiresReason(t *testing.T) {
	job := newTestRenderJob(t)
	if err := job.Start(RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := job.Fail(RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC()}); !errors.Is(err, ErrReasonRequired) {
		t.Fatalf("ожидалась ErrReasonRequired, получено %v", err)
	}
	if err := job.Fail(RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC(), Reason: "движок отказал"}); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	if job.State() != RenderStateFailed || job.Reason() != "движок отказал" {
		t.Fatalf("состояние отказа: %s / %q", job.State(), job.Reason())
	}
}

func TestRenderJobRehydrateRoundTrip(t *testing.T) {
	job := newTestRenderJob(t)
	if err := job.Start(RenderTransitionInput{Actor: "worker", At: time.Unix(2, 0).UTC()}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := job.Complete(RenderTransitionInput{Actor: "worker", At: time.Unix(3, 0).UTC(), EventID: "evt-1"}, landingArtifact(t)); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	restored, err := RehydrateRenderJob(job.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateRenderJob: %v", err)
	}
	if restored.State() != RenderStateRendered {
		t.Fatalf("состояние после восстановления: %s", restored.State())
	}
	artifact, ok := restored.Artifact()
	if !ok || artifact.Format() != RenderFormatLandingPage {
		t.Fatalf("артефакт потерян при восстановлении: %+v", artifact)
	}
	if len(restored.DomainEvents()) != 0 {
		t.Fatal("восстановление эмитило события")
	}
}

func mustSlide(t *testing.T) Slide {
	t.Helper()
	slide, err := NewSlide("Слайд", []string{"Пункт."})
	if err != nil {
		t.Fatalf("NewSlide: %v", err)
	}
	return slide
}
