package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	httptransport "lead-gen/backend/platform/transport/http"
)

// approvedActionResource — ресурс владельца, принимающего approved-action.
// command идёт POST-ом на ресурс владельца, повтор распознаётся по ключу
// идемпотентности (design.md, Решение 2).
const approvedActionResource = "/approved-actions"

// ApprovedActionPublisher публикует контракт approved-action владельцем по связи
// outreach-to-delivery (интеграция command, транспорт sync/http-json).
//
// Публикация — единственное, что делает адаптер: он сериализует DTO, переносит
// конверт и ключ идемпотентности и отправляет POST через платформенный клиент.
// Доменных решений здесь нет: допустимость действия уже обеспечена агрегатом, а
// идемпотентность получателя — контрактом (ключ tenant|campaign|draft|version).
//
// Publisher структурно реализует relay.Sink, поэтому подключается после
// transactional outbox: relay вызывает Publish только после commit, а при сбое
// доставки повторит вызов с тем же ключом идемпотентности.
type ApprovedActionPublisher struct {
	client  httptransport.Client
	baseURL string
	timeout time.Duration
}

// NewApprovedActionPublisher собирает издателя. timeout <= 0 заменяется значением
// по умолчанию.
func NewApprovedActionPublisher(client httptransport.Client, baseURL string, timeout time.Duration) *ApprovedActionPublisher {
	return &ApprovedActionPublisher{
		client:  client,
		baseURL: baseURL,
		timeout: resolvedTimeout(timeout),
	}
}

// Publish отправляет approved-action владельцу. Невалидный контракт не
// доставляется: ошибка видна отправителю, а не прячется в чужой системе.
// Неуспешный ответ владельца (вне 2xx) — ошибка: повтор разрешён транспортом,
// потому что POST несёт стабильный ключ идемпотентности.
func (p *ApprovedActionPublisher) Publish(ctx context.Context, action contracts.ApprovedAction) error {
	if p == nil || p.client == nil {
		return fmt.Errorf("transport: публикация approved-action без клиента")
	}
	if err := contracts.ValidateApprovedAction(action); err != nil {
		return err
	}

	body, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("transport: сериализация approved-action: %w", err)
	}

	response, err := p.client.Do(ctx, httptransport.Request{
		Method:         http.MethodPost,
		URL:            joinURL(p.baseURL, approvedActionResource),
		Body:           body,
		Envelope:       toPlatformEnvelope(action.Envelope),
		IdempotencyKey: action.IdempotencyKey(),
		Timeout:        p.timeout,
	})
	if err != nil {
		return fmt.Errorf("transport: публикация approved-action: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("transport: approved-action отвергнут владельцем: статус %d, тело %s", response.StatusCode, response.Body)
	}
	return nil
}
