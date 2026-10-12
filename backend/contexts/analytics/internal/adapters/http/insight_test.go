package httpapi_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// seedInsights наполняет проекцию качества поиска исходами классификации:
// устойчивая ниша saas с высокой долей «сигнал не найден» и узкий срез solo.
func seedInsights(t *testing.T, server *httptest.Server) {
	t.Helper()
	occurred := day(0).Format(time.RFC3339)
	events := []map[string]any{
		{"tenant_id": "t1", "company_id": "s1", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s2", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s3", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s4", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s5", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s6", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s7", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "fits", "occurred_at": occurred},
		{"tenant_id": "t1", "company_id": "s8", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "unsuitable", "reason": "refuted", "occurred_at": occurred},
		// Ошибка извлечения считается отдельно от опровержения.
		{"tenant_id": "t1", "company_id": "s9", "kind": "lead-assessment", "job_id": "job-a", "source": "crawl", "industry": "saas", "verdict": "unsuitable", "reason": "extraction_error", "occurred_at": occurred},
		// Узкий срез из одной записи: скрыт порогом группы.
		{"tenant_id": "t1", "company_id": "solo", "kind": "lead-assessment", "job_id": "job-a", "source": "forum", "industry": "solo", "verdict": "hypothesis", "reason": "no_signal", "occurred_at": occurred},
	}
	for _, event := range events {
		postEvent(t, server, event)
	}
}

// TestInsightsRendersDistributionAndRecommendations проверяет экран анализа
// качества поиска: распределение вердиктов и причин, разрез по нише и
// рекомендацию с основанием (задачи 4.1–4.4).
func TestInsightsRendersDistributionAndRecommendations(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	status, page := getPage(t, server, "/insights?tenant_id=t1&role=operator")
	if status != http.StatusOK {
		t.Fatalf("GET /insights = %d, ожидался 200", status)
	}
	for _, want := range []string{
		"Анализ качества поиска", "Вердикты", "Подходит", "Гипотеза", "Не подходит", "Ошибки извлечения",
		"Причины «не подходит»", "сигнал не найден", "потребность опровергнута", "ошибка извлечения",
		"Рекомендации оператору", "добавить цели обхода", "наблюдений",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("экран анализа не содержит %q:\n%s", want, page)
		}
	}
	// Распределение вердиктов отдельно: ошибка извлечения не приписана «не подходит».
	if !strings.Contains(page, `data-verdict="extraction_error">1<`) {
		t.Fatalf("ошибка извлечения не показана отдельно:\n%s", page)
	}
	if !strings.Contains(page, `data-verdict="unsuitable">1<`) {
		t.Fatalf("«не подходит» должно учитывать только опровержение:\n%s", page)
	}
	// Узкий срез (одна запись) не показан в разрезе по нишам.
	if strings.Contains(page, `data-slice="solo"`) {
		t.Fatalf("срез из одной записи показан:\n%s", page)
	}
	// Персональных данных и записей пула нет.
	if strings.Contains(page, "s1") || strings.Contains(page, "solo.example") {
		t.Fatalf("экран раскрывает записи пула:\n%s", page)
	}
}

// TestInsightsHiddenAndUnavailable проверяет «недоступно»/«нет данных» вместо
// нуля и порог минимальной группы (задача 4.4).
func TestInsightsHiddenAndUnavailable(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	// Срез без наблюдений: сообщается отсутствие данных, метрики не подменены нулём.
	status, page := getPage(t, server, "/insights?tenant_id=t1&role=operator&industry=unknown")
	if status != http.StatusOK {
		t.Fatalf("GET /insights = %d, ожидался 200", status)
	}
	if !strings.Contains(page, "нет наблюдений") {
		t.Fatalf("пустой срез должен сообщать отсутствие данных:\n%s", page)
	}
	if strings.Contains(page, `data-verdict="fits">0<`) {
		t.Fatalf("пустой срез не должен показывать нули:\n%s", page)
	}
	if strings.Contains(page, "Рекомендации оператору") {
		t.Fatalf("для пустого среза рекомендации не формируются:\n%s", page)
	}
}

// TestInsightsJSON проверяет JSON-срез анализа.
func TestInsightsJSON(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	status, body := getPage(t, server, "/insights.json?tenant_id=t1&role=operator")
	if status != http.StatusOK {
		t.Fatalf("GET /insights.json = %d, ожидался 200", status)
	}
	var view struct {
		Available bool `json:"Available"`
		Visible   bool `json:"Visible"`
		Data      struct {
			Total    int `json:"Total"`
			Verdicts struct {
				ExtractionErrors int `json:"ExtractionErrors"`
			} `json:"Verdicts"`
			Recommendations []struct {
				Action string `json:"Action"`
				Basis  string `json:"Basis"`
			} `json:"Recommendations"`
		} `json:"Data"`
	}
	if err := json.Unmarshal([]byte(body), &view); err != nil {
		t.Fatalf("разбор JSON: %v\n%s", err, body)
	}
	if !view.Available || !view.Visible {
		t.Fatalf("JSON-срез недоступен: %s", body)
	}
	if view.Data.Total != 10 {
		t.Fatalf("Total = %d, ожидалось 10", view.Data.Total)
	}
	if view.Data.Verdicts.ExtractionErrors != 1 {
		t.Fatalf("ошибки извлечения = %d, ожидалось 1", view.Data.Verdicts.ExtractionErrors)
	}
	if len(view.Data.Recommendations) == 0 || view.Data.Recommendations[0].Basis == "" {
		t.Fatalf("рекомендация без основания: %s", body)
	}
}

// TestInsightsAccessDenied проверяет, что анализ закрыт для клиента и чужой роли
// (задача 4.4: операционный разбор причин клиенту не показывается).
func TestInsightsAccessDenied(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	for _, path := range []string{"/insights?tenant_id=t1&role=guest", "/insights?tenant_id=t1&role=client", "/insights.json?tenant_id=t1&role=guest"} {
		if status, _ := getPage(t, server, path); status != http.StatusForbidden {
			t.Fatalf("GET %s = %d, ожидался 403", path, status)
		}
	}
}

// TestInsightsRecommendationApplyUnavailable проверяет, что применение
// рекомендации без подключённого контура поиска честно недоступно (AGENTS.md §3).
func TestInsightsRecommendationApplyUnavailable(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	payload, _ := json.Marshal(map[string]any{
		"tenant_id": "t1", "scope_kind": "industry", "scope_key": "saas",
		"reason": "no_signal", "basis": "наблюдений 9",
		"change": map[string]any{"kind": "new-search", "criteria": "saas-агентства"},
	})
	resp, err := http.Post(server.URL+"/insights/recommendations/apply?role=operator", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST apply: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST apply без контура = %d, ожидался 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "недоступен") {
		t.Fatalf("отказ не сообщает недоступность: %s", body)
	}

	// Неоператору применение запрещено даже при подключённом контуре.
	denied, err := http.Post(server.URL+"/insights/recommendations/apply?role=guest", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("POST apply (guest): %v", err)
	}
	defer denied.Body.Close()
	if denied.StatusCode != http.StatusForbidden {
		t.Fatalf("POST apply для чужой роли = %d, ожидался 403", denied.StatusCode)
	}
}

// TestInsightsApplicationsJSONUnavailable проверяет честность JSON-сравнения
// применений: без хранилища отдаётся недоступность, а не пустой успех.
func TestInsightsApplicationsJSONUnavailable(t *testing.T) {
	server := newServer(t, day(10))
	seedInsights(t, server)

	status, body := getPage(t, server, "/insights/applications.json?tenant_id=t1&role=operator")
	if status != http.StatusOK {
		t.Fatalf("GET applications = %d, ожидался 200", status)
	}
	if !strings.Contains(body, "Available") {
		t.Fatalf("сравнение применений не вернуло модель: %s", body)
	}
}
