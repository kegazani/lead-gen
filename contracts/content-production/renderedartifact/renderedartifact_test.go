package renderedartifact

import (
	"context"
	"errors"
	"testing"
)

// sourceStub — минимальная реализация контрактных интерфейсов для проверки «форм
// контракта»: типы и сигнатуры не меняются от реализации к реализации.
type sourceStub struct{}

func (sourceStub) Landing(_ context.Context, ref ArtifactRef) (LandingPage, error) {
	if ref == "unknown" {
		return LandingPage{}, ErrNotFound
	}
	return LandingPage{Ref: ref, Title: "T", Headline: "H", Sections: []Section{{Heading: "S", Body: "B"}}, FormRef: "form"}, nil
}

func (sourceStub) Presentation(_ context.Context, ref ArtifactRef) (Presentation, error) {
	if ref == "unknown" {
		return Presentation{}, ErrNotFound
	}
	return Presentation{Ref: ref, Title: "T", Slides: []Slide{{Heading: "S", Bullets: []string{"b"}}}}, nil
}

func TestContractIdentity(t *testing.T) {
	if ContractName != "rendered-artifact" {
		t.Errorf("ContractName: получено %q", ContractName)
	}
	if SchemaVersion == "" {
		t.Error("пустая SchemaVersion")
	}
}

func TestLandingSourceContract(t *testing.T) {
	var source LandingSource = sourceStub{}
	page, err := source.Landing(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Landing: неожиданная ошибка: %v", err)
	}
	if page.Ref != "demo" || page.FormRef == "" || len(page.Sections) == 0 {
		t.Fatalf("наполнение неполно: %+v", page)
	}
	if _, err := source.Landing(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}

func TestPresentationSourceContract(t *testing.T) {
	var source PresentationSource = sourceStub{}
	deck, err := source.Presentation(context.Background(), "demo")
	if err != nil {
		t.Fatalf("Presentation: неожиданная ошибка: %v", err)
	}
	if deck.Ref != "demo" || len(deck.Slides) == 0 {
		t.Fatalf("наполнение неполно: %+v", deck)
	}
	if _, err := source.Presentation(context.Background(), "unknown"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ожидалась ErrNotFound, получено %v", err)
	}
}
