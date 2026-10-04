package contracttest

import (
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)

// at возвращает момент времени, сдвинутый на minutes минут от baseTime.
func at(minutes int) time.Time {
	return baseTime.Add(time.Duration(minutes) * time.Minute)
}

// mustLocale собирает локаль либо завершает тест.
func mustLocale(t *testing.T, language, region string) domain.ContentLocale {
	t.Helper()
	locale, err := domain.NewContentLocale(language, region)
	if err != nil {
		t.Fatalf("NewContentLocale(%q, %q): %v", language, region, err)
	}
	return locale
}

// mustPackage собирает контент-пакет с одной версией и детерминированными полями
// либо завершает тест. Идентификаторы версии и события выводятся из
// идентификатора пакета, чтобы каждая версия несла различимый event_id.
func mustPackage(t *testing.T, assetID domain.ContentAssetID) *domain.ContentPackage {
	t.Helper()
	pkg, err := domain.NewContentPackage(domain.NewContentPackageParams{
		ID:        assetID,
		TenantID:  "tenant-1",
		Locale:    mustLocale(t, "ru", "RU"),
		VersionID: domain.ContentVersionID("ver-" + assetID.String()),
		Body:      "Первый текст версии.",
		Actor:     "actor-1",
		At:        baseTime,
		EventID:   domain.EventID("evt-" + assetID.String()),
	})
	if err != nil {
		t.Fatalf("NewContentPackage(%s): %v", assetID, err)
	}
	return pkg
}

// mustJob собирает задание рендеринга в заданном состоянии либо завершает тест.
// Завершённое задание получает наполнение лендинга, чтобы артефакт прошёл
// восстановление.
func mustJob(t *testing.T, id domain.RenderJobID, state domain.RenderState) *domain.RenderJob {
	t.Helper()
	job, err := domain.NewRenderJob(domain.NewRenderJobParams{
		ID:         id,
		TenantID:   "tenant-1",
		AssetID:    "asset-1",
		VersionID:  "ver-1",
		TemplateID: "tmpl-1",
		Format:     domain.RenderFormatLandingPage,
	})
	if err != nil {
		t.Fatalf("NewRenderJob(%s): %v", id, err)
	}
	switch state {
	case domain.RenderStateRequested, "":
		return job
	case domain.RenderStateRendering:
		if err := job.Start(domain.RenderTransitionInput{Actor: "actor-1", At: at(1)}); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
	case domain.RenderStateRendered:
		if err := job.Start(domain.RenderTransitionInput{Actor: "actor-1", At: at(1)}); err != nil {
			t.Fatalf("Start(%s): %v", id, err)
		}
		section, err := domain.NewLandingSection("Заголовок", "Текст блока.")
		if err != nil {
			t.Fatalf("NewLandingSection: %v", err)
		}
		landing, err := domain.NewLandingContent(
			domain.LandingContentID("lc-"+id.String()),
			"ver-1",
			"Заголовок",
			"Главный заголовок",
			"Вступление.",
			[]domain.LandingSection{section},
		)
		if err != nil {
			t.Fatalf("NewLandingContent: %v", err)
		}
		if err := job.Complete(
			domain.RenderTransitionInput{Actor: "actor-1", At: at(2), EventID: domain.EventID("evt-" + id.String())},
			domain.NewLandingArtifact("asset-1", landing),
		); err != nil {
			t.Fatalf("Complete(%s): %v", id, err)
		}
	case domain.RenderStateFailed:
		if err := job.Fail(domain.RenderTransitionInput{Actor: "actor-1", At: at(1), Reason: "движок недоступен"}); err != nil {
			t.Fatalf("Fail(%s): %v", id, err)
		}
	default:
		t.Fatalf("неизвестное состояние задания: %q", state)
	}
	return job
}

// assertPackage сверяет загруженный пакет с ожидаемым по наблюдаемому через
// публичный API состоянию. Буфер неподтверждённых событий не сравнивается:
// события вычитываются use case и пишутся в outbox, а не входят в состояние.
func assertPackage(t *testing.T, got, want *domain.ContentPackage) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.Locale().String() != want.Locale().String() {
		t.Fatalf("Locale: получено %q, ожидалось %q", got.Locale(), want.Locale())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}

	gotVersions, wantVersions := got.Versions(), want.Versions()
	if len(gotVersions) != len(wantVersions) {
		t.Fatalf("число версий: получено %d, ожидалось %d", len(gotVersions), len(wantVersions))
	}
	for i := range wantVersions {
		assertVersion(t, i, gotVersions[i], wantVersions[i])
	}

	gotLanding, gotHasLanding := got.LandingContent()
	wantLanding, wantHasLanding := want.LandingContent()
	if gotHasLanding != wantHasLanding {
		t.Fatalf("наличие лендинга: получено %v, ожидалось %v", gotHasLanding, wantHasLanding)
	}
	if wantHasLanding && gotLanding.Title() != wantLanding.Title() {
		t.Fatalf("Landing.Title: получено %q, ожидалось %q", gotLanding.Title(), wantLanding.Title())
	}

	gotDocument, gotHasDocument := got.Document()
	wantDocument, wantHasDocument := want.Document()
	if gotHasDocument != wantHasDocument {
		t.Fatalf("наличие документа: получено %v, ожидалось %v", gotHasDocument, wantHasDocument)
	}
	if wantHasDocument && gotDocument.Body() != wantDocument.Body() {
		t.Fatalf("Document.Body: получено %q, ожидалось %q", gotDocument.Body(), wantDocument.Body())
	}
}

// assertVersion сверяет версию контента поэлементно.
func assertVersion(t *testing.T, index int, got, want domain.ContentVersion) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("Versions[%d].ID: получено %q, ожидалось %q", index, got.ID(), want.ID())
	}
	if got.Revision() != want.Revision() {
		t.Fatalf("Versions[%d].Revision: получено %d, ожидалось %d", index, got.Revision(), want.Revision())
	}
	if got.Body() != want.Body() {
		t.Fatalf("Versions[%d].Body: получено %q, ожидалось %q", index, got.Body(), want.Body())
	}
	if got.Locale().String() != want.Locale().String() {
		t.Fatalf("Versions[%d].Locale: получено %q, ожидалось %q", index, got.Locale(), want.Locale())
	}
	if got.CreatedBy() != want.CreatedBy() {
		t.Fatalf("Versions[%d].CreatedBy: получено %q, ожидалось %q", index, got.CreatedBy(), want.CreatedBy())
	}
	if !got.CreatedAt().Equal(want.CreatedAt()) {
		t.Fatalf("Versions[%d].CreatedAt: получено %s, ожидалось %s", index, got.CreatedAt(), want.CreatedAt())
	}
}

// assertJob сверяет загруженное задание с ожидаемым по наблюдаемому через
// публичный API состоянию. Артефакт сравнивается по формату и ссылке.
func assertJob(t *testing.T, got, want *domain.RenderJob) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.AssetID() != want.AssetID() {
		t.Fatalf("AssetID: получено %q, ожидалось %q", got.AssetID(), want.AssetID())
	}
	if got.VersionID() != want.VersionID() {
		t.Fatalf("VersionID: получено %q, ожидалось %q", got.VersionID(), want.VersionID())
	}
	if got.TemplateID() != want.TemplateID() {
		t.Fatalf("TemplateID: получено %q, ожидалось %q", got.TemplateID(), want.TemplateID())
	}
	if got.Format() != want.Format() {
		t.Fatalf("Format: получено %q, ожидалось %q", got.Format(), want.Format())
	}
	if got.State() != want.State() {
		t.Fatalf("State: получено %q, ожидалось %q", got.State(), want.State())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}
	if got.Reason() != want.Reason() {
		t.Fatalf("Reason: получено %q, ожидалось %q", got.Reason(), want.Reason())
	}

	gotArtifact, gotHasArtifact := got.Artifact()
	wantArtifact, wantHasArtifact := want.Artifact()
	if gotHasArtifact != wantHasArtifact {
		t.Fatalf("наличие артефакта: получено %v, ожидалось %v", gotHasArtifact, wantHasArtifact)
	}
	if wantHasArtifact {
		if gotArtifact.Format() != wantArtifact.Format() {
			t.Fatalf("Artifact.Format: получено %q, ожидалось %q", gotArtifact.Format(), wantArtifact.Format())
		}
		if gotArtifact.Ref() != wantArtifact.Ref() {
			t.Fatalf("Artifact.Ref: получено %q, ожидалось %q", gotArtifact.Ref(), wantArtifact.Ref())
		}
	}
}
