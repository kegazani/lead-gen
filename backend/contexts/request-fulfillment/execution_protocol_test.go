package requestfulfillment_test

import (
	"errors"
	"testing"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
)

type activeExecutionConsumer bool

func (c activeExecutionConsumer) Active() bool { return bool(c) }

func TestExecutionProtocolDefaultsToV1(t *testing.T) {
	if _, err := requestfulfillment.Build(requestfulfillment.Config{}); err != nil {
		t.Fatalf("default protocol должен собираться как v1: %v", err)
	}
}

func TestExecutionProtocolV2RequiresActiveConsumer(t *testing.T) {
	for name, consumer := range map[string]requestfulfillment.ExecutionIntentConsumer{
		"missing":  nil,
		"inactive": activeExecutionConsumer(false),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := requestfulfillment.Build(requestfulfillment.Config{
				ExecutionProtocol:       requestfulfillment.ExecutionProtocolV2,
				ExecutionConsumer:       consumer,
				ExecutionManifestActive: true,
			})
			if !errors.Is(err, requestfulfillment.ErrExecutionConsumerUnavailable) {
				t.Fatalf("Build error = %v, ожидался отказ из-за consumer", err)
			}
		})
	}
}

func TestExecutionProtocolV2AcceptsExplicitActiveConsumer(t *testing.T) {
	_, err := requestfulfillment.Build(requestfulfillment.Config{
		ExecutionProtocol:       requestfulfillment.ExecutionProtocolV2,
		ExecutionConsumer:       activeExecutionConsumer(true),
		ExecutionManifestActive: true,
	})
	if err != nil {
		t.Fatalf("Build v2 c active consumer: %v", err)
	}
}

func TestExecutionProtocolV2RequiresActiveManifest(t *testing.T) {
	_, err := requestfulfillment.Build(requestfulfillment.Config{
		ExecutionProtocol: requestfulfillment.ExecutionProtocolV2,
		ExecutionConsumer: activeExecutionConsumer(true),
	})
	if !errors.Is(err, requestfulfillment.ErrExecutionManifestRequired) {
		t.Fatalf("Build error=%v, ожидался отказ без active manifest", err)
	}
}

func TestExecutionProtocolRejectsUnknownValue(t *testing.T) {
	if _, err := requestfulfillment.Build(requestfulfillment.Config{ExecutionProtocol: "v3"}); err == nil {
		t.Fatal("неизвестный ExecutionProtocol принят")
	}
}
