package platformgovernance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

func TestReadinessRoleProfilesAndNoConfiguredPublisherSuccess(t *testing.T) {
	svc := &Service{readiness: readiness.New(readiness.Config{}), publisher: readinessTestPublisher{}}
	api := svc.Readiness(context.Background(), composition.RoleAPI)
	if api.Ready || api.Status != readiness.StatusUnknown || len(api.Checks) != 3 {
		t.Fatalf("api profile = %#v", api)
	}
	worker := svc.Readiness(context.Background(), composition.RoleWorker)
	if worker.Ready || worker.Status != readiness.StatusUnknown || len(worker.Checks) != 3 {
		t.Fatalf("worker profile = %#v", worker)
	}
	if worker.Checks[1].Status != readiness.StatusUnknown || worker.Checks[1].Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("configured publisher was treated as up: %#v", worker.Checks[1])
	}
	encoded, err := json.Marshal(worker)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "amqp://") {
		t.Fatalf("readiness disclosed dependency details: %s", encoded)
	}
}

func TestRelayReadinessTracksActualWorkerLifecycle(t *testing.T) {
	svc := &Service{readiness: readiness.New(readiness.Config{TTL: time.Minute}), relayLoop: &workerSignal{}}
	check := func() readiness.Check {
		snapshot := svc.readiness.Evaluate(context.Background(), readiness.Profile{Capability: "worker", Checks: []readiness.CheckSpec{svc.relayLoop.check()}})
		return snapshot.Checks[0]
	}
	if got := check(); got.Status != readiness.StatusUnknown {
		t.Fatalf("nil relay loop = %#v", got)
	}
	svc.relayLoop.begin()
	if got := check(); got.Status != readiness.StatusUnknown {
		t.Fatalf("starting relay = %#v", got)
	}
	svc.relayLoop.finish(nil)
	started := make(chan struct{})
	svc.relay = blockingRelay{started: started}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { done <- svc.runWorker(ctx) }()
	<-started
	if got := check(); got.Status != readiness.StatusUp {
		t.Fatalf("running relay = %#v", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := check(); got.Status != readiness.StatusDown {
		t.Fatalf("stopped relay = %#v", got)
	}
	svc.relay = failingRelay{}
	if err := svc.runWorker(context.Background()); err == nil {
		t.Fatal("expected relay error")
	}
	if got := check(); got.Status != readiness.StatusDown {
		t.Fatalf("failed relay remained ready: %#v", got)
	} else if got.Reason != readiness.ReasonProbeFailed {
		t.Fatalf("relay failure reason = %s", got.Reason)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				svc.relayLoop.begin()
				svc.relayLoop.activate()
				_ = check()
				svc.relayLoop.finish(nil)
			}
		}()
	}
	wg.Wait()
}

type blockingRelay struct {
	started chan struct{}
}

func (r blockingRelay) Run(ctx context.Context) error {
	close(r.started)
	<-ctx.Done()
	return nil
}

type failingRelay struct{}

func (failingRelay) Run(context.Context) error { return errors.New("relay failure") }

type readinessTestPublisher struct{}

func (readinessTestPublisher) Publish(context.Context, string, string, []byte, map[string]string) error {
	return nil
}
