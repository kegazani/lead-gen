package prometheus

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client — клиент HTTP API Prometheus. Нулевое значение не готово к работе:
// клиент собирают через NewClient. После сборки клиент неизменяем и
// потокобезопасен.
type Client struct {
	// baseURL — нормализованный адрес API без завершающего слеша.
	baseURL string
	// rt — транспорт HTTP-вызова. Повторы и предохранитель — не его забота:
	// их навешивает вызывающий, обернув RoundTripper.
	rt http.RoundTripper
}

// NewClient собирает клиент для baseURL. Завершающий слеш нормализуется, поэтому
// `http://host:9090` и `http://host:9090/` равнозначны. Нулевой RoundTripper
// заменяется на http.DefaultTransport; таймауты задаются контекстом вызова, а не
// клиентом, поэтому http.Client здесь не используется.
func NewClient(baseURL string, rt http.RoundTripper) *Client {
	if rt == nil {
		rt = http.DefaultTransport
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		rt:      rt,
	}
}

// Query выполняет instant-запрос и возвращает вектор. У каждой серии ровно один
// Sample. Второе значение — предупреждения из ответа Prometheus (может быть
// пустым); они возвращаются и вместе с ошибкой.
func (c *Client) Query(ctx context.Context, query string) (Vector, []string, error) {
	params := url.Values{}
	params.Set("query", query)
	// Instant-запрос без времени сервер вычисляет на своё «сейчас». Передаём
	// время явно, чтобы поведение не зависело от часов сервера.
	params.Set("time", formatTime(time.Now()))

	env, err := c.do(ctx, queryPath, params)
	if err != nil {
		return nil, nil, err
	}
	data, err := decodeData(env)
	if err != nil {
		return nil, env.Warnings, err
	}
	if data.ResultType != resultTypeVector {
		return nil, env.Warnings, fmt.Errorf("prometheus: неожиданный resultType %q для instant-запроса, ожидался %q", data.ResultType, resultTypeVector)
	}
	vector, err := parseVector(data)
	if err != nil {
		return nil, env.Warnings, fmt.Errorf("prometheus: %w", err)
	}
	return vector, env.Warnings, nil
}

// QueryRange выполняет range-запрос и возвращает матрицу. step задаётся
// длительностью и передаётся в секундах с поддержкой дробных значений. Второе
// значение — предупреждения из ответа Prometheus (может быть пустым).
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (Matrix, []string, error) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("start", formatTime(start))
	params.Set("end", formatTime(end))
	params.Set("step", formatStep(step))

	env, err := c.do(ctx, queryRangePath, params)
	if err != nil {
		return nil, nil, err
	}
	data, err := decodeData(env)
	if err != nil {
		return nil, env.Warnings, err
	}
	if data.ResultType != resultTypeMatrix {
		return nil, env.Warnings, fmt.Errorf("prometheus: неожиданный resultType %q для range-запроса, ожидался %q", data.ResultType, resultTypeMatrix)
	}
	matrix, err := parseMatrix(data)
	if err != nil {
		return nil, env.Warnings, fmt.Errorf("prometheus: %w", err)
	}
	return matrix, env.Warnings, nil
}

// do выполняет один GET-запрос к пути API и возвращает разобранный конверт.
//
// Разбор идёт до проверки кода ответа: errorType и error приходят в теле, и они
// нужны и на не-2xx. Битый JSON на не-2xx не маскирует код ответа — наружу всё
// равно уходит APIError.
func (c *Client) do(ctx context.Context, path string, params url.Values) (apiResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	u := c.baseURL + path
	if encoded := params.Encode(); encoded != "" {
		u += "?" + encoded
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return apiResponse{}, fmt.Errorf("prometheus: сборка запроса %s: %w", u, err)
	}

	resp, err := c.rt.RoundTrip(req)
	if err != nil {
		return apiResponse{}, fmt.Errorf("prometheus: вызов %s: %w", u, err)
	}
	defer resp.Body.Close()

	body, err := readBody(resp.Body)
	if err != nil {
		return apiResponse{}, fmt.Errorf("prometheus: чтение ответа %s: %w", u, err)
	}

	var env apiResponse
	ok := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices
	if len(body) > 0 {
		if err := json.Unmarshal(body, &env); err != nil && ok {
			return apiResponse{}, fmt.Errorf("prometheus: разбор ответа %s: %w", u, err)
		}
	}

	if !ok {
		return apiResponse{}, &APIError{
			Status:    strconv.Itoa(resp.StatusCode),
			ErrorType: env.ErrorType,
			Message:   env.Error,
		}
	}
	if env.Status != statusSuccess {
		return apiResponse{}, &APIError{
			Status:    strconv.Itoa(resp.StatusCode),
			ErrorType: env.ErrorType,
			Message:   env.Error,
		}
	}
	return env, nil
}

// readBody читает ответ, ограничивая его размер. Превышение лимита — ошибка,
// а не молчаливое усечение: усечённый JSON всё равно не разберётся, но ошибка
// про лимит объясняет причину.
func readBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxResponseBytes {
		return nil, fmt.Errorf("ответ превышает лимит %d байт", maxResponseBytes)
	}
	return data, nil
}
