package call

import (
	"context"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// Unavailable — провайдер звонков не подключён: порт честно отвечает отказом, а
// не выдумывает идентификатор звонка. Так выглядит сборка, пока контракт
// call-intent не подключён (реализация call-orchestration — отдельный change).
type Unavailable struct{}

// Unavailable реализует порт инициирования звонка.
var _ ports.CallInitiator = Unavailable{}

// InitiateCall реализует ports.CallInitiator.
func (Unavailable) InitiateCall(context.Context, ports.InitiateCall) (ports.CallResult, error) {
	return ports.CallResult{}, ports.ErrCallUnavailable
}
