package engagementobservation

import (
	"context"
	"testing"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
)

type consumerReadinessFake struct{ consumed int }

func (c *consumerReadinessFake) Consume(context.Context, string, amqp.Handler) error {
	c.consumed++
	return nil
}

func TestWorkerReadinessDoesNotConsumeMessages(t *testing.T) {
	consumer := &consumerReadinessFake{}
	report := newReadinessReporter(Persistence{Ping: func(context.Context) error { return nil }}, consumer, nil, nil)
	worker := report.RoleReadiness(context.Background(), composition.RoleWorker)
	if worker.Ready || worker.Status != readiness.StatusUnknown {
		t.Fatalf("consumer без пассивного health-контракта должен быть unknown: %+v", worker)
	}
	if consumer.consumed != 0 {
		t.Fatalf("readiness вызвала Consume %d раз", consumer.consumed)
	}
	ingest := report.RoleReadiness(context.Background(), composition.RoleIngest)
	if !ingest.Ready {
		t.Fatalf("ingest при доступном хранилище/outbox не готов: %+v", ingest)
	}
}

func TestInMemoryIngestReadinessIsUnknown(t *testing.T) {
	report := newReadinessReporter(Persistence{}, nil, nil, nil)
	ingest := report.RoleReadiness(context.Background(), composition.RoleIngest)
	if ingest.Ready || ingest.Status != readiness.StatusUnknown {
		t.Fatalf("in-memory persistence не должна быть готовой: %+v", ingest)
	}
}
