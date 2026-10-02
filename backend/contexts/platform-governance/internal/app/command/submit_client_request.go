package command

import (
	"context"
	"fmt"
	"strings"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/app/projection"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
	"lead-gen/contracts/request-fulfillment/requestintent"
)

// defaultActor — актор подачи заявки из портала, если он не передан (dev-контур
// без аутентификации). Настоящие роли пользователей — модель platform-access.
const defaultActor = "client"

// SubmitClientRequest — команда подачи заявки клиентом: что предлагает и кому.
type SubmitClientRequest struct {
	TenantID string
	Content  string
	Target   string
	Actor    string
}

// SubmitClientRequestResult — результат подачи: идентификатор заявки и её статус.
type SubmitClientRequestResult struct {
	RequestID string
	Status    string
}

// SubmitClientRequestHandler — use case подачи заявки.
//
// Портал не пишет в хранилище заявок напрямую: команду создаёт владелец через
// опубликованный контракт request-intent (design.md, Решение 3). После успеха
// портал запоминает идентификатор заявки тенанта в своей проекции.
type SubmitClientRequestHandler struct {
	requests   requestintent.Port
	ids        ports.IDGenerator
	projection *projection.LastRequest
}

// NewSubmitClientRequestHandler собирает use case из контракта владельца заявки,
// генератора идентификаторов и проекции портала.
func NewSubmitClientRequestHandler(requests requestintent.Port, ids ports.IDGenerator, proj *projection.LastRequest) *SubmitClientRequestHandler {
	return &SubmitClientRequestHandler{requests: requests, ids: ids, projection: proj}
}

// Handle вызывает команду владельца заявки и запоминает результат. Заявка
// создаётся в статусе submitted и привязана к тенанту клиента.
func (h *SubmitClientRequestHandler) Handle(ctx context.Context, cmd SubmitClientRequest) (SubmitClientRequestResult, error) {
	tenant := strings.TrimSpace(cmd.TenantID)
	if tenant == "" {
		return SubmitClientRequestResult{}, fmt.Errorf("%w: %v", app.ErrRejected, domain.ErrEmptyID)
	}
	result, err := h.requests.Submit(ctx, requestintent.SubmitIntent{
		RequestID: h.ids.NewRequestID(),
		OfferID:   h.ids.NewOfferID(),
		TenantID:  tenant,
		Content:   cmd.Content,
		Target:    cmd.Target,
		Actor:     actorOrDefault(cmd.Actor),
	})
	if err != nil {
		return SubmitClientRequestResult{}, app.TranslateRequestIntent(err)
	}
	h.projection.Remember(domain.TenantID(tenant), result.RequestID)
	return SubmitClientRequestResult{RequestID: result.RequestID, Status: result.Status}, nil
}

// actorOrDefault подставляет актора по умолчанию, если он не задан.
func actorOrDefault(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return defaultActor
	}
	return actor
}
