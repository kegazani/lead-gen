package prometheus

import (
	"bytes"
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// serve поднимает тестовый сервер Prometheus и возвращает клиент к нему.
func serve(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return NewClient(srv.URL, srv.Client().Transport)
}

// writeJSON отдаёт тело с типом JSON.
func writeJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

func TestQueryInstant(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("instant-запрос должен идти методом GET, пришёл %s", r.Method)
		}
		if r.URL.Path != queryPath {
			t.Errorf("путь = %q, ожидался %q", r.URL.Path, queryPath)
		}
		q := r.URL.Query()
		if got := q.Get("query"); got != "up" {
			t.Errorf("параметр query = %q", got)
		}
		if q.Get("time") == "" {
			t.Error("параметр time не передан")
		}
		writeJSON(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"up","job":"api"},"value":[1726400000.5,"1"]}]}}`)
	})

	vector, warnings, err := client.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("предупреждения = %v", warnings)
	}
	if len(vector) != 1 {
		t.Fatalf("серий = %d, ожидалось 1", len(vector))
	}
	if vector[0].Metric["__name__"] != "up" || vector[0].Metric["job"] != "api" {
		t.Fatalf("метки серии = %v", vector[0].Metric)
	}
	if len(vector[0].Samples) != 1 {
		t.Fatalf("точек в instant-серии = %d, ожидалось 1", len(vector[0].Samples))
	}
	sample := vector[0].Samples[0]
	if sample.Value != 1 {
		t.Fatalf("значение = %v", sample.Value)
	}
	want := time.Unix(1726400000, 500000000).UTC()
	if !sample.Time.Equal(want) {
		t.Fatalf("время = %v, ожидалось %v", sample.Time, want)
	}
}

func TestQueryRange(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != queryRangePath {
			t.Errorf("путь = %q, ожидался %q", r.URL.Path, queryRangePath)
		}
		q := r.URL.Query()
		if got := q.Get("query"); got != "rate(http_requests[1m])" {
			t.Errorf("параметр query = %q", got)
		}
		if got := q.Get("start"); got != "1726400000" {
			t.Errorf("параметр start = %q", got)
		}
		if got := q.Get("end"); got != "1726400060" {
			t.Errorf("параметр end = %q", got)
		}
		// Дробный шаг передаётся в секундах.
		if got := q.Get("step"); got != "1.5" {
			t.Errorf("параметр step = %q", got)
		}
		writeJSON(w, `{"status":"success","data":{"resultType":"matrix","result":[
			{"metric":{"__name__":"http_requests"},"values":[[1726400000,"1"],[1726400030,"2.5"]]}]}}`)
	})

	start := time.Unix(1726400000, 0)
	end := time.Unix(1726400060, 0)
	matrix, _, err := client.QueryRange(context.Background(), "rate(http_requests[1m])", start, end, 1500*time.Millisecond)
	if err != nil {
		t.Fatalf("QueryRange: %v", err)
	}
	if len(matrix) != 1 {
		t.Fatalf("серий = %d, ожидалось 1", len(matrix))
	}
	samples := matrix[0].Samples
	if len(samples) != 2 {
		t.Fatalf("точек = %d, ожидалось 2", len(samples))
	}
	if samples[0].Value != 1 || samples[1].Value != 2.5 {
		t.Fatalf("значения = %v, %v", samples[0].Value, samples[1].Value)
	}
	if !samples[1].Time.Equal(time.Unix(1726400030, 0).UTC()) {
		t.Fatalf("время второй точки = %v", samples[1].Time)
	}
}

func TestQuerySpecialFloatValues(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"v":"inf"},"value":[1,"+Inf"]},
			{"metric":{"v":"nan"},"value":[1,"NaN"]},
			{"metric":{"v":"neginf"},"value":[1,"-Inf"]}]}}`)
	})

	vector, _, err := client.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(vector) != 3 {
		t.Fatalf("серий = %d, ожидалось 3", len(vector))
	}
	if v := vector[0].Samples[0].Value; !math.IsInf(v, 1) {
		t.Errorf("значение = %v, ожидалось +Inf", v)
	}
	if v := vector[1].Samples[0].Value; !math.IsNaN(v) {
		t.Errorf("значение = %v, ожидалось NaN", v)
	}
	if v := vector[2].Samples[0].Value; !math.IsInf(v, -1) {
		t.Errorf("значение = %v, ожидалось -Inf", v)
	}
}

func TestQueryNon2xxReturnsAPIError(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		writeJSON(w, `{"status":"error","errorType":"bad_data","error":"invalid query"}`)
	})

	_, _, err := client.Query(context.Background(), "up{")
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ожидался *APIError, получено %T: %v", err, err)
	}
	if apiErr.Status != "422" {
		t.Errorf("Status = %q, ожидалось 422", apiErr.Status)
	}
	if apiErr.ErrorType != "bad_data" {
		t.Errorf("ErrorType = %q", apiErr.ErrorType)
	}
	if apiErr.Message != "invalid query" {
		t.Errorf("Message = %q", apiErr.Message)
	}
	if !strings.Contains(apiErr.Error(), "422") || !strings.Contains(apiErr.Error(), "invalid query") {
		t.Errorf("Error() = %q", apiErr.Error())
	}
}

func TestQueryErrorStatusWithOKCode(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		// Prometheus может вернуть status:"error" с кодом 200 (например, при
		// ошибке вычисления в самом запросе).
		writeJSON(w, `{"status":"error","errorType":"execution","error":"query timed out"}`)
	})

	_, _, err := client.Query(context.Background(), "up")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("ожидался *APIError, получено %T: %v", err, err)
	}
	if apiErr.Status != "200" || apiErr.ErrorType != "execution" {
		t.Fatalf("APIError = %+v", apiErr)
	}
}

func TestQueryBrokenJSON(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","data":`)
	})

	_, _, err := client.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("ожидалась ошибка разбора")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("битый JSON не должен быть *APIError: %v", err)
	}
}

func TestQueryUnsupportedResultType(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","data":{"resultType":"scalar","result":[1726400000,"1"]}}`)
	})

	_, _, err := client.Query(context.Background(), "1")
	if err == nil {
		t.Fatal("ожидалась ошибка про неподдержанный resultType")
	}
	if !strings.Contains(err.Error(), resultTypeVector) || !strings.Contains(err.Error(), "scalar") {
		t.Fatalf("непонятная ошибка: %v", err)
	}
}

func TestQueryRangeUnsupportedResultType(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	})

	_, _, err := client.QueryRange(context.Background(), "up", time.Now(), time.Now(), time.Second)
	if err == nil {
		t.Fatal("ожидалась ошибка про неподдержанный resultType")
	}
	if !strings.Contains(err.Error(), resultTypeMatrix) {
		t.Fatalf("непонятная ошибка: %v", err)
	}
}

func TestWarningsAreReturned(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","warnings":["partial data","stale marker"],
			"data":{"resultType":"vector","result":[]}}`)
	})

	_, warnings, err := client.Query(context.Background(), "up")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(warnings) != 2 || warnings[0] != "partial data" || warnings[1] != "stale marker" {
		t.Fatalf("предупреждения = %v", warnings)
	}
}

func TestQueryContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		// Держим запрос, пока клиент не разорвёт соединение отменой.
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewClient(srv.URL, srv.Client().Transport)
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		_, _, err := client.Query(ctx, "up")
		errCh <- err
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("отмена контекста должна вернуть ошибку")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ошибка = %v, ожидалась обёртка context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("вызов не прервался по отмене контекста")
	}
}

func TestQueryContextDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := NewClient(srv.URL, srv.Client().Transport)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, _, err := client.Query(ctx, "up")
	if err == nil {
		t.Fatal("истечение бюджета должно вернуть ошибку")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ошибка = %v, ожидалась обёртка context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("бюджет не соблюдён: %v", elapsed)
	}
}

func TestClientBaseURLNormalization(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)
	record := func(p string) {
		mu.Lock()
		paths = append(paths, p)
		mu.Unlock()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r.URL.Path)
		writeJSON(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
	}))
	defer srv.Close()

	bases := []string{srv.URL, srv.URL + "/", srv.URL + "//"}
	for _, base := range bases {
		client := NewClient(base, srv.Client().Transport)
		if _, _, err := client.Query(context.Background(), "up"); err != nil {
			t.Fatalf("baseURL %q: %v", base, err)
		}
	}

	// Базовый путь-префикс тоже сохраняется.
	prefixed := NewClient(srv.URL+"/prometheus/", srv.Client().Transport)
	if _, _, err := prefixed.Query(context.Background(), "up"); err != nil {
		t.Fatalf("baseURL с префиксом: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{queryPath, queryPath, queryPath, "/prometheus" + queryPath}
	if len(paths) != len(want) {
		t.Fatalf("путей = %v, ожидалось %v", paths, want)
	}
	for i, p := range paths {
		if p != want[i] {
			t.Fatalf("путь[%d] = %q, ожидался %q", i, p, want[i])
		}
	}
}

func TestQueryResponseTooLarge(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		chunk := bytes.Repeat([]byte("a"), 1<<16)
		written := 0
		for written <= int(maxResponseBytes) {
			n, err := w.Write(chunk)
			if err != nil {
				return
			}
			written += n
		}
	})

	_, _, err := client.Query(context.Background(), "up")
	if err == nil {
		t.Fatal("превышение лимита должно вернуть ошибку")
	}
	if !strings.Contains(err.Error(), "лимит") {
		t.Fatalf("непонятная ошибка: %v", err)
	}
}

func TestNewClientNilTransportUsesDefault(t *testing.T) {
	c := NewClient("http://127.0.0.1:9090", nil)
	if c.rt != http.DefaultTransport {
		t.Fatalf("нулевой RoundTripper должен заменяться на http.DefaultTransport, получено %T", c.rt)
	}
}

func TestClientConcurrent(t *testing.T) {
	client := serve(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, `{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"up"},"value":[1,"1"]}]}}`)
	})

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			vector, _, err := client.Query(context.Background(), "up")
			if err != nil {
				t.Errorf("Query: %v", err)
				return
			}
			if len(vector) != 1 || vector[0].Samples[0].Value != 1 {
				t.Errorf("неожиданный ответ: %+v", vector)
			}
		}()
	}
	wg.Wait()
}

func TestTimeFromSecondsFraction(t *testing.T) {
	cases := []struct {
		in   float64
		want time.Time
	}{
		{1726400000, time.Unix(1726400000, 0).UTC()},
		{1726400000.5, time.Unix(1726400000, 500000000).UTC()},
		{1726400000.25, time.Unix(1726400000, 250000000).UTC()},
	}
	for _, c := range cases {
		if got := timeFromSeconds(c.in); !got.Equal(c.want) {
			t.Errorf("timeFromSeconds(%v) = %v, ожидалось %v", c.in, got, c.want)
		}
	}
	if !timeFromSeconds(math.NaN()).IsZero() {
		t.Error("NaN во времени должен давать нулевое время")
	}
}

func TestFormatStepFraction(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30"},
		{1500 * time.Millisecond, "1.5"},
		{time.Minute, "60"},
	}
	for _, c := range cases {
		if got := formatStep(c.in); got != c.want {
			t.Errorf("formatStep(%v) = %q, ожидалось %q", c.in, got, c.want)
		}
	}
}
