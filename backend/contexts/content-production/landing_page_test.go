package contentproduction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/app/command"
	"lead-gen/backend/contexts/content-production/internal/app/query"
	"lead-gen/backend/contexts/content-production/internal/domain"
)

// produceAndRenderLanding создаёт и исполняет задание рендера лендинга, пока
// наполнение не закреплено за пакетом.
func produceAndRenderLanding(t *testing.T, slice *Slice, assetID, requestID string) {
	t.Helper()
	ctx := context.Background()
	produced, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  requestID,
		AssetID:    domain.ContentAssetID(assetID),
		TenantID:   "tenant-1",
		Language:   "ru",
		Format:     domain.RenderFormatLandingPage,
		TemplateID: "tmpl-landing",
		Body:       sampleBody,
		Actor:      "actor-1",
	})
	if err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	if rendered := processRender(t, slice, produced.RenderJobID); rendered.State != domain.RenderStateRendered {
		t.Fatalf("состояние рендера: %s", rendered.State)
	}
}

func sampleLandingQuery(t *testing.T, assetID string) query.GetLandingPage {
	t.Helper()
	form, err := domain.NewLeadForm("https://client.example/leads", []domain.LeadFormField{domain.LeadFormFieldName, domain.LeadFormFieldEmail})
	if err != nil {
		t.Fatalf("NewLeadForm: %v", err)
	}
	general, _ := domain.NewCompanyContact(domain.CompanyContactEmail, "sales@client.example", false)
	personal, _ := domain.NewCompanyContact(domain.CompanyContactPhone, "+7 999 000-11-22", true)
	company, err := domain.NewCompanyProfile("ООО Клиент", "Санкт-Петербург", []domain.CompanyContact{general, personal})
	if err != nil {
		t.Fatalf("NewCompanyProfile: %v", err)
	}
	return query.GetLandingPage{
		AssetID:     domain.ContentAssetID(assetID),
		TenantID:    "tenant-1",
		Description: "Лендинг клиента",
		Company:     company,
		Form:        form,
	}
}

func TestAssembleLandingPage(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)
	produceAndRenderLanding(t, slice, "asset-page", "req-page")

	view, err := slice.GetLandingPage.Handle(ctx, sampleLandingQuery(t, "asset-page"))
	if err != nil {
		t.Fatalf("GetLandingPage: %v", err)
	}
	if view.Title == "" || view.HTML == "" || view.StyleSheet == "" {
		t.Fatalf("страница неполна: %+v", view)
	}
	for _, want := range []string{"ООО Клиент", "sales@client.example", `action="https://client.example/leads"`, `<html lang="ru">`} {
		if !strings.Contains(view.HTML, want) {
			t.Fatalf("HTML не содержит %q", want)
		}
	}
	if strings.Contains(view.HTML, "+7 999 000-11-22") {
		t.Fatal("личный контакт попал в страницу")
	}
	if len(view.ExcludedContacts) != 1 {
		t.Fatalf("факт исключения личного контакта не сообщён: %+v", view.ExcludedContacts)
	}
	if len(view.Archive) == 0 {
		t.Fatal("архив страницы пуст")
	}
}

func TestAssembleLandingPageBeforeRender(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)
	if _, err := slice.ProduceContent.Handle(ctx, command.ProduceContent{
		RequestID:  "req-notready",
		AssetID:    "asset-notready",
		TenantID:   "tenant-1",
		Language:   "ru",
		Format:     domain.RenderFormatLandingPage,
		TemplateID: "tmpl",
		Body:       sampleBody,
		Actor:      "actor-1",
	}); err != nil {
		t.Fatalf("ProduceContent: %v", err)
	}
	_, err := slice.GetLandingPage.Handle(ctx, query.GetLandingPage{AssetID: "asset-notready", TenantID: "tenant-1"})
	if !errors.Is(err, query.ErrArtifactNotReady) {
		t.Fatalf("ожидалась ErrArtifactNotReady, получено %v", err)
	}
}

func TestRestoreContentVersion(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)

	if _, err := slice.CreateContentPackage.Handle(ctx, command.CreateContentPackage{
		AssetID:   "asset-restore",
		TenantID:  "tenant-1",
		VersionID: "ver-1",
		Language:  "ru",
		Body:      "Первая версия.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("CreateContentPackage: %v", err)
	}
	if _, err := slice.PublishContentVersion.Handle(ctx, command.PublishContentVersion{
		AssetID:   "asset-restore",
		TenantID:  "tenant-1",
		VersionID: "ver-2",
		Body:      "Вторая версия.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("PublishContentVersion: %v", err)
	}

	restored, err := slice.RestoreContentVersion.Handle(ctx, command.RestoreContentVersion{
		AssetID:         "asset-restore",
		TenantID:        "tenant-1",
		SourceVersionID: "ver-1",
		NewVersionID:    "ver-3",
		Actor:           "actor-1",
	})
	if err != nil {
		t.Fatalf("RestoreContentVersion: %v", err)
	}
	if restored.VersionID != "ver-3" || restored.RestoredFrom != "ver-1" {
		t.Fatalf("результат возврата: %+v", restored)
	}

	view, err := slice.GetContent.Handle(ctx, query.GetContent{AssetID: "asset-restore", TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("GetContent: %v", err)
	}
	latest := view.Versions[len(view.Versions)-1]
	if latest.VersionID != "ver-3" || latest.Body != "Первая версия." {
		t.Fatalf("восстановленная версия неполна: %+v", latest)
	}
	if len(view.Versions) != 3 {
		t.Fatalf("история версий потеряна: %d", len(view.Versions))
	}

	if _, err := slice.RestoreContentVersion.Handle(ctx, command.RestoreContentVersion{
		AssetID:         "asset-restore",
		TenantID:        "tenant-1",
		SourceVersionID: "ver-3",
		NewVersionID:    "ver-4",
		Actor:           "actor-1",
	}); !errors.Is(err, domain.ErrRestoreSelf) {
		t.Fatalf("ожидалась ErrRestoreSelf, получено %v", err)
	}
}

func TestLandingPageHTTP(t *testing.T) {
	slice := buildSlice(t)
	produceAndRenderLanding(t, slice, "asset-http-landing", "req-http-landing")
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	url := server.URL + "/content/packages/asset-http-landing/landing-page?tenant_id=tenant-1&form_action=https://client.example/leads&company_name=%D0%9E%D0%9E%D0%9E+%D0%9A%D0%BB%D0%B8%D0%B5%D0%BD%D1%82"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET landing-page: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d", resp.StatusCode)
	}
	var payload struct {
		HTML             string   `json:"html"`
		ExcludedContacts []string `json:"excluded_contacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if !strings.Contains(payload.HTML, "ООО Клиент") {
		t.Fatalf("HTML не содержит компанию: %s", payload.HTML)
	}

	archiveResp, err := http.Get(url + "&format=archive")
	if err != nil {
		t.Fatalf("GET archive: %v", err)
	}
	defer archiveResp.Body.Close()
	if archiveResp.StatusCode != http.StatusOK {
		t.Fatalf("статус архива: %d", archiveResp.StatusCode)
	}
	if got := archiveResp.Header.Get("Content-Type"); got != "application/zip" {
		t.Fatalf("тип архива: %q", got)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(archiveResp.Body); err != nil {
		t.Fatalf("чтение архива: %v", err)
	}
	if !bytes.HasPrefix(buf.Bytes(), []byte("PK")) {
		t.Fatalf("ответ не zip: %q", buf.Bytes()[:min(4, buf.Len())])
	}
}

func TestProposeLandingStructureHTTP(t *testing.T) {
	slice := buildSlice(t)
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	body := `{"business_description":"Описываем бизнес","product_info":"Продукт"}`
	resp, err := http.Post(server.URL+"/content/landing-structure", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST landing-structure: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус: %d", resp.StatusCode)
	}
	var payload struct {
		Blocks []struct {
			Role  string `json:"role"`
			Label string `json:"label"`
		} `json:"blocks"`
		Gaps []struct {
			Role string `json:"role"`
		} `json:"gaps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	roles := map[string]bool{}
	for _, block := range payload.Blocks {
		if block.Label == "" {
			t.Fatalf("блок без названия: %+v", block)
		}
		roles[block.Role] = true
	}
	for _, want := range []string{"problem", "solution", "offer", "action"} {
		if !roles[want] {
			t.Fatalf("нет блока %q: %+v", want, roles)
		}
	}
	if roles["proof"] {
		t.Fatal("блок подтверждения создан без документов")
	}
	if len(payload.Gaps) == 0 || payload.Gaps[0].Role != "proof" {
		t.Fatalf("нехватка подтверждения не сообщена: %+v", payload.Gaps)
	}
}

func TestRestoreVersionHTTP(t *testing.T) {
	ctx := context.Background()
	slice := buildSlice(t)
	if _, err := slice.CreateContentPackage.Handle(ctx, command.CreateContentPackage{
		AssetID:   "asset-http-restore",
		TenantID:  "tenant-1",
		VersionID: "ver-1",
		Language:  "ru",
		Body:      "Первая.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("CreateContentPackage: %v", err)
	}
	if _, err := slice.PublishContentVersion.Handle(ctx, command.PublishContentVersion{
		AssetID:   "asset-http-restore",
		TenantID:  "tenant-1",
		VersionID: "ver-2",
		Body:      "Вторая.",
		Actor:     "actor-1",
	}); err != nil {
		t.Fatalf("PublishContentVersion: %v", err)
	}

	server := httptest.NewServer(slice.Server)
	defer server.Close()

	body := `{"tenant_id":"tenant-1","new_version_id":"ver-3","actor":"actor-1"}`
	resp, err := http.Post(server.URL+"/content/packages/asset-http-restore/versions/ver-1/restore", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST restore: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("статус: %d", resp.StatusCode)
	}
	var payload struct {
		VersionID    string `json:"version_id"`
		RestoredFrom string `json:"restored_from"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if payload.VersionID != "ver-3" || payload.RestoredFrom != "ver-1" {
		t.Fatalf("ответ неполон: %+v", payload)
	}
}
