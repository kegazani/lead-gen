package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/contracts/integration-platform/vector"
)

// fakeVector — фейковый порт векторного хранилища: детерминированные ответы и
// учёт вызовов.
type fakeVector struct {
	embedErr  error
	searchErr error
	matches   []vector.Match

	upserts int
	indexed map[string]string
}

// Embed реализует vector.Port.
func (f *fakeVector) Embed(_ context.Context, text string) (vector.Vector, error) {
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	return vector.Vector{float32(len(text))}, nil
}

// Upsert реализует vector.Port.
func (f *fakeVector) Upsert(_ context.Context, document vector.Document) (vector.Vector, error) {
	if f.embedErr != nil {
		return nil, f.embedErr
	}
	f.upserts++
	if f.indexed == nil {
		f.indexed = make(map[string]string)
	}
	f.indexed[document.ID] = document.Text
	return vector.Vector{float32(len(document.Text))}, nil
}

// Search реализует vector.Port.
func (f *fakeVector) Search(context.Context, string, int) ([]vector.Match, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.matches, nil
}

// companyWith собирает карточку компании для тестов.
func companyWith(t *testing.T, id, name, description string, socials ...string) *domain.Company {
	t.Helper()
	provenance := domain.Provenance{SourceURL: "https://" + id + "/about", ExtractedAt: testTime}
	socialsValues := make([]domain.AttributedValue, 0, len(socials))
	for _, social := range socials {
		socialsValues = append(socialsValues, domain.AttributedValue{Value: social, Provenance: provenance})
	}
	company, err := domain.CreateCompany(domain.CreateCompanyParams{
		Domain:      id,
		Name:        domain.AttributedValue{Value: name, Provenance: provenance},
		Description: domain.AttributedValue{Value: description, Provenance: provenance},
		Socials:     socialsValues,
		CreatedAt:   testTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	return company
}

// TestEmbedCompanyCreatesEmbedding — сценарий «Эмбеддинг создан»: у компании
// появляется векторное представление, документ индексируется.
func TestEmbedCompanyCreatesEmbedding(t *testing.T) {
	ctx := context.Background()
	profiles := newMemProfiles()
	_ = profiles.Save(ctx, companyWith(t, "acme.example", "Acme", "разработка ПО под заказ"))
	vectors := &fakeVector{}

	useCase := &EmbedCompany{Profiles: profiles, Vector: vectors, Clock: fakeClock{testTime}}
	embedding, err := useCase.Run(ctx, EmbedCompanyInput{CompanyID: "acme.example"})
	if err != nil {
		t.Fatalf("EmbedCompany: %v", err)
	}
	if embedding.IsZero() {
		t.Fatal("эмбеддинг не сформирован")
	}
	if embedding.SourceVersion() == "" {
		t.Fatal("watermark исходных данных не задан")
	}
	if !strings.Contains(vectors.indexed["acme.example"], "Acme") {
		t.Fatalf("документ компании не проиндексирован: %q", vectors.indexed["acme.example"])
	}
}

// TestEmbedCompanySkipsFreshEmbedding проверяет, что повторный вызов без изменения
// данных не пересчитывает вектор.
func TestEmbedCompanySkipsFreshEmbedding(t *testing.T) {
	ctx := context.Background()
	profiles := newMemProfiles()
	_ = profiles.Save(ctx, companyWith(t, "acme.example", "Acme", "разработка ПО"))
	vectors := &fakeVector{}
	useCase := &EmbedCompany{Profiles: profiles, Vector: vectors, Clock: fakeClock{testTime}}

	if _, err := useCase.Run(ctx, EmbedCompanyInput{CompanyID: "acme.example"}); err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	if _, err := useCase.Run(ctx, EmbedCompanyInput{CompanyID: "acme.example"}); err != nil {
		t.Fatalf("второй вызов: %v", err)
	}
	if vectors.upserts != 1 {
		t.Fatalf("вектор пересчитан зря: upserts=%d", vectors.upserts)
	}
}

// TestEmbedCompanyRecomputesAfterChange — сценарий «Пересчёт при изменении»:
// изменившиеся данные меняют watermark, и вектор пересчитывается.
func TestEmbedCompanyRecomputesAfterChange(t *testing.T) {
	ctx := context.Background()
	profiles := newMemProfiles()
	company := companyWith(t, "acme.example", "Acme", "разработка ПО")
	_ = profiles.Save(ctx, company)
	vectors := &fakeVector{}
	useCase := &EmbedCompany{Profiles: profiles, Vector: vectors, Clock: fakeClock{testTime}}

	first, err := useCase.Run(ctx, EmbedCompanyInput{CompanyID: "acme.example"})
	if err != nil {
		t.Fatalf("первый вызов: %v", err)
	}
	company.Enrich(
		domain.AttributedValue{},
		domain.AttributedValue{},
		[]domain.AttributedValue{
			{Value: "github.com/acme", Provenance: domain.Provenance{SourceURL: "https://acme.example/about", ExtractedAt: testTime}},
		},
		testTime.Add(time.Hour),
	)

	second, err := useCase.Run(ctx, EmbedCompanyInput{CompanyID: "acme.example"})
	if err != nil {
		t.Fatalf("пересчёт: %v", err)
	}
	if first.SourceVersion() == second.SourceVersion() {
		t.Fatal("watermark не изменился при изменении данных")
	}
	if vectors.upserts != 2 {
		t.Fatalf("пересчёт не выполнен: upserts=%d", vectors.upserts)
	}
}

// TestEmbedCompanyWithoutVector отвергает работу без векторного хранилища.
func TestEmbedCompanyWithoutVector(t *testing.T) {
	profiles := newMemProfiles()
	useCase := &EmbedCompany{Profiles: profiles}
	if _, err := useCase.Run(context.Background(), EmbedCompanyInput{CompanyID: "acme.example"}); !errors.Is(err, ErrVectorUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVectorUnavailable", err)
	}
}

// TestSemanticRecallOrdersCandidates — сценарий «Поиск кандидатов»: список
// упорядочен по убыванию близости, вердикта в нём нет.
func TestSemanticRecallOrdersCandidates(t *testing.T) {
	vectors := &fakeVector{matches: []vector.Match{
		{ID: "a.example", Score: 0.91},
		{ID: "b.example", Score: 0.42},
	}}
	recall := &SemanticRecall{Vector: vectors}
	candidates, err := recall.Run(context.Background(), SemanticRecallInput{Text: "оффер", Limit: 10})
	if err != nil {
		t.Fatalf("SemanticRecall: %v", err)
	}
	if len(candidates) != 2 || candidates[0].CompanyID != "a.example" || candidates[1].CompanyID != "b.example" {
		t.Fatalf("кандидаты = %+v", candidates)
	}
	if candidates[0].Score < candidates[1].Score {
		t.Fatalf("список не упорядочен по убыванию: %+v", candidates)
	}
}

// TestSemanticRecallWithoutVector отвергает работу без хранилища.
func TestSemanticRecallWithoutVector(t *testing.T) {
	recall := &SemanticRecall{}
	if _, err := recall.Run(context.Background(), SemanticRecallInput{Text: "оффер"}); !errors.Is(err, ErrVectorUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrVectorUnavailable", err)
	}
}

// TestSemanticRecallRejectsEmptyText отвергает recall по пустому или пробельному
// тексту: кандидаты не выдумываются по несформулированному запросу.
func TestSemanticRecallRejectsEmptyText(t *testing.T) {
	recall := &SemanticRecall{Vector: &fakeVector{matches: []vector.Match{{ID: "a.example", Score: 0.9}}}}
	if _, err := recall.Run(context.Background(), SemanticRecallInput{Text: "   "}); !errors.Is(err, ErrInvalidRecallRequest) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidRecallRequest", err)
	}
}

// fakeSize — источник размера компании.
type fakeSize struct {
	component domain.RichComponent
	ok        bool
}

// SizeComponent реализует SizeSource.
func (f fakeSize) SizeComponent(context.Context, domain.CompanyID) (domain.RichComponent, bool, error) {
	return f.component, f.ok, nil
}

// TestBuildRichDescriptionFromSources — сценарий «Составляющие с источником»:
// описание и соцсети взяты из osint-enrichment, размер — из company-size-assessment.
func TestBuildRichDescriptionFromSources(t *testing.T) {
	ctx := context.Background()
	profiles := newMemProfiles()
	_ = profiles.Save(ctx, companyWith(t, "acme.example", "Acme", "разработка ПО", "linkedin.com/company/acme"))

	size := fakeSize{
		ok: true,
		component: domain.RichComponent{
			Kind: domain.ComponentSize, Value: "250 сотрудников", Source: SourceCompanySize,
		},
	}
	useCase := &BuildRichDescription{Profiles: profiles, Size: size, Clock: fakeClock{testTime}}
	rich, err := useCase.Run(ctx, "acme.example")
	if err != nil {
		t.Fatalf("BuildRichDescription: %v", err)
	}

	sources := make(map[domain.ComponentKind]string)
	for _, component := range rich.Components() {
		if component.Source == "" {
			t.Fatalf("составляющая без источника: %+v", component)
		}
		sources[component.Kind] = component.Source
	}
	if sources[domain.ComponentProducts] != SourceOsint {
		t.Fatalf("источник продуктов = %q", sources[domain.ComponentProducts])
	}
	if sources[domain.ComponentSignals] != SourceOsint {
		t.Fatalf("источник сигналов = %q", sources[domain.ComponentSignals])
	}
	if sources[domain.ComponentSize] != SourceCompanySize {
		t.Fatalf("источник размера = %q", sources[domain.ComponentSize])
	}

	stored, err := profiles.FindByID(ctx, "acme.example")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if stored.RichDescription().IsZero() {
		t.Fatal("rich-описание не сохранено в карточке")
	}
}

// TestBuildRichDescriptionWithoutSizeSource проверяет, что без источника размера
// составляющая размера не выдумывается.
func TestBuildRichDescriptionWithoutSizeSource(t *testing.T) {
	ctx := context.Background()
	profiles := newMemProfiles()
	_ = profiles.Save(ctx, companyWith(t, "acme.example", "Acme", "разработка ПО"))
	useCase := &BuildRichDescription{Profiles: profiles, Clock: fakeClock{testTime}}
	rich, err := useCase.Run(ctx, "acme.example")
	if err != nil {
		t.Fatalf("BuildRichDescription: %v", err)
	}
	for _, component := range rich.Components() {
		if component.Kind == domain.ComponentSize {
			t.Fatal("составляющая размера создана без источника")
		}
	}
}
