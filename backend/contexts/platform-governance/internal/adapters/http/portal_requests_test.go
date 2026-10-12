package httpapi_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/analytics/campaignreport"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// fakeListRequests — управляемый порт request-intent, хранящий несколько заявок на
// тенант и позволяющий менять список во время теста (для проверки SSE-обновления).
type fakeListRequests struct {
	mu      sync.Mutex
	entries map[string][]requestintent.ListEntry
}

func newFakeListRequests() *fakeListRequests {
	return &fakeListRequests{entries: map[string][]requestintent.ListEntry{}}
}

func (f *fakeListRequests) set(tenantID string, entries ...requestintent.ListEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries[tenantID] = append([]requestintent.ListEntry(nil), entries...)
}

// Submit реализует requestintent.Port.
func (f *fakeListRequests) Submit(_ context.Context, in requestintent.SubmitIntent) (requestintent.SubmitResult, error) {
	return requestintent.SubmitResult{RequestID: in.RequestID, Status: "submitted"}, nil
}

// Status реализует requestintent.Port.
func (f *fakeListRequests) Status(_ context.Context, q requestintent.StatusQuery) (requestintent.StatusResult, error) {
	return requestintent.StatusResult{RequestID: q.RequestID, Status: "submitted"}, nil
}

// List реализует requestintent.Port. Возвращает копию списка, чтобы мутация
// владельца не меняла уже отданный срез.
func (f *fakeListRequests) List(_ context.Context, q requestintent.ListQuery) ([]requestintent.ListEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]requestintent.ListEntry(nil), f.entries[q.TenantID]...), nil
}

// fakeReport — управляемый порт campaign-report для метрик главного экрана.
type fakeReport struct {
	mu      sync.Mutex
	numbers map[string]campaignreport.ClientNumbers
	err     error
}

func newFakeReport() *fakeReport {
	return &fakeReport{numbers: map[string]campaignreport.ClientNumbers{}}
}

func (f *fakeReport) set(clientID string, numbers campaignreport.ClientNumbers) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.numbers[clientID] = numbers
}

// Numbers реализует campaignreport.Port.
func (f *fakeReport) Numbers(context.Context, campaignreport.Request) (campaignreport.NumbersResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return campaignreport.NumbersResponse{}, f.err
	}
	clients := make([]campaignreport.ClientNumbers, 0, len(f.numbers))
	for _, numbers := range f.numbers {
		clients = append(clients, numbers)
	}
	return campaignreport.NumbersResponse{Clients: clients, Count: len(clients)}, nil
}

// Clients реализует campaignreport.Port.
func (f *fakeReport) Clients(context.Context, campaignreport.Request) (campaignreport.ClientsResponse, error) {
	return campaignreport.ClientsResponse{}, nil
}

// newPortalServer собирает сервер портала с правами администратора, заданным
// портом заявок и (опционально) портом метрик, а также периодами SSE-потока.
func newPortalServer(t *testing.T, requests requestintent.Port, report campaignreport.Port, poll, heartbeat time.Duration) (*platformgovernance.Slice, *httptest.Server) {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{
		Grants:                adminGrants,
		Requests:              requests,
		Report:                report,
		PortalStreamPoll:      poll,
		PortalStreamHeartbeat: heartbeat,
		Clock:                 fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)},
		IDs:                   &seqIDs{},
	})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return slice, server
}

// entry — короткая запись списка заявок для теста.
func entry(requestID, status string) requestintent.ListEntry {
	return requestintent.ListEntry{RequestID: requestID, Status: status}
}

// TestPortalShowsRequestTree проверяет, что главный экран рисует дерево истории
// заявок клиента: список текущих статусов из requestintent.List.
func TestPortalShowsRequestTree(t *testing.T) {
	requests := newFakeListRequests()
	slice, server := newPortalServer(t, requests, nil, time.Second, time.Hour)
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	requests.set(tenantID,
		entry("req-1", "submitted"),
		entry("req-2", "processing"),
	)

	page := body(t, getCookie(t, server, "/portal", cookie))
	for _, want := range []string{"История заявок", "req-1", "req-2", "Принята", "В работе"} {
		if !strings.Contains(page, want) {
			t.Fatalf("портал не показал %q в дереве заявок:\n%s", want, page)
		}
	}
}

// TestPortalShowsMetricsFromReport проверяет, что метрики главного экрана приходят
// из контракта campaign-report: found → «Найдено лидов», processed →
// «Взаимодействий», result → «Откликов».
func TestPortalShowsMetricsFromReport(t *testing.T) {
	requests := newFakeListRequests()
	report := newFakeReport()
	slice, server := newPortalServer(t, requests, report, time.Second, time.Hour)
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	report.set(tenantID, campaignreport.ClientNumbers{
		ClientID:   tenantID,
		Found:      7,
		Processed:  4,
		Rejected:   1,
		InProgress: 2,
		Result:     3,
	})

	page := body(t, getCookie(t, server, "/portal", cookie))
	for _, want := range []string{
		"Найдено лидов</dt><dd>7</dd>",
		"Взаимодействий</dt><dd>4</dd>",
		"Откликов</dt><dd>3</dd>",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("портал не показал метрику %q:\n%s", want, page)
		}
	}
}

// TestPortalMetricsUnavailableWithoutReport проверяет честный «—» вместо нулей,
// когда владелец метрик (analytics) не подключён.
func TestPortalMetricsUnavailableWithoutReport(t *testing.T) {
	requests := newFakeListRequests()
	slice, server := newPortalServer(t, requests, nil, time.Second, time.Hour)
	registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	page := body(t, getCookie(t, server, "/portal", cookie))
	for _, want := range []string{
		"Найдено лидов</dt><dd>—</dd>",
		"Взаимодействий</dt><dd>—</dd>",
		"Откликов</dt><dd>—</dd>",
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("портал не показал честное «—» для %q:\n%s", want, page)
		}
	}
}

// TestPortalStreamEmitsInitialAndUpdate проверяет poll-backed SSE: первый кадр
// отдаётся сразу, а после изменения списка статусов у владельца приходит свежий
// кадр без перезагрузки страницы.
func TestPortalStreamEmitsInitialAndUpdate(t *testing.T) {
	requests := newFakeListRequests()
	slice, server := newPortalServer(t, requests, nil, 20*time.Millisecond, time.Hour)
	tenantID, _ := registerAndApprove(t, slice, "client@romashka.ru", "secret", "7701234567", "hello@romashka.ru")
	cookie := login(t, server, "client@romashka.ru", "secret")

	requests.set(tenantID, entry("req-1", "submitted"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/portal/stream", nil)
	if err != nil {
		t.Fatalf("запрос потока: %v", err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("открытие потока: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, ожидался text/event-stream", ct)
	}
	reader := bufio.NewReader(resp.Body)

	first := readPortalFrame(t, reader)
	if first.Unavailable {
		t.Fatalf("первый кадр пометил владельца недоступным: %+v", first)
	}
	if len(first.Requests) != 1 || first.Requests[0].RequestID != "req-1" || first.Requests[0].Status != "submitted" {
		t.Fatalf("первый кадр = %+v, ожидалась одна заявка req-1/submitted", first)
	}

	// Владелец переводит заявку и добавляет вторую: подписчик получает свежий кадр.
	requests.set(tenantID, entry("req-1", "processing"), entry("req-2", "submitted"))

	updated := readPortalFrame(t, reader)
	if updated.Unavailable {
		t.Fatalf("кадр обновления пометил владельца недоступным: %+v", updated)
	}
	if len(updated.Requests) != 2 {
		t.Fatalf("кадр обновления = %+v, ожидались две заявки", updated)
	}
	byID := map[string]string{}
	for _, r := range updated.Requests {
		byID[r.RequestID] = r.Status
	}
	if byID["req-1"] != "processing" || byID["req-2"] != "submitted" {
		t.Fatalf("кадр обновления = %+v", updated)
	}
}

// portalStreamRequest — строка заявки в кадре SSE (только для разбора в тесте).
type portalStreamRequest struct {
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
}

// portalStreamFrame — разобранный кадр SSE-потока портала.
type portalStreamFrame struct {
	Requests    []portalStreamRequest `json:"requests"`
	Unavailable bool                  `json:"unavailable"`
}

// readPortalFrame читает один кадр SSE с ограничением по времени, чтобы тест не
// завис при отсутствии обновления.
func readPortalFrame(t *testing.T, reader *bufio.Reader) portalStreamFrame {
	t.Helper()
	type outcome struct {
		frame portalStreamFrame
		err   error
	}
	result := make(chan outcome, 1)
	go func() {
		var data strings.Builder
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				result <- outcome{err: err}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				// Пустая строка завершает кадр: разбираем собранные data-строки.
				var frame portalStreamFrame
				if data.Len() > 0 {
					_ = json.Unmarshal([]byte(data.String()), &frame)
				}
				result <- outcome{frame: frame}
				return
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			field, value, _ := strings.Cut(line, ":")
			value = strings.TrimPrefix(value, " ")
			if field == "data" {
				data.WriteString(value)
			}
		}
	}()
	select {
	case out := <-result:
		if out.err != nil {
			t.Fatalf("чтение кадра потока: %v", out.err)
		}
		return out.frame
	case <-time.After(5 * time.Second):
		t.Fatalf("кадр потока не получен за 5с")
		return portalStreamFrame{}
	}
}
