package httpapi_test

import (
	"flag"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// approvalTokenRe — случайные токены (CSRF-токен сессии) в разметке. Они
// генерируются на каждый запуск, поэтому в золотом файле заменяются на TOKEN.
var approvalTokenRe = regexp.MustCompile(`[0-9a-f]{64}`)

// updateApprovalGolden — при true перезаписывает золотые файлы экранов
// утверждения текущим выводом.
var updateApprovalGolden = flag.Bool("update", false, "перезаписать золотые файлы экранов утверждения")

// approvalDecisionForm собирает форму решения оператора с валидным CSRF-токеном
// сессии клиента.
func approvalDecisionForm(client *consoleClient) url.Values {
	return url.Values{"tenant_id": {tenantID}, "csrf": {client.csrf}}
}

// assertApprovalGolden сравнивает разметку с золотым файлом. С флагом -update
// золотой файл перезаписывается. Волатильные токены нормализуются: рендер не
// зависит от конкретной сессии.
func assertApprovalGolden(t *testing.T, name, body string) {
	t.Helper()
	got := approvalTokenRe.ReplaceAllString(body, "TOKEN")

	golden := filepath.Join("testdata", name)
	if *updateApprovalGolden {
		if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
			t.Fatalf("создание testdata: %v", err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatalf("запись золотого файла: %v", err)
		}
		return
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("чтение золотого файла %s: %v; запустите тест с -update для генерации", golden, err)
	}
	if got != string(want) {
		t.Fatalf("рендер не совпадает с золотым файлом %s\n--- получено ---\n%s\n--- ожидалось ---\n%s", golden, got, want)
	}
}

// TestApprovalQueueRenderGolden фиксирует полную страницу очереди с заявкой на
// паузе: разметка страницы не меняется незаметно.
func TestApprovalQueueRenderGolden(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(7)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
	assertApprovalGolden(t, "approval-queue.golden", body)
}

// TestApprovalQueueEmptyRenderGolden фиксирует пустое состояние очереди: оно
// отличается от недоступности исполнения отдельным золотым файлом.
func TestApprovalQueueEmptyRenderGolden(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: ports.WorkflowStatus{Phase: "searching"}}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
	assertApprovalGolden(t, "approval-queue-empty.golden", body)
}

// TestApprovalQueueUnavailableRenderGolden фиксирует состояние «исполнение
// недоступно»: оно не выдаётся за пустую очередь.
func TestApprovalQueueUnavailableRenderGolden(t *testing.T) {
	server := newApprovalServer(t, nil, nil)
	client := prepareApprovalClient(t, server)

	body := readBody(t, client.get(t, "/approvals?tenant_id="+tenantID))
	assertApprovalGolden(t, "approval-queue-unavailable.golden", body)
}

// TestApprovalStatusFragmentRenderGolden фиксирует фрагмент состояния исполнения,
// который HTMX подставляет после решения оператора.
func TestApprovalStatusFragmentRenderGolden(t *testing.T) {
	server := newApprovalServer(t, stubWorkflow{status: awaitingStatus(4)}, stubWorkflow{})
	client := prepareApprovalClient(t, server)

	resp := client.postFormHTMX(t, "/requests/"+requestID+"/approval/approve", approvalDecisionForm(client))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("HTMX-утверждение = %d, ожидался 200: %s", resp.StatusCode, readBody(t, resp))
	}
	assertApprovalGolden(t, "approval-status.golden", readBody(t, resp))
}
