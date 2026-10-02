package domain

import (
	"errors"
	"testing"
	"time"
)

// TestNewEmbeddingValidation проверяет инварианты эмбеддинга: вектор, watermark и
// время расчёта обязательны.
func TestNewEmbeddingValidation(t *testing.T) {
	if _, err := NewEmbedding(nil, "v1", baseTime); !errors.Is(err, ErrInvalidEmbedding) {
		t.Fatalf("пустой вектор: %v", err)
	}
	if _, err := NewEmbedding([]float32{1}, "", baseTime); !errors.Is(err, ErrInvalidEmbedding) {
		t.Fatalf("пустой watermark: %v", err)
	}
	if _, err := NewEmbedding([]float32{1}, "v1", time.Time{}); !errors.Is(err, ErrInvalidEmbedding) {
		t.Fatalf("нулевое время: %v", err)
	}
	embedding, err := NewEmbedding([]float32{1, 2}, "v1", baseTime)
	if err != nil {
		t.Fatalf("NewEmbedding: %v", err)
	}
	if !embedding.MatchesSource("v1") || embedding.MatchesSource("v2") {
		t.Fatal("watermark сопоставляется неверно")
	}
}

// TestCompanyEmbeddingRoundTrip проверяет, что эмбеддинг и rich-описание переживают
// сохранение и восстановление карточки.
func TestCompanyEmbeddingRoundTrip(t *testing.T) {
	company, err := CreateCompany(CreateCompanyParams{
		Domain:    "example.com",
		Name:      AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if err := company.SetEmbedding([]float32{0.5, 0.25}, "v1", baseTime); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	rich, err := NewRichDescription([]RichComponent{
		{Kind: ComponentProducts, Value: "разработка ПО", Source: "osint-enrichment"},
	}, baseTime)
	if err != nil {
		t.Fatalf("NewRichDescription: %v", err)
	}
	company.SetRichDescription(rich, baseTime)

	restored, err := RehydrateCompany(company.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateCompany: %v", err)
	}
	if restored.Embedding().IsZero() || !restored.Embedding().MatchesSource("v1") {
		t.Fatalf("эмбеддинг потерян: %+v", restored.Embedding())
	}
	if restored.RichDescription().IsZero() {
		t.Fatal("rich-описание потеряно")
	}
}

// TestCompanyEmbeddingOutdated проверяет, что смена watermark помечает вектор
// устаревшим.
func TestCompanyEmbeddingOutdated(t *testing.T) {
	company, err := CreateCompany(CreateCompanyParams{
		Domain:    "example.com",
		Name:      AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if !company.EmbeddingOutdated("v1") {
		t.Fatal("отсутствующий эмбеддинг должен считаться устаревшим")
	}
	if err := company.SetEmbedding([]float32{1}, "v1", baseTime); err != nil {
		t.Fatalf("SetEmbedding: %v", err)
	}
	if company.EmbeddingOutdated("v1") {
		t.Fatal("актуальный эмбеддинг помечен устаревшим")
	}
	if !company.EmbeddingOutdated("v2") {
		t.Fatal("эмбеддинг другой версии должен считаться устаревшим")
	}
}

// TestRichDescriptionRequiresSource проверяет инвариант: составляющая без
// источника не принимается.
func TestRichDescriptionRequiresSource(t *testing.T) {
	_, err := NewRichDescription([]RichComponent{
		{Kind: ComponentProducts, Value: "разработка ПО"},
	}, baseTime)
	if !errors.Is(err, ErrInvalidRichDescription) {
		t.Fatalf("ошибка = %v, ожидалась ErrInvalidRichDescription", err)
	}
	if _, err := NewRichDescription([]RichComponent{
		{Kind: ComponentKind("bogus"), Value: "x", Source: "osint-enrichment"},
	}, baseTime); !errors.Is(err, ErrInvalidRichDescription) {
		t.Fatalf("неизвестный вид: %v", err)
	}
}

// TestRichDescriptionText проверяет текстовую сводку: только значения, без
// источников.
func TestRichDescriptionText(t *testing.T) {
	rich, err := NewRichDescription([]RichComponent{
		{Kind: ComponentProducts, Value: "разработка ПО", Source: "osint-enrichment"},
		{Kind: ComponentSize, Value: "250 сотрудников", Source: "company-size-assessment"},
	}, baseTime)
	if err != nil {
		t.Fatalf("NewRichDescription: %v", err)
	}
	text := rich.Text()
	if text != "разработка ПО 250 сотрудников" {
		t.Fatalf("text = %q", text)
	}
}
