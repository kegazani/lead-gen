package query

import (
	"context"

	"lead-gen/backend/contexts/platform-governance/internal/app"
	"lead-gen/backend/contexts/platform-governance/internal/ports"
)

// AdapterCatalogHandler — use case чтения состояния адаптеров интеграций для
// раздела «Интеграции» консоли.
//
// Владельцем адаптеров остаётся integration-platform: консоль читает состояние
// через порт и не заводит собственного реестра адаптеров. Если владелец не
// подключён, раздел честно отвечает «недоступно», а не показывает пустой список
// как успех.
type AdapterCatalogHandler struct {
	catalog ports.AdapterCatalog
}

// NewAdapterCatalogHandler собирает use case из порта чтения адаптеров.
func NewAdapterCatalogHandler(catalog ports.AdapterCatalog) *AdapterCatalogHandler {
	return &AdapterCatalogHandler{catalog: catalog}
}

// Handle возвращает нормализованное состояние адаптеров. Неизвестное состояние
// приводится к AdapterUnknown (консоль не подставляет «в норме»). Ошибка порта и
// отсутствие владельца дают app.ErrUnavailable.
func (h *AdapterCatalogHandler) Handle(ctx context.Context) ([]ports.AdapterState, error) {
	if h.catalog == nil {
		return nil, app.ErrUnavailable
	}
	states, err := h.catalog.States(ctx)
	if err != nil {
		return nil, app.ErrUnavailable
	}
	normalized := make([]ports.AdapterState, 0, len(states))
	for _, state := range states {
		state.Health = normalizeHealth(state.Health)
		state.Reason = normalizeReason(state.Reason)
		normalized = append(normalized, state)
	}
	return normalized, nil
}

// normalizeHealth сводит состояние к объявленному набору; неизвестное значение
// становится AdapterUnknown.
func normalizeHealth(health ports.AdapterHealth) ports.AdapterHealth {
	switch health {
	case ports.AdapterHealthy, ports.AdapterDegraded, ports.AdapterDown:
		return health
	default:
		return ports.AdapterUnknown
	}
}

// normalizeReason сводит причину к объявленному набору; неизвестное значение
// (в том числе произвольный текст с секретом) становится пустым.
func normalizeReason(reason ports.AdapterReason) ports.AdapterReason {
	switch reason {
	case ports.AdapterReasonUnreachable, ports.AdapterReasonAuthFailed,
		ports.AdapterReasonRateLimited, ports.AdapterReasonDegraded,
		ports.AdapterReasonMisconfigured:
		return reason
	default:
		return ports.AdapterReasonNone
	}
}
