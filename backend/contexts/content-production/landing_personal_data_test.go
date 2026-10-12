package contentproduction

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"lead-gen/backend/contexts/content-production/internal/app/command"
)

// Проверка границы персональных данных (задача 10.2): после полного цикла и серии
// заявок аудит хранилищ и журналов не находит персональных данных — ни из
// материалов клиента, ни из заявок посетителей.

const (
	// Данные заявителя: в хранилищах платформы им появляться негде.
	leadName  = "Посетитель Тестовый"
	leadEmail = "visitor@example.com"
	leadPhone = "+7 900 111-22-33"
)

// TestLandingPersonalDataBoundaryAudit доказывает критерий задачи 10.2.
func TestLandingPersonalDataBoundaryAudit(t *testing.T) {
	// Приёмник заявок в контуре клиента: страница отправляет заявки сюда, и только
	// сюда. Платформа заявки не принимает и не сохраняет.
	var delivered int
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		delivered++
		w.WriteHeader(http.StatusOK)
	}))
	defer receiver.Close()

	// Полный цикл генерации лендинга на реальной сборке среза. Сайт клиента
	// содержит персональные данные, материалы — личный контакт компании.
	fixture := runLandingCycle(t, receiver.URL)

	// Персональные данные с сайта исключены из собранного текста и о факте
	// исключения сообщено.
	collected := collectedText(fixture.collected)
	if len(fixture.collected.ExcludedPersonal) == 0 {
		t.Fatal("факт исключения персональных данных с сайта не сообщён")
	}
	assertNoPersonalData(t, "собранный с сайта текст", collected, sitePersonalMarkers())
	// Общий контакт компании при этом сохранён: исключение не задело нужные данные.
	if !strings.Contains(collected, siteGeneralMail) {
		t.Fatalf("общий контакт компании потерян при исключении: %q", collected)
	}

	// Личный контакт компании исключён из страницы с сообщением.
	if strings.Contains(fixture.page.HTML, companyPersonalPhone) {
		t.Fatal("личный контакт компании попал в страницу")
	}
	if len(fixture.page.ExcludedContacts) == 0 {
		t.Fatal("факт исключения личного контакта компании не сообщён")
	}

	// Аудит хранилищ и журналов после полного цикла.
	afterCycle := platformAuditDump(t, fixture.slice)
	// Дамп действительно содержит сохранённое наполнение: иначе поиск был бы
	// вырожденным (проверка пустоты вместо проверки границы).
	if !strings.Contains(afterCycle, "интернет-магазины") {
		t.Fatal("аудит не видит сохранённое наполнение: проверка вырождена")
	}
	materialMarkers := append(sitePersonalMarkers(), companyPersonalPhone)
	assertNoPersonalData(t, "аудит после полного цикла", afterCycle, materialMarkers)

	// Серия заявок: посетитель отправляет заявки в приёмник клиента.
	for i := 0; i < 5; i++ {
		resp, err := http.PostForm(receiver.URL, url.Values{
			"name":  {leadName},
			"email": {leadEmail},
			"phone": {leadPhone},
		})
		if err != nil {
			t.Fatalf("заявка %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("статус приёма заявки %d: %d", i, resp.StatusCode)
		}
	}
	if delivered != 5 {
		t.Fatalf("приёмник клиента получил %d заявок, ожидалось 5", delivered)
	}

	// Заявки не изменили состояние платформы и не осели в её хранилищах.
	afterLeads := platformAuditDump(t, fixture.slice)
	if afterLeads != afterCycle {
		t.Fatal("серия заявок изменила состояние платформы")
	}
	assertNoPersonalData(t, "аудит после серии заявок", afterLeads, []string{leadName, leadEmail, leadPhone})
}

// collectedText объединяет очищенные страницы, собранные с сайта клиента.
func collectedText(result command.CollectSiteResult) string {
	var b strings.Builder
	for _, page := range result.Pages {
		b.WriteString(page.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// sitePersonalMarkers возвращает персональные данные, найденные на сайте клиента.
func sitePersonalMarkers() []string {
	return []string{siteDirectorName, sitePersonalMail, sitePersonalPhone}
}

// platformAuditDump сериализует всё, что платформа сохранила: снимки
// контент-пакетов и заданий рендеринга и ожидающие записи журнала outbox.
func platformAuditDump(t *testing.T, slice *Slice) string {
	t.Helper()
	ctx := context.Background()

	var b strings.Builder
	// %+v печатает и неэкспортированные поля: карты хранилища целиком, включая
	// тексты версий и наполнение.
	fmt.Fprintf(&b, "store: %+v\n", slice.Store)
	pending, err := slice.Journal.Pending(ctx, 0)
	if err != nil {
		t.Fatalf("Journal.Pending: %v", err)
	}
	for _, message := range pending {
		fmt.Fprintf(&b, "outbox %s %s %s %s %v\n", message.ID, message.Exchange, message.RoutingKey, string(message.Body), message.Headers)
	}
	return b.String()
}

// assertNoPersonalData проверяет отсутствие маркеров персональных данных в дампе.
func assertNoPersonalData(t *testing.T, where, haystack string, markers []string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(haystack, marker) {
			t.Fatalf("%s: обнаружены персональные данные %q", where, marker)
		}
	}
}
