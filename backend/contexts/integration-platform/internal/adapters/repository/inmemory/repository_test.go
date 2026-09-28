package inmemory

import (
	"testing"

	"lead-gen/backend/contexts/integration-platform/internal/ports"
	"lead-gen/backend/contexts/integration-platform/internal/ports/contracttest"
)

// Общий contract suite репозиториев проходит против репозитория регистраций.
func TestAdapterRegistrationRepositoryContract(t *testing.T) {
	contracttest.RunAdapterRegistrationRepositoryContract(t, func(t *testing.T) ports.AdapterRegistrationRepository {
		t.Helper()
		return NewAdapterRegistrationRepository()
	})
}

// Общий contract suite репозиториев проходит против репозитория вызовов.
func TestIntegrationInvocationRepositoryContract(t *testing.T) {
	contracttest.RunIntegrationInvocationRepositoryContract(t, func(t *testing.T) ports.IntegrationInvocationRepository {
		t.Helper()
		return NewIntegrationInvocationRepository()
	})
}
