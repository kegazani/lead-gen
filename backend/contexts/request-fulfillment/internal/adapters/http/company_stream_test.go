package httpapi_test

import (
	"bufio"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// sseEvent — кадр потока серверных событий: имя события и тело data.
type sseEvent struct {
	event string
	data  string
}

// openCompanyStream открывает SSE-поток заявок компании с cookie сессии клиента
// и возвращает канал кадров и функцию закрытия. Закрытие тела останавливает
// обработчик потока на сервере.
func openCompanyStream(t *testing.T, client *consoleClient) (<-chan sseEvent, func()) {
	t.Helper()
	resp := client.do(t, http.MethodGet, "/company/stream?tenant_id="+tenantID, nil, false)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("GET /company/stream = %d, ожидался 200: %s", resp.StatusCode, readBody(t, resp))
	}
	events := make(chan sseEvent, 16)
	done := make(chan struct{})
	go func() {
		defer close(events)
		reader := bufio.NewReader(resp.Body)
		var current sseEvent
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if current.event != "" || current.data != "" {
					select {
					case events <- current:
					case <-done:
						return
					}
				}
				current = sseEvent{}
				continue
			}
			switch {
			case strings.HasPrefix(line, "event: "):
				current.event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				current.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	cancel := func() {
		close(done)
		resp.Body.Close()
	}
	return events, cancel
}

// waitSSEEvent ждёт следующий кадр потока в пределах отведённого времени.
func waitSSEEvent(t *testing.T, events <-chan sseEvent) sseEvent {
	t.Helper()
	select {
	case event, ok := <-events:
		if !ok {
			t.Fatalf("поток закрылся до появления кадра")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatalf("кадр потока не пришёл за 5 с")
		return sseEvent{}
	}
}

// decodeStatuses разбирает data кадра потока в список [{request_id, status}].
func decodeStatuses(t *testing.T, data string) []map[string]string {
	t.Helper()
	var rows []map[string]string
	if err := json.Unmarshal([]byte(data), &rows); err != nil {
		t.Fatalf("разбор кадра потока %q: %v", data, err)
	}
	return rows
}

// TestCompanyStreamFirstFrame проверяет, что подписчик сразу получает текущие
// статусы заявок тенанта: первый кадр несёт событие statuses со списком заявок.
// Сервер собран через newScreenServer, который уже подал заявку req-1.
func TestCompanyStreamFirstFrame(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)

	events, cancel := openCompanyStream(t, client)
	defer cancel()

	first := waitSSEEvent(t, events)
	if first.event != "statuses" {
		t.Fatalf("событие первого кадра = %q, ожидался statuses", first.event)
	}
	rows := decodeStatuses(t, first.data)
	if len(rows) != 1 || rows[0]["request_id"] != requestID || rows[0]["status"] != "submitted" {
		t.Fatalf("первый кадр = %v, ожидалась заявка req-1 со статусом submitted", rows)
	}
}

// TestCompanyStreamNotifiesOnTransition проверяет, что после перехода заявки
// (Save в транзакции) подписчик получает новый кадр со свежим статусом.
func TestCompanyStreamNotifiesOnTransition(t *testing.T) {
	server := newScreenServer(t)
	client := newConsoleClient(server)
	client.login(t)

	events, cancel := openCompanyStream(t, client)
	defer cancel()

	first := waitSSEEvent(t, events)
	if rows := decodeStatuses(t, first.data); len(rows) != 1 || rows[0]["status"] != "submitted" {
		t.Fatalf("первый кадр = %v, ожидался submitted", rows)
	}

	// Переход submitted → validating через контрактный эндпоинт: команда идёт
	// через unit-of-work, который уведомляет подписчиков после коммита.
	resp := do(t, server, http.MethodPost, "/requests/"+requestID+"/validation/start", transition(tenantID))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("переход validation/start = %d: %s", resp.StatusCode, readBody(t, resp))
	}
	resp.Body.Close()

	second := waitSSEEvent(t, events)
	if second.event != "statuses" {
		t.Fatalf("событие второго кадра = %q, ожидался statuses", second.event)
	}
	if rows := decodeStatuses(t, second.data); len(rows) != 1 || rows[0]["status"] != "validating" {
		t.Fatalf("второй кадр = %v, ожидался validating", rows)
	}
}

// TestCompanyStreamRequiresSession проверяет, что поток без сессии оператора
// редиректится на вход, как и экран /company.
func TestCompanyStreamRequiresSession(t *testing.T) {
	server := newScreenServer(t)
	client := noRedirectClient()

	resp, err := client.Get(server.URL + "/company/stream?tenant_id=" + tenantID)
	if err != nil {
		t.Fatalf("GET /company/stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET /company/stream без сессии = %d, ожидался 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("GET /company/stream без сессии: Location = %q, ожидался /login", loc)
	}
}

// TestCompanyStreamServesScript проверяет, что скрипт клиентской подписки
// раздаётся по /static/company_requests.js без сессии: он — статика, как общее
// оформление компонентов.
func TestCompanyStreamServesScript(t *testing.T) {
	server := newScreenServer(t)
	resp := do(t, server, http.MethodGet, "/static/company_requests.js", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("скрипт потока = %d, ожидался 200", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "EventSource") {
		t.Fatalf("скрипт потока не содержит подписку EventSource:\n%s", body)
	}
}
