package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	persistence "lead-gen/backend/contexts/analytics/internal/adapters/persistence/inmemory"
	composition "lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

func TestReadinessRoleProfilesReportUnknownWithoutSafeStoreProbe(t *testing.T) {
	svc := &Service{
		slice:     &Slice{ProjectionStore: persistence.New(), ExportStore: persistence.NewReportExportStore()},
		readiness: readiness.New(readiness.Config{}),
	}
	api := svc.Readiness(context.Background(), composition.RoleAPI)
	if !api.Ready || api.Status != readiness.StatusDegraded || len(api.Checks) != 3 {
		t.Fatalf("api profile = %#v", api)
	}
	if api.Checks[1].Required || api.Checks[1].Status != readiness.StatusUnknown || api.Checks[2].Required || api.Checks[2].Status != readiness.StatusUnknown {
		t.Fatalf("optional analytics capabilities must remain visible and not block API core: %#v", api)
	}
	worker := svc.Readiness(context.Background(), composition.RoleWorker)
	if worker.Ready || worker.Status != readiness.StatusUnknown || len(worker.Checks) != 5 || worker.Checks[1].ID != "report_exports" || !worker.Checks[1].Required {
		t.Fatalf("worker profile = %#v", worker)
	}
	encoded, err := json.Marshal(api)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "password") || strings.Contains(string(encoded), "postgres://") {
		t.Fatalf("readiness disclosed details: %s", encoded)
	}
}

func TestWorkerSignalsFollowLoopLifecycleAndNilIsUnknown(t *testing.T) {
	signal := &workerSignal{}
	evaluator := readiness.New(readiness.Config{TTL: time.Minute})
	evaluate := func() readiness.Snapshot {
		return evaluator.Evaluate(context.Background(), readiness.Profile{Capability: "worker", Checks: []readiness.CheckSpec{signal.check("subscriber", true)}})
	}
	if got := evaluate(); got.Status != readiness.StatusUnknown || got.Checks[0].Reason != readiness.ReasonCheckMissing {
		t.Fatalf("not-started loop = %#v", got)
	}
	signal.begin()
	if got := evaluate(); got.Status != readiness.StatusUnknown {
		t.Fatalf("starting loop = %#v", got)
	}
	signal.finish(nil)
	started := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		done <- runTracked(ctx, signal, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return nil
		})
	}()
	<-started
	if got := evaluate(); got.Status != readiness.StatusUp {
		t.Fatalf("running loop = %#v", got)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := evaluate(); got.Status != readiness.StatusDown {
		t.Fatalf("stopped loop = %#v", got)
	}
	if err := runTracked(context.Background(), signal, func(context.Context) error { return errors.New("failure") }); err == nil {
		t.Fatal("expected loop error")
	}
	if got := evaluate(); got.Status != readiness.StatusDown {
		t.Fatalf("failed loop remained ready: %#v", got)
	} else if got.Checks[0].Reason != readiness.ReasonProbeFailed {
		t.Fatalf("loop failure reason = %s", got.Checks[0].Reason)
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				signal.begin()
				signal.activate()
				_ = evaluate()
				signal.finish(nil)
			}
		}()
	}
	wg.Wait()
}

func TestStoreProbeSanitizesDependencyFailure(t *testing.T) {
	evaluator := readiness.New(readiness.Config{})
	snapshot := evaluator.Evaluate(context.Background(), readiness.Profile{Capability: "store", Checks: []readiness.CheckSpec{{
		ID: "database", Required: true, Probe: storeProbe(failingReadinessStore{}),
	}}})
	if snapshot.Status != readiness.StatusDown || snapshot.Checks[0].Reason != readiness.ReasonProbeFailed {
		t.Fatalf("failure profile = %#v", snapshot)
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "secret") {
		t.Fatalf("raw error leaked: %s", encoded)
	}
}

type failingReadinessStore struct{}

func (failingReadinessStore) Readiness(context.Context) error { return errors.New("secret=credential") }
