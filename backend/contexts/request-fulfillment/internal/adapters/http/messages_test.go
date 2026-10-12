package httpapi

import (
	"strings"
	"testing"

	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
)

// TestEveryStatusHasLabel проверяет полноту перечисления: для каждого статуса из
// domain.RequestStatuses() есть непустая читаемая подпись — новый статус не может
// отобразиться сырым кодом (или пропасть) из-за пропуска в каталоге сообщений.
func TestEveryStatusHasLabel(t *testing.T) {
	for _, status := range domain.RequestStatuses() {
		label := consoleMessages().Label("request.status", string(status))
		if strings.TrimSpace(label) == "" {
			t.Fatalf("статус %q без подписи", status)
		}
		if label == string(status) {
			t.Fatalf("статус %q отображается сырым кодом: подпись не объявлена", status)
		}
	}
}

// TestFailedStatusLabelRussian проверяет, что failed показывается по-русски и
// попадает в JSON-объект подписей для клиентского обновления строк.
func TestFailedStatusLabelRussian(t *testing.T) {
	label := consoleMessages().Label("request.status", string(domain.StatusFailed))
	if label != "Сбой" {
		t.Fatalf("подпись failed = %q, ожидалась «Сбой»", label)
	}
	if !strings.Contains(statusLabelsJSON(), `"failed":"Сбой"`) {
		t.Fatalf("statusLabelsJSON не содержит failed: %s", statusLabelsJSON())
	}
}

func TestRequestStatusLabelsForDemo(t *testing.T) {
	for _, status := range []domain.RequestStatus{
		domain.StatusSubmitted,
		domain.StatusValidating,
		domain.StatusValidated,
	} {
		if label := consoleMessages().Label("request.status", string(status)); label != "Создана" {
			t.Errorf("подпись %s = %q, ожидалась «Создана»", status, label)
		}
	}
	for status, want := range map[domain.RequestStatus]string{
		domain.StatusProcessing:    "В работе",
		domain.StatusCancelled:     "Отменена",
		domain.StatusFulfilled:     "Выполнена",
		domain.StatusRejected:      "Отклонена",
		domain.StatusNeedsRevision: "Нужна доработка",
		domain.StatusFailed:        "Сбой",
	} {
		if label := consoleMessages().Label("request.status", string(status)); label != want {
			t.Errorf("подпись %s = %q, ожидалась %q", status, label, want)
		}
	}
}
