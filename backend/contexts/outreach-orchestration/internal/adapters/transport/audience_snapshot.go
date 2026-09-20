package transport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	httptransport "lead-gen/backend/platform/transport/http"
)

// audienceSnapshotResource — ресурс владельца (контекста audience), отдающий
// снимок аудитории. query идёт GET-ом с детерминированным ключом кэша
// (design.md, Решение 2).
const audienceSnapshotResource = "/audience-snapshots/"

// AudienceSnapshotClient запрашивает контракт audience-snapshot у владельца по
// связи audience-to-outreach (интеграция query, транспорт sync/http-json).
//
// Адаптер — сторона ПОТРЕБИТЕЛЯ: он разбирает ответ в собственный
// антикоррупционный DTO contracts.AudienceSnapshot и проверяет его. Типы
// владельца (audience) сюда не импортируются, поэтому изменение модели владельца
// не протекает дальше границы.
type AudienceSnapshotClient struct {
	client  httptransport.Client
	baseURL string
	timeout time.Duration
}

// NewAudienceSnapshotClient собирает клиента. timeout <= 0 заменяется значением по
// умолчанию.
func NewAudienceSnapshotClient(client httptransport.Client, baseURL string, timeout time.Duration) *AudienceSnapshotClient {
	return &AudienceSnapshotClient{
		client:  client,
		baseURL: baseURL,
		timeout: resolvedTimeout(timeout),
	}
}

// Fetch запрашивает снимок аудитории по идентификатору. Конверт вызывающего
// задаёт сквозную корреляцию и тенант; имя и версия контракта проставляются
// адаптером, чтобы запрос нельзя было отправить под чужим контрактом.
//
// Ответ разбирается в DTO потребителя и проверяется: невалидный ответ владельца —
// ошибка, а не молча принятый снимок.
func (c *AudienceSnapshotClient) Fetch(ctx context.Context, env contracts.Envelope, snapshotID string) (contracts.AudienceSnapshot, error) {
	if c == nil || c.client == nil {
		return contracts.AudienceSnapshot{}, fmt.Errorf("transport: запрос audience-snapshot без клиента")
	}
	env.Name = contracts.ContractAudienceSnapshot
	env.SchemaVersion = contracts.AudienceSnapshotSchemaVersion

	response, err := c.client.Do(ctx, httptransport.Request{
		Method:   http.MethodGet,
		URL:      joinURL(c.baseURL, audienceSnapshotResource+url.PathEscape(snapshotID)),
		Envelope: toPlatformEnvelope(env),
		Timeout:  c.timeout,
	})
	if err != nil {
		return contracts.AudienceSnapshot{}, fmt.Errorf("transport: запрос audience-snapshot %q: %w", snapshotID, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return contracts.AudienceSnapshot{}, fmt.Errorf("transport: audience-snapshot %q отвергнут владельцем: статус %d, тело %s", snapshotID, response.StatusCode, response.Body)
	}

	var snapshot contracts.AudienceSnapshot
	if err := json.Unmarshal(response.Body, &snapshot); err != nil {
		return contracts.AudienceSnapshot{}, fmt.Errorf("transport: разбор audience-snapshot %q: %w", snapshotID, err)
	}
	if err := snapshot.Validate(); err != nil {
		return contracts.AudienceSnapshot{}, err
	}
	return snapshot, nil
}
