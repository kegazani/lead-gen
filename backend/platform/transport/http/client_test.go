package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/backend/platform/contracts"
	"lead-gen/backend/platform/reliability/resilience"
)

// fakeBreaker — предохранитель с управляемым решением и журналом исходов.
type fakeBreaker struct {
	allow bool

	mu      sync.Mutex
	records []bool
}

func (b *fakeBreaker) Allow() bool { return b.allow }

func (b *fakeBreaker) Record(success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.records = append(b.records, success)
}

func (b *fakeBreaker) recorded() []bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]bool(nil), b.records...)
}

func commandEnvelope() contracts.Envelope {
	return contracts.Envelope{
		EventID:       "evt-7",
		OccurredAt:    time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "campaign-1",
		CausationID:   "decision-1",
		WorkflowID:    "wf-42",
		Name:          "approved-action",
		SchemaVersion: "1.2.0",
	}
}

func TestClientCommandRoundTrip(t *testing.T) {
	var (
		mu       sync.Mutex
		gotReq   Request
		gotCount int
	)
	server := httptest.NewServer(NewServer(func(_ context.Context, req Request) (Response, error) {
		mu.Lock()
		gotReq = req
		gotCount++
		mu.Unlock()
		return Response{
			StatusCode: http.StatusAccepted,
			Body:       []byte(`{"ok":true}`),
			Envelope:   contracts.Envelope{EventID: "resp-1", Name: "approved-action", SchemaVersion: "1.2.0"},
		}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, &fakeBreaker{allow: true})
	resp, err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		URL:            server.URL + "/approved-action",
		Body:           []byte(`{"draft_id":"d-1"}`),
		Envelope:       commandEnvelope(),
		IdempotencyKey: "approved-action|tenant-1|d-1|3",
		Timeout:        time.Second,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("статус = %d", resp.StatusCode)
	}
	if string(resp.Body) != `{"ok":true}` {
		t.Fatalf("тело ответа = %q", resp.Body)
	}
	if resp.Envelope.EventID != "resp-1" {
		t.Fatalf("конверт ответа потерян: %+v", resp.Envelope)
	}

	mu.Lock()
	defer mu.Unlock()
	if gotCount != 1 {
		t.Fatalf("обработчик вызван %d раз", gotCount)
	}
	if gotReq.Method != http.MethodPost || string(gotReq.Body) != `{"draft_id":"d-1"}` {
		t.Fatalf("обработчик получил неверный запрос: %+v", gotReq)
	}
	if gotReq.IdempotencyKey != "approved-action|tenant-1|d-1|3" {
		t.Fatalf("ключ идемпотентности потерян: %q", gotReq.IdempotencyKey)
	}
	if gotReq.Envelope.CorrelationID != "campaign-1" || gotReq.Envelope.CausationID != "decision-1" {
		t.Fatalf("корреляция потеряна: %+v", gotReq.Envelope)
	}
	if gotReq.Envelope.WorkflowID != "wf-42" {
		t.Fatalf("workflow_id потерян при передаче по транспорту: %+v", gotReq.Envelope)
	}
	if gotReq.Envelope.SchemaVersion != "1.2.0" || gotReq.Envelope.TenantID != "tenant-1" {
		t.Fatalf("версия или тенант потеряны: %+v", gotReq.Envelope)
	}
}

func TestClientQueryRoundTrip(t *testing.T) {
	server := httptest.NewServer(NewServer(func(_ context.Context, req Request) (Response, error) {
		if req.Method != http.MethodGet {
			t.Errorf("query должен идти методом GET, пришёл %s", req.Method)
		}
		return Response{StatusCode: http.StatusOK, Body: []byte(`{"items":[]}`)}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, &fakeBreaker{allow: true})
	resp, err := client.Do(context.Background(), Request{
		Method:   http.MethodGet,
		URL:      server.URL + "/approvals?status=pending",
		Envelope: contracts.Envelope{CorrelationID: "campaign-1", Name: "pending-approvals", SchemaVersion: "1.0.0"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if resp.StatusCode != http.StatusOK || string(resp.Body) != `{"items":[]}` {
		t.Fatalf("ответ = %+v", resp)
	}
}

func TestClientRetriesIdempotentCommand(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		if calls.Add(1) < 3 {
			return Response{}, context.DeadlineExceeded // отказ обработки → 500
		}
		return Response{StatusCode: http.StatusOK}, nil
	}))
	defer server.Close()

	breaker := &fakeBreaker{allow: true}
	client := NewClient(server.Client().Transport, resilience.RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
	}, breaker)

	_, err := client.Do(context.Background(), Request{
		Method:         http.MethodPost,
		URL:            server.URL + "/approved-action",
		Envelope:       commandEnvelope(),
		IdempotencyKey: "key-1",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("попыток = %d, ожидалось 3", got)
	}
	if rec := breaker.recorded(); len(rec) != 1 || !rec[0] {
		t.Fatalf("предохранитель должен зафиксировать один успех: %v", rec)
	}
}

func TestClientDoesNotRetryCommandWithoutKey(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		calls.Add(1)
		return Response{}, context.DeadlineExceeded
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{
		MaxAttempts: 5,
		BaseDelay:   time.Millisecond,
	}, &fakeBreaker{allow: true})

	_, err := client.Do(context.Background(), Request{
		Method:   http.MethodPost,
		URL:      server.URL + "/approved-action",
		Envelope: commandEnvelope(),
		// Ключ не задан: command неидемпотентен.
	})
	if err == nil {
		t.Fatal("ожидалась ошибка downstream")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("неидемпотентный command повторён %d раз", got)
	}
}

func TestClientReturns4xxWithoutRetry(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		calls.Add(1)
		return Response{StatusCode: http.StatusConflict, Body: []byte(`{"error":"version"}`)}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Millisecond}, &fakeBreaker{allow: true})
	resp, err := client.Do(context.Background(), Request{
		Method:   http.MethodGet,
		URL:      server.URL + "/approvals",
		Envelope: commandEnvelope(),
	})
	if err != nil {
		t.Fatalf("4xx не должен быть ошибкой транспорта: %v", err)
	}
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("статус = %d", resp.StatusCode)
	}
	if calls.Load() != 1 {
		t.Fatalf("4xx не должен повторяться: %d", calls.Load())
	}
}

func TestClientRejectsWhenBreakerOpen(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(NewServer(func(_ context.Context, _ Request) (Response, error) {
		calls.Add(1)
		return Response{StatusCode: http.StatusOK}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, &fakeBreaker{allow: false})
	if _, err := client.Do(context.Background(), Request{Method: http.MethodGet, URL: server.URL, Envelope: commandEnvelope()}); err == nil {
		t.Fatal("при разомкнутом предохранителе вызов должен отклоняться")
	}
	if calls.Load() != 0 {
		t.Fatalf("downstream не должен быть вызван: %d", calls.Load())
	}
}

func TestClientHonorsTimeoutBudget(t *testing.T) {
	server := httptest.NewServer(NewServer(func(ctx context.Context, _ Request) (Response, error) {
		select {
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
		}
		return Response{StatusCode: http.StatusOK}, nil
	}))
	defer server.Close()

	client := NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, &fakeBreaker{allow: true})
	start := time.Now()
	_, err := client.Do(context.Background(), Request{
		Method:   http.MethodGet,
		URL:      server.URL,
		Envelope: commandEnvelope(),
		Timeout:  20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("вызов должен завершиться по бюджету")
	}
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("бюджет не соблюдён: %v", elapsed)
	}
}

func TestIsIdempotentClassification(t *testing.T) {
	cases := []struct {
		req  Request
		want bool
	}{
		{Request{Method: http.MethodGet}, true},
		{Request{Method: http.MethodHead}, true},
		{Request{Method: http.MethodPost, IdempotencyKey: "k"}, true},
		{Request{Method: http.MethodPost}, false},
		{Request{Method: http.MethodPut}, false},
	}
	for _, c := range cases {
		if got := isIdempotent(c.req); got != c.want {
			t.Errorf("isIdempotent(%+v) = %v, ожидалось %v", c.req, got, c.want)
		}
	}
}

func TestNewClientNilTransportUsesDefault(t *testing.T) {
	c := NewClient(nil, resilience.RetryPolicy{MaxAttempts: 1}, nil)
	if c == nil {
		t.Fatal("NewClient вернул nil")
	}
	if _, err := c.Do(context.Background(), Request{Method: http.MethodGet, URL: "://"}); err == nil {
		t.Fatal("некорректный URL должен вернуть ошибку, а не панику")
	}
}
