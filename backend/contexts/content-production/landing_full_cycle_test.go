package contentproduction

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/content-production/internal/adapters/renderer/deterministic"
	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/llm"
)

// Сквозной сценарий capability landing-page-generator (задача 10.1): материалы
// клиента → сбор с сайта → структура → утверждение → тексты → сборка → выдача
// архива. Проверяется на реальной сборке среза (Build), реальном наполнении и
// реальном модуле сборки страницы internal/landing.
//
// Тестовые двойники стоят только там, где внешней системы нет в тестовой среде, и
// это видно в коде: движок сбора (Firecrawl, движок 3.1) за портом crawl.Port,
// LLM за портом llm.Port (генерация текста — capability linguistics), движок
// рендера (Chromium) заменён детерминированным dev-двойником. Реальный продуктовый
// результат — HTML/CSS и архив — производит модуль internal/landing, а не двойник.

const (
	cycleAssetID   = domain.ContentAssetID("asset-landing-cycle")
	cycleTenantID  = domain.TenantID("tenant-landing-cycle")
	cycleRequestID = domain.RenderJobID("req-landing-cycle")
	cycleSiteURL   = "https://client.example"
	cycleOperator  = domain.ActorID("operator-cycle")

	cycleBusiness = "Разрабатываем сайты и интернет-магазины для малого бизнеса"
	cycleProduct  = "Аналитика продаж и техническая поддержка"
	cycleDocument = "Коммерческое предложение: аудит сайта и запуск рекламы"

	cycleCompanyName = "ООО «Клиент-Сервис»"
	cycleAddress     = "Санкт-Петербург"

	// Персональные данные, найденные на сайте клиента: в наполнение и хранилище
	// попадать не должны.
	siteDirectorName  = "Иван Петров"
	sitePersonalMail  = "ivan.petrov@client.example"
	sitePersonalPhone = "+7 999 123-45-67"
	// Общий контакт компании: переносится в наполнение.
	siteGeneralMail = "sales@client.example"
	// Личный контакт компании из материалов: исключается с сообщением.
	companyPersonalPhone = "+7 999 000-11-22"
)

// landingCycleFixture — результат сквозного прогона среза.
type landingCycleFixture struct {
	slice     *Slice
	collected command.CollectSiteResult
	structure query.LandingStructureView
	texts     command.GenerateLandingTextsResult
	page      query.LandingPageAssembly
}

// runLandingCycle прогоняет сценарий целиком на реальной сборке среза и возвращает
// его результат. formAction — адрес приёмника заявок в контуре клиента.
func runLandingCycle(t *testing.T, formAction string) landingCycleFixture {
	t.Helper()
	ctx := context.Background()

	// Тестовый двойник порта LLM: внешний движок linguistics недоступен в тестовой
	// среде. Ответ правдоподобен и не содержит сведений вне материалов клиента.
	const llmReply = `{"heading":"Сайты и интернет-магазины","body":"Разрабатываем сайты и интернет-магазины для малого бизнеса, помогаем видеть продажи."}`

	slice, err := Build(Config{
		LLM:           stubLLM{reply: llm.ConverseReply{Text: llmReply}},
		SiteCollector: stubCrawl{outcome: crawl.CrawlOutcome{Pages: []crawl.Page{{URL: cycleSiteURL, Markdown: cycleSiteMarkdown()}}}},
		Renderer:      deterministic.New(),
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	// 1. Сбор сведений с сайта клиента средствами движка 3.1 за портом crawl.Port.
	collected, err := slice.CollectSite.Handle(ctx, command.CollectSite{StartURL: cycleSiteURL, MaxPages: 5, MaxDepth: 2})
	if err != nil {
		t.Fatalf("CollectSite: %v", err)
	}
	if !collected.Available || len(collected.Pages) == 0 {
		t.Fatalf("сбор не выполнен: %+v", collected)
	}

	// 2. Структура показывается оператору до генерации текстов.
	structure, err := slice.ProposeLandingStructure.Handle(ctx, query.ProposeLandingStructure{
		SiteURL:             cycleSiteURL,
		BusinessDescription: cycleBusiness,
		ProductInfo:         cycleProduct,
		Documents:           []string{cycleDocument},
	})
	if err != nil {
		t.Fatalf("ProposeLandingStructure: %v", err)
	}
	if len(structure.Blocks) == 0 {
		t.Fatalf("структура пуста: %+v", structure)
	}

	// 3. Тексты: только после утверждения структуры, через capability linguistics.
	texts, err := slice.GenerateLandingTexts.Handle(ctx, command.GenerateLandingTexts{
		SiteURL:             cycleSiteURL,
		BusinessDescription: cycleBusiness,
		ProductInfo:         cycleProduct,
		Documents:           []string{cycleDocument},
		SiteText:            collected.Pages[0].Text,
		Tone:                "деловой",
		Language:            "русский",
		Approved:            true,
		ApprovedBy:          cycleOperator,
		ApprovedAt:          time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("GenerateLandingTexts: %v", err)
	}
	if texts.Title == "" || len(texts.Sections) == 0 {
		t.Fatalf("наполнение неполно: %+v", texts)
	}

	// 4. Сборка: версия контента, рендер и публикуемая страница с архивом.
	produced, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  string(cycleRequestID),
		AssetID:    cycleAssetID,
		TenantID:   cycleTenantID,
		Language:   "ru",
		Region:     "RU",
		Format:     domain.RenderFormatLandingPage,
		TemplateID: "tmpl-landing",
		Body:       composeLandingBody(texts),
		Actor:      cycleOperator,
	})
	if err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	if rendered := processRender(t, slice, produced.RenderJobID); rendered.State != domain.RenderStateRendered {
		t.Fatalf("состояние рендера: %s (%s)", rendered.State, rendered.Reason)
	}

	page, err := slice.GetLandingPage.Handle(ctx, query.GetLandingPage{
		AssetID:     cycleAssetID,
		TenantID:    cycleTenantID,
		Description: "Лендинг клиента",
		Company:     cycleCompany(t),
		Form:        cycleForm(t, formAction),
	})
	if err != nil {
		t.Fatalf("GetLandingPage: %v", err)
	}

	return landingCycleFixture{slice: slice, collected: collected, structure: structure, texts: texts, page: page}
}

// TestLandingPageGeneratorFullCycle доказывает критерий задачи 10.1: сценарий
// проходит целиком, а выданный архив открывается локально без обращений к сети.
func TestLandingPageGeneratorFullCycle(t *testing.T) {
	fixture := runLandingCycle(t, "https://receiver.client.example/leads")

	// Структура построена по материалам и включает роли, подтверждённые ими.
	roles := map[string]bool{}
	for _, block := range fixture.structure.Blocks {
		roles[block.Role] = true
	}
	for _, want := range []string{"problem", "solution", "offer", "action", "proof"} {
		if !roles[want] {
			t.Fatalf("нет блока %q: %+v", want, roles)
		}
	}
	if len(fixture.structure.Gaps) != 0 {
		t.Fatalf("материалов достаточно, но нехватка сообщена: %+v", fixture.structure.Gaps)
	}

	// Архив размещения: распакованный, открывается локально (index.html + styles.css).
	files := readArchive(t, fixture.page.Archive)
	index, ok := files["index.html"]
	if !ok {
		t.Fatalf("в архиве нет index.html: %v", archiveNames(files))
	}
	style, ok := files["styles.css"]
	if !ok {
		t.Fatalf("в архиве нет styles.css: %v", archiveNames(files))
	}
	if strings.TrimSpace(style) == "" {
		t.Fatal("styles.css пуст")
	}

	for _, want := range []string{
		"<!doctype html>",
		`<html lang="ru">`,
		`<meta name="viewport"`,
		`<title>`,
		`href="styles.css"`,
		"интернет-магазины",
		cycleCompanyName,
		siteGeneralMail,
		`action="https://receiver.client.example/leads"`,
	} {
		if !strings.Contains(index, want) {
			t.Fatalf("index.html не содержит %q", want)
		}
	}
	assertSelfContainedPage(t, index)

	// Личный контакт компании исключён с сообщением.
	if strings.Contains(index, companyPersonalPhone) {
		t.Fatal("личный контакт попал в страницу")
	}
	if len(fixture.page.ExcludedContacts) != 1 {
		t.Fatalf("факт исключения личного контакта не сообщён: %+v", fixture.page.ExcludedContacts)
	}
}

// composeLandingBody собирает текст версии для рендера из порождённого наполнения:
// заголовок и тексты блоков.
func composeLandingBody(result command.GenerateLandingTextsResult) string {
	var b strings.Builder
	b.WriteString(strings.TrimSpace(result.Title))
	b.WriteString("\n\n")
	for _, section := range result.Sections {
		if body := strings.TrimSpace(section.Body); body != "" {
			b.WriteString(body)
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// cycleSiteMarkdown — содержимое страницы сайта клиента: сведения о компании,
// общий контакт и персональные данные, которые не должны попасть в наполнение.
func cycleSiteMarkdown() string {
	return strings.Join([]string{
		"Компания «Клиент-Сервис».",
		"Разрабатываем сайты и интернет-магазины.",
		"Генеральный директор " + siteDirectorName,
		sitePersonalMail,
		sitePersonalPhone,
		siteGeneralMail,
	}, "\n")
}

// cycleCompany собирает профиль компании: общий контакт переносится, личный
// помечается персональным и исключается.
func cycleCompany(t *testing.T) domain.CompanyProfile {
	t.Helper()
	general, err := domain.NewCompanyContact(domain.CompanyContactEmail, siteGeneralMail, false)
	if err != nil {
		t.Fatalf("NewCompanyContact(общий): %v", err)
	}
	personal, err := domain.NewCompanyContact(domain.CompanyContactPhone, companyPersonalPhone, true)
	if err != nil {
		t.Fatalf("NewCompanyContact(личный): %v", err)
	}
	company, err := domain.NewCompanyProfile(cycleCompanyName, cycleAddress, []domain.CompanyContact{general, personal})
	if err != nil {
		t.Fatalf("NewCompanyProfile: %v", err)
	}
	return company
}

// cycleForm собирает форму заявки минимального состава с приёмником клиента.
func cycleForm(t *testing.T, action string) domain.LeadForm {
	t.Helper()
	form, err := domain.NewLeadForm(action, []domain.LeadFormField{domain.LeadFormFieldName, domain.LeadFormFieldEmail})
	if err != nil {
		t.Fatalf("NewLeadForm: %v", err)
	}
	return form
}

// readArchive распаковывает архив размещения в карту «имя файла → содержимое».
func readArchive(t *testing.T, data []byte) map[string]string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("архив не читается: %v", err)
	}
	files := make(map[string]string, len(reader.File))
	for _, entry := range reader.File {
		file, err := entry.Open()
		if err != nil {
			t.Fatalf("открытие %q в архиве: %v", entry.Name, err)
		}
		content, err := io.ReadAll(file)
		file.Close()
		if err != nil {
			t.Fatalf("чтение %q из архива: %v", entry.Name, err)
		}
		files[entry.Name] = string(content)
	}
	return files
}

// archiveNames возвращает имена файлов архива для сообщения об ошибке.
func archiveNames(files map[string]string) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	return names
}

// assertSelfContainedPage проверяет, что страница не требует сети: нет внешних
// стилей, скриптов, изображений и импортов. Тогда архив открывается локально.
func assertSelfContainedPage(t *testing.T, html string) {
	t.Helper()
	for _, forbidden := range []string{`src="http`, `href="http`, "<script", "@import", "url(http"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("страница ссылается на внешний ресурс (%q): открытие локально не гарантировано", forbidden)
		}
	}
}
