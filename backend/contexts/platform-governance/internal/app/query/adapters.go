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
// приводится к AdapterStateUnknown (консоль не подставляет «в норме»).
// Неизвестная причина сводится к пустой: свободный текст владельца (в том числе
// с секретом) наружу не проходит. Ошибка порта и отсутствие владельца дают
// app.ErrUnavailable.
func (h *AdapterCatalogHandler) Handle(ctx context.Context) ([]ports.AdapterStatus, error) {
	if h.catalog == nil {
		return nil, app.ErrUnavailable
	}
	states, err := h.catalog.States(ctx)
	if err != nil {
		// Текст ошибки владельца наружу не переносится: он может нести
		// технические детали (адреса, токены). Наружу — только честный исход
		// «недоступно».
		return nil, app.ErrUnavailable
	}
	normalized := make([]ports.AdapterStatus, 0, len(states))
	for _, state := range states {
		state.State = normalizeState(state.State)
		state.Reason = normalizeReason(state.Reason)
		normalized = append(normalized, state)
	}
	return normalized, nil
}

// normalizeState сводит состояние к объявленному набору; неизвестное значение
// (включая пустое у неподключённого адаптера) становится AdapterStateUnknown.
func normalizeState(state string) string {
	switch state {
	case ports.AdapterStateUp, ports.AdapterStateDegraded, ports.AdapterStateDown:
		return state
	default:
		return ports.AdapterStateUnknown
	}
}

// normalizeReason сводит причину к объявленному набору; неизвестное значение
// (в том числе произвольный текст с секретом) становится пустым.
func normalizeReason(reason string) string {
	switch reason {
	case ports.AdapterReasonUnreachable, ports.AdapterReasonAuthFailed,
		ports.AdapterReasonRateLimited, ports.AdapterReasonDegraded,
		ports.AdapterReasonMisconfigured:
		return reason
	default:
		return ports.AdapterReasonNone
	}
}
