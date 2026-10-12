package requestfulfillment

import (
	"errors"
	"fmt"

	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
)

// ExecutionProtocol выбирает совместимый legacy-путь либо durable admission.
type ExecutionProtocol = ports.ExecutionProtocol

const (
	ExecutionProtocolV1 = ports.ExecutionProtocolV1
	ExecutionProtocolV2 = ports.ExecutionProtocolV2
)

var ErrExecutionConsumerUnavailable = errors.New("request-fulfillment: для ExecutionProtocol v2 не зарегистрирован активный consumer")
var ErrExecutionManifestRequired = errors.New("request-fulfillment: ExecutionProtocol v2 требует активный подписанный manifest")

// ExecutionIntentConsumer — порт зарегистрированного потребителя intent.
type ExecutionIntentConsumer = ports.ExecutionIntentConsumer

func normalizeExecutionProtocol(protocol ExecutionProtocol) (ExecutionProtocol, error) {
	if protocol == "" {
		return ExecutionProtocolV1, nil
	}
	switch protocol {
	case ExecutionProtocolV1, ExecutionProtocolV2:
		return protocol, nil
	default:
		return "", fmt.Errorf("request-fulfillment: неизвестный ExecutionProtocol %q", protocol)
	}
}
