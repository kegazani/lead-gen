package ports

// ExecutionProtocol выбирает поведение request-fulfillment при исполнении.
type ExecutionProtocol string

// ExecutionIntentConsumer сообщает о наличии активного consumer accepted intent.
type ExecutionIntentConsumer interface {
	Active() bool
}

const (
	ExecutionProtocolV1 ExecutionProtocol = "v1"
	ExecutionProtocolV2 ExecutionProtocol = "v2"
)
