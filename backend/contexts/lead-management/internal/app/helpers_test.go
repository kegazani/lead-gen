package app

import (
	"reflect"
	"testing"

	"lead-gen/contracts/integration-platform/crawl"
)

func TestNormalizeDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{in: "https://www.Example.com/path?q=1", want: "example.com"},
		{in: "http://example.com", want: "example.com"},
		{in: "www.example.com", want: "example.com"},
		{in: "EXAMPLE.COM.", want: "example.com"},
		{in: "example.com", want: "example.com"},
		{in: "https://example.com:8080/page", want: "example.com"},
		{in: "  example.com  ", want: "example.com"},
	}
	for _, tc := range cases {
		if got := NormalizeDomain(tc.in); got != tc.want {
			t.Fatalf("NormalizeDomain(%q) = %q, ожидалось %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeEmail(t *testing.T) {
	if got := NormalizeEmail("  Foo.Bar@Example.COM "); got != "foo.bar@example.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
}

func TestNormalizePhone(t *testing.T) {
	if got := NormalizePhone("+1 (234) 567-89-01"); got != "12345678901" {
		t.Fatalf("NormalizePhone = %q", got)
	}
}

func TestDecodeCloudflareEmail(t *testing.T) {
	// Вектор: ключ 0x5a, строка "hello@example.com".
	encoded := "5a323f3636351a3f223b372a363f74393537"
	got, ok := DecodeCloudflareEmail(encoded)
	if !ok {
		t.Fatalf("декодирование %q не удалось", encoded)
	}
	if got != "hello@example.com" {
		t.Fatalf("декодировано %q, ожидалось hello@example.com", got)
	}

	if _, ok := DecodeCloudflareEmail("xyz"); ok {
		t.Fatal("не-hex строка должна вернуть false")
	}
	if _, ok := DecodeCloudflareEmail(""); ok {
		t.Fatal("пустая строка должна вернуть false")
	}
	if _, ok := DecodeCloudflareEmail("abc"); ok {
		t.Fatal("нечётная длина должна вернуть false")
	}
}

func TestExtractEmails(t *testing.T) {
	got := ExtractEmails("Напишите Foo@Bar.com или support@Example.org. Foo@Bar.com снова.")
	want := []string{"foo@bar.com", "support@example.org"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractEmails = %v, ожидалось %v", got, want)
	}
}

func TestExtractPhones(t *testing.T) {
	got := ExtractPhones("Звоните +1 (234) 567-8901 или 555-1234 или (415) 555-2671.")
	want := []string{"12345678901", "4155552671"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractPhones = %v, ожидалось %v", got, want)
	}
}

func TestIsPhoneCandidate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Телефоны: ведущий «+», скобки, дефисы или пробелы.
		{name: "номер с ведущим плюсом", in: "+1 (234) 567-8901", want: true},
		{name: "номер с дефисом", in: "555-1234", want: false},
		{name: "номер со скобками", in: "(415) 555-2671", want: true},
		{name: "номер с пробелами", in: "+7 495 123-45-67", want: true},
		{name: "плюс и слитная запись", in: "+74951234567", want: true},

		// Мусор: даты, цены и числовые последовательности.
		{name: "дата через точку", in: "2024.01.01", want: false},
		{name: "дата через дефис", in: "2024-01-01", want: false},
		{name: "дата день-месяц-год", in: "01.02.2023", want: false},
		{name: "цена с запятой", in: "12,5", want: false},
		{name: "год", in: "2024", want: false},
		{name: "слитная длинная последовательность", in: "12345678901234567890", want: false},
		{name: "слитно без плюса и разделителей", in: "88005553535", want: false},
		{name: "слишком мало цифр", in: "555-12", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPhoneCandidate(tc.in); got != tc.want {
				t.Fatalf("isPhoneCandidate(%q) = %v, ожидалось %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractPhonesSkipsDatesAndLongRuns(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{name: "дата через точку", in: "Опубликовано 2024.01.01", want: nil},
		{name: "дата через дефис", in: "Опубликовано 2024-01-01", want: nil},
		{name: "дата день-месяц-год", in: "Опубликовано 01.02.2023", want: nil},
		{name: "короткое число", in: "Код 2024", want: nil},
		{name: "цена с запятой", in: "Цена 12,5 рубля", want: nil},
		{name: "слитная длинная последовательность", in: "Трек 12345678901234567890", want: nil},
		{name: "дата и телефон", in: "Дата 2024.01.01, звоните +1 (234) 567-8901", want: []string{"12345678901"}},
		{name: "форма с плюсом и пробелами", in: "Тел: +7 495 123-45-67", want: []string{"74951234567"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractPhones(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ExtractPhones(%q) = %v, ожидалось %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestContactCandidatesFromExtractFiltersPhones(t *testing.T) {
	candidates := []crawl.ContactCandidate{
		{Email: "Sales@Example.com", Phone: "+1 (234) 567-8901", Role: "Sales"},
		{Phone: "2024.01.01"},                      // дата — не телефон, кандидат без идентификатора
		{Email: "Only@Example.com", Phone: "2024"}, // мусорный телефон отброшен, email остаётся
	}
	got := contactCandidatesFromExtract(candidates, crawl.Page{URL: "https://example.com", Markdown: "Sales@example.com Only@example.com Телефон: +1 (234) 567-8901"}, testTime)
	if len(got) != 2 {
		t.Fatalf("кандидатов %d, ожидалось 2: %+v", len(got), got)
	}
	if got[0].email.value != "sales@example.com" || got[0].phone.value != "12345678901" {
		t.Fatalf("первый кандидат %+v, ожидались email sales@example.com и телефон 12345678901", got[0])
	}
	if got[1].email.value != "only@example.com" || got[1].phone.value != "" {
		t.Fatalf("второй кандидат %+v, ожидался только email only@example.com", got[1])
	}
}

func TestExtractLayer1PrefersMetadataTitle(t *testing.T) {
	r := &RunSearch{}

	// title есть: именно он даёт название компании, H1 может быть заголовком секции.
	name, desc, _ := r.extractLayer1(crawl.Page{
		URL:      "https://example.com/",
		Markdown: "# Расчёт стоимости\nтекст",
		Metadata: map[string]string{"title": "Компания Термопроект", "description": "d"},
	})
	if name.Value != "Компания Термопроект" {
		t.Fatalf("name = %q, ожидалось название из metadata.title", name.Value)
	}
	if desc.Value != "d" {
		t.Fatalf("description = %q, ожидалось из metadata.description", desc.Value)
	}

	// title пуст: фолбэк на первый H1.
	name, _, _ = r.extractLayer1(crawl.Page{
		URL:      "https://example.com/",
		Markdown: "# Компания Термопроект\nтекст",
		Metadata: map[string]string{},
	})
	if name.Value != "Компания Термопроект" {
		t.Fatalf("name = %q, ожидался фолбэк на H1", name.Value)
	}
}

func TestExtractSocials(t *testing.T) {
	text := `
	LinkedIn: https://www.linkedin.com/company/example
	Twitter: https://twitter.com/example
	Facebook: https://facebook.com/example
	Instagram: https://instagram.com/example
	TG: https://t.me/example
	`
	got := ExtractSocials(text)
	want := []string{
		"https://www.linkedin.com/company/example",
		"https://twitter.com/example",
		"https://facebook.com/example",
		"https://instagram.com/example",
		"https://t.me/example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractSocials = %v, ожидалось %v", got, want)
	}
}
