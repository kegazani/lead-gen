package domain

import "testing"

// TestLeadProfileEnrichedEvent проверяет, что событие обогащения объявлено в
// домене, эмитится при создании/обогащении и не публикуется наружу: потребитель
// получает его только через Events() агрегата, а не через внешний канал.
func TestLeadProfileEnrichedEvent(t *testing.T) {
	c, err := CreateCompany(CreateCompanyParams{
		Domain:    "example.com",
		Name:      AttributedValue{Value: "Example", Provenance: provenance("https://example.com")},
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateCompany: %v", err)
	}

	events := c.Events()
	if len(events) != 1 {
		t.Fatalf("событий после создания %d, ожидалось 1", len(events))
	}
	if events[0].EventName() != "lead-profile-enriched" {
		t.Fatalf("имя события %q", events[0].EventName())
	}
	if enriched, ok := events[0].(LeadProfileEnriched); !ok || enriched.CompanyID != "example.com" {
		t.Fatalf("событие не несёт идентификатор компании: %+v", events[0])
	}

	// После вычитки буфер пуст — событие не «залипает» в агрегате.
	if len(c.Events()) != 0 {
		t.Fatal("после вычитки буфер событий должен быть пуст")
	}
}
