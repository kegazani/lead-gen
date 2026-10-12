package datagovernance

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/readiness"
)

func TestReadinessRequiresBasisAndAuthAndSanitizesFailures(t *testing.T) {
	svc := &Service{
		slice:     &Slice{Repository: readinessBasisRepo{err: errors.New("password=secret postgres://private")}},
		readiness: readiness.New(readiness.Config{}),
	}
	api := svc.Readiness(context.Background(), composition.RoleAPI)
	if api.Ready || api.Status != readiness.StatusDown || len(api.Checks) != 2 {
		t.Fatalf("api profile = %#v", api)
	}
	if api.Checks[0].Reason != readiness.ReasonProbeFailed || api.Checks[1].Reason != readiness.ReasonProbeNotConfigured {
		t.Fatalf("expected failed repository and unknown auth: %#v", api.Checks)
	}
	worker := svc.Readiness(context.Background(), composition.RoleWorker)
	if worker.Ready || worker.Checks[0].Status != readiness.StatusDown {
		t.Fatalf("worker profile = %#v", worker)
	}
	encoded, err := json.Marshal(api)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "postgres://") {
		t.Fatalf("readiness disclosed repository error: %s", encoded)
	}
}

type readinessBasisRepo struct{ err error }

func (r readinessBasisRepo) Readiness(context.Context) error                        { return r.err }
func (readinessBasisRepo) Save(context.Context, *domain.ProcessingBasis, int) error { return nil }
func (readinessBasisRepo) FindByID(context.Context, domain.BasisID) (*domain.ProcessingBasis, error) {
	return nil, nil
}
func (readinessBasisRepo) FindByTenant(context.Context, domain.TenantID) ([]*domain.ProcessingBasis, error) {
	return nil, nil
}
func (readinessBasisRepo) ListExpired(context.Context, time.Time) ([]*domain.ProcessingBasis, error) {
	return nil, nil
}
