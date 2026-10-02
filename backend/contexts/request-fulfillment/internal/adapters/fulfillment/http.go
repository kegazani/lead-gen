package fulfillment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	transport "lead-gen/backend/platform/transport/http"
)

// HTTPQuery — реализация ports.FulfillmentQuery поверх синхронного
// query-контракта владельца по транспорту sync/http-json.
//
// Каждому виду артефакта соответствует базовый адрес владельца; статус
// запрашивается как GET <base>/<kind>/<id>. Ответ — JSON {"status":"...",
// "found":true}.
type HTTPQuery struct {
	client  transport.Client
	baseURL map[ports.ArtifactKind]string
}

// HTTPQuery реализует ports.FulfillmentQuery.
var _ ports.FulfillmentQuery = (*HTTPQuery)(nil)

// NewHTTPQuery собирает адаптер. Клиент синхронного вызова обязателен; для вида
// артефакта без базового адреса чтение отвечает отказом.
func NewHTTPQuery(client transport.Client, baseURL map[ports.ArtifactKind]string) *HTTPQuery {
	return &HTTPQuery{client: client, baseURL: baseURL}
}

// statusResponse — форма ответа владельца.
type statusResponse struct {
	Status string `json:"status"`
	Found  bool   `json:"found"`
}

// Status выполняет синхронный запрос статуса артефакта.
func (q *HTTPQuery) Status(ctx context.Context, ref ports.ArtifactRef) (ports.ArtifactStatus, error) {
	base, ok := q.baseURL[ref.Kind]
	if !ok || base == "" {
		return ports.ArtifactStatus{}, fmt.Errorf("%w: адрес владельца %s не задан", ports.ErrArtifactUnavailable, ref.Kind)
	}
	response, err := q.client.Do(ctx, transport.Request{
		Method: http.MethodGet,
		URL:    fmt.Sprintf("%s/%s/%s", base, url.PathEscape(string(ref.Kind)), url.PathEscape(ref.ID)),
	})
	if err != nil {
		return ports.ArtifactStatus{}, fmt.Errorf("%w: %s:%s: %v", ports.ErrArtifactUnavailable, ref.Kind, ref.ID, err)
	}
	if response.StatusCode == http.StatusNotFound {
		return ports.ArtifactStatus{Ref: ref, Found: false}, nil
	}
	if response.StatusCode != http.StatusOK {
		return ports.ArtifactStatus{}, fmt.Errorf("%w: %s:%s: статус %d", ports.ErrArtifactUnavailable, ref.Kind, ref.ID, response.StatusCode)
	}
	var decoded statusResponse
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		return ports.ArtifactStatus{}, fmt.Errorf("%w: разбор ответа %s:%s: %v", ports.ErrArtifactUnavailable, ref.Kind, ref.ID, err)
	}
	return ports.ArtifactStatus{Ref: ref, Status: decoded.Status, Found: decoded.Found}, nil
}
