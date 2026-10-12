package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lead-gen/backend/platform/reliability/resilience"
)

func TestServerDefaultsStatusToOK(t *testing.T) {
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		return Response{Body: []byte(`{}`)}, nil
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("статус = %d, ожидался 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
}

func TestServerReturns500OnHandlerError(t *testing.T) {
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		return Response{}, errors.New("отказ обработки")
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("статус = %d, ожидался 500", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "отказ обработки") {
		t.Fatalf("тело ошибки = %q", body)
	}
}

func TestServerReadsEnvelopeAndKeyFromHeaders(t *testing.T) {
	// Приёмник на канале: обработчик выполняется в чужой горутине, канал задаёт
	// корректное отношение «до» при чтении из теста.
	received := make(chan Request, 1)
	server := httptest.NewServer(NewServer(func(_ context.Context, req Request) (Response, error) {
		received <- req
		return Response{StatusCode: http.StatusOK}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, nil)
	_, err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		URL:            server.URL,
		Body:           []byte(`{}`),
		Envelope:       commandEnvelope(),
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}

	req := <-received
	if req.IdempotencyKey != "key-1" {
		t.Fatalf("ключ = %q", req.IdempotencyKey)
	}
	if req.Envelope.CorrelationID != "campaign-1" || req.Envelope.SchemaVersion != "1.2.0" || req.Envelope.TenantID != "tenant-1" {
		t.Fatalf("конверт восстановлен неверно: %+v", req.Envelope)
	}
	if req.Envelope.WorkflowID != "wf-42" {
		t.Fatalf("workflow_id потерян при восстановлении конверта: %+v", req.Envelope)
	}
	if req.Envelope.Name != "approved-action" {
		t.Fatalf("имя контракта потеряно: %+v", req.Envelope)
	}
}
