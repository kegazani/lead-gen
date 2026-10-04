package landing

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/domain"
)

// sampleContent собирает наполнение лендинга для тестов сборки.
func sampleContent(t *testing.T) domain.LandingContent {
	t.Helper()
	section, err := domain.NewLandingSection("Наше предложение", "Мы делаем сложное простым.")
	if err != nil {
		t.Fatalf("NewLandingSection: %v", err)
	}
	content, err := domain.NewLandingContent(
		"lc-1", "ver-1",
		"Ромашка — сервис",
		"Ромашка: доставка вовремя",
		"Коротко о компании.",
		[]domain.LandingSection{section},
	)
	if err != nil {
		t.Fatalf("NewLandingContent: %v", err)
	}
	return content
}

func sampleOptions(t *testing.T) Options {
	t.Helper()
	brand, err := domain.NewBrandKit("#2563eb", "#1e40af", "Segoe UI, Arial", "Ромашка")
	if err != nil {
		t.Fatalf("NewBrandKit: %v", err)
	}
	general, _ := domain.NewCompanyContact(domain.CompanyContactEmail, "sales@romashka.example", false)
	personal, _ := domain.NewCompanyContact(domain.CompanyContactPhone, "+7 999 123-45-67", true)
	company, err := domain.NewCompanyProfile("ООО Ромашка", "Москва", []domain.CompanyContact{general, personal})
	if err != nil {
		t.Fatalf("NewCompanyProfile: %v", err)
	}
	form, err := domain.NewLeadForm("https://client.example/leads", []domain.LeadFormField{domain.LeadFormFieldName, domain.LeadFormFieldEmail})
	if err != nil {
		t.Fatalf("NewLeadForm: %v", err)
	}
	return Options{Description: "Лендинг Ромашки", Brand: brand, Company: company, Form: form}
}

func TestDocumentCarriesContentAndMetadata(t *testing.T) {
	page := New(sampleContent(t), "ru", sampleOptions(t))
	doc := page.Document()

	for _, want := range []string{
		`<html lang="ru">`,
		`<meta name="viewport"`,
		"<title>Ромашка — сервис</title>",
		`<meta name="description" content="Лендинг Ромашки">`,
		"Ромашка: доставка вовремя",
		"Мы делаем сложное простым.",
		"ООО Ромашка",
		"sales@romashka.example",
		`action="https://client.example/leads"`,
		`name="name"`,
		`name="email"`,
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("документ не содержит %q\n%s", want, doc)
		}
	}
}

func TestDocumentExcludesPersonalContacts(t *testing.T) {
	doc := New(sampleContent(t), "ru", sampleOptions(t)).Document()
	if strings.Contains(doc, "+7 999 123-45-67") || strings.Contains(doc, "ivan") {
		t.Fatalf("личный контакт попал в наполнение:\n%s", doc)
	}
}

func TestDocumentHasNoExternalResources(t *testing.T) {
	page := New(sampleContent(t), "ru", sampleOptions(t))
	if strings.Contains(page.Document(), "http://") || strings.Contains(page.StyleSheet(), "http://") {
		t.Fatal("страница обращается к внешнему ресурсу по http")
	}
	// Приёмник заявки — единственный http-адрес, и он в атрибуте action, а не в
	// стилях или подключаемых ресурсах.
	if strings.Contains(page.StyleSheet(), "@import") || strings.Contains(page.StyleSheet(), "url(") {
		t.Fatal("стили подключают внешний ресурс")
	}
}

func TestDocumentEscapesInjectedMarkup(t *testing.T) {
	section, err := domain.NewLandingSection("", "<script>alert(1)</script>")
	if err != nil {
		t.Fatalf("NewLandingSection: %v", err)
	}
	content, err := domain.NewLandingContent("lc-x", "ver-1", "T", "H", "", []domain.LandingSection{section})
	if err != nil {
		t.Fatalf("NewLandingContent: %v", err)
	}
	doc := New(content, "ru", Options{}).Document()
	if strings.Contains(doc, "<script>alert") {
		t.Fatalf("данные не экранированы:\n%s", doc)
	}
	if !strings.Contains(doc, "&lt;script&gt;") {
		t.Fatalf("ожидалось экранирование разметки:\n%s", doc)
	}
}

func TestStyleSheetUsesBrandColors(t *testing.T) {
	page := New(sampleContent(t), "ru", sampleOptions(t))
	css := page.StyleSheet()
	if !strings.Contains(css, "--primary:#2563eb") || !strings.Contains(css, "--accent:#1e40af") {
		t.Fatalf("бренд-цвета не применены:\n%s", css)
	}
	if !strings.Contains(css, "@media (max-width:600px)") {
		t.Fatalf("нет адаптивных правил:\n%s", css)
	}
}

func TestNeutralStyleSheetWithoutBrand(t *testing.T) {
	page := New(sampleContent(t), "ru", Options{})
	css := page.StyleSheet()
	if !strings.Contains(css, "--primary:"+neutralPrimary) {
		t.Fatalf("нейтральное оформление не применено:\n%s", css)
	}
}

func TestArchiveContainsPlaceableFiles(t *testing.T) {
	page := New(sampleContent(t), "ru", sampleOptions(t))
	data, err := page.Archive()
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("архив не читается: %v", err)
	}
	files := map[string]string{}
	for _, file := range reader.File {
		handle, err := file.Open()
		if err != nil {
			t.Fatalf("открытие %s: %v", file.Name, err)
		}
		body, err := io.ReadAll(handle)
		handle.Close()
		if err != nil {
			t.Fatalf("чтение %s: %v", file.Name, err)
		}
		files[file.Name] = string(body)
	}
	index, ok := files["index.html"]
	if !ok {
		t.Fatalf("в архиве нет index.html: %v", files)
	}
	if _, ok := files["styles.css"]; !ok {
		t.Fatalf("в архиве нет styles.css: %v", files)
	}
	if !strings.Contains(index, `href="styles.css"`) {
		t.Fatalf("index.html не ссылается на styles.css:\n%s", index)
	}
	if strings.Contains(index, "<style>") {
		t.Fatal("index.html архива должен ссылаться на внешние стили, а не встраивать их")
	}
}
