package domain

import (
	"errors"
	"testing"
	"time"
)

var baseTime = time.Date(2026, time.January, 2, 10, 0, 0, 0, time.UTC)

func provenance(url string) Provenance {
	return Provenance{SourceURL: url, ExtractedAt: baseTime}
}

func attrProvenance(url string, method ExtractionMethod) AttributeProvenance {
	return AttributeProvenance{SourceURL: url, ExtractedAt: baseTime, Method: method}
}

func TestCreateCompanyRequiresName(t *testing.T) {
	_, err := CreateCompany(CreateCompanyParams{Domain: "example.com"})
	if !errors.Is(err, ErrEmptyCompanyName) {
		t.Fatalf("ожидалась ErrEmptyCompanyName, получено %v", err)
	}
}

func TestCreateCompanyRequiresNameProvenance(t *testing.T) {
	_, err := CreateCompany(CreateCompanyParams{
		Domain: "example.com",
		Name:   AttributedValue{Value: "Example"},
	})
	if !errors.Is(err, ErrMissingProvenance) {
		t.Fatalf("ожидалась ErrMissingProvenance, получено %v", err)
	}
}

func TestCreateCompanyRoundTrip(t *testing.T) {
	c, err := CreateCompany(CreateCompanyParams{
		Domain:      "example.com",
		Name:        AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		Description: AttributedValue{Value: "d", Provenance: provenance("https://example.com/about")},
		Socials:     []AttributedValue{{Value: "https://linkedin.com/example", Provenance: provenance("https://example.com")}},
		CreatedAt:   baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}
	if c.ID() != CompanyID("example.com") {
		t.Fatalf("ID %q, ожидалось example.com", c.ID())
	}
	if c.Name().Value != "Example" {
		t.Fatalf("Name %q", c.Name().Value)
	}
	if len(c.Socials()) != 1 {
		t.Fatalf("соцсетей %d, ожидалось 1", len(c.Socials()))
	}

	rehydrated, err := RehydrateCompany(c.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateCompany: %v", err)
	}
	if rehydrated.ID() != c.ID() || rehydrated.Name().Value != c.Name().Value {
		t.Fatal("восстановленная компания не совпадает с исходной")
	}
}

func TestCompanyEnrichDoesNotOverwrite(t *testing.T) {
	c, err := CreateCompany(CreateCompanyParams{
		Domain:    "example.com",
		Name:      AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	c.Enrich(
		AttributedValue{Value: "New Name", Provenance: provenance("https://other.com")},
		AttributedValue{Value: "desc", Provenance: provenance("https://other.com")},
		[]AttributedValue{{Value: "https://linkedin.com/example", Provenance: provenance("https://other.com")}},
		baseTime.Add(time.Hour),
	)

	if c.Name().Value != "Example" {
		t.Fatalf("Enrich перетёр название: %q", c.Name().Value)
	}
	if c.Description().Value != "desc" {
		t.Fatalf("Enrich не дополнил описание: %q", c.Description().Value)
	}
	if len(c.Socials()) != 1 {
		t.Fatalf("соцсетей %d, ожидалось 1", len(c.Socials()))
	}
}

func TestCompanyMergeSocialsDeduplicates(t *testing.T) {
	c, err := CreateCompany(CreateCompanyParams{
		Domain: "example.com",
		Name:   AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		Socials: []AttributedValue{
			{Value: "https://linkedin.com/example", Provenance: provenance("https://example.com")},
		},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	c.Enrich(
		AttributedValue{},
		AttributedValue{},
		[]AttributedValue{
			{Value: "https://linkedin.com/example", Provenance: provenance("https://example.com")},
			{Value: "https://twitter.com/example", Provenance: provenance("https://example.com")},
		},
		baseTime,
	)

	if len(c.Socials()) != 2 {
		t.Fatalf("соцсетей после слияния %d, ожидалось 2", len(c.Socials()))
	}
}

func TestRehydrateCompanyRejectsBadSnapshot(t *testing.T) {
	if _, err := RehydrateCompany(CompanySnapshot{ID: "example.com"}); !errors.Is(err, ErrEmptyCompanyName) {
		t.Fatalf("ожидалась ErrEmptyCompanyName, получено %v", err)
	}
	if _, err := RehydrateCompany(CompanySnapshot{
		ID:   "example.com",
		Name: AttributedValue{Value: "Example"}, // без происхождения
	}); !errors.Is(err, ErrMissingProvenance) {
		t.Fatalf("ожидалась ErrMissingProvenance, получено %v", err)
	}
}
