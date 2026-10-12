package outreachorchestration

import (
	"context"
	"testing"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
	"lead-gen/backend/platform/transport/amqp"
)

type readinessPublisher struct{ publishes int }

func (p *readinessPublisher) Publish(context.Context, string, string, []byte, map[string]string) error {
	p.publishes++
	return nil
}

func TestRoleReadinessDoesNotExecuteBusinessOperations(t *testing.T) {
	publisher := &readinessPublisher{}
	report := NewReadinessReport(Persistence{Ping: func(context.Context) error { return nil }}, publisher)
	api := report.RoleReadiness(context.Background(), composition.RoleAPI)
	if api.Ready || api.Status != readiness.StatusUnknown {
		t.Fatalf("API без пассивной проверки доступа должна остаться unknown: %+v", api)
	}
	worker := report.RoleReadiness(context.Background(), composition.RoleWorker)
	if worker.Ready || worker.Status != readiness.StatusUnknown {
		t.Fatalf("publisher без пассивного health-контракта должен быть unknown: %+v", worker)
	}
	if publisher.publishes != 0 {
		t.Fatalf("readiness вызвала Publish %d раз", publisher.publishes)
	}
	schedule := report.RoleReadiness(context.Background(), composition.RoleSchedule)
	if schedule.Ready || schedule.Status != readiness.StatusUnknown {
		t.Fatalf("нереализованная schedule не должна быть готова: %+v", schedule)
	}
	var _ amqp.Publisher = publisher
}
