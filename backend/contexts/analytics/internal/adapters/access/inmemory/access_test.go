package inmemory_test

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/analytics/internal/adapters/access/inmemory"
	"lead-gen/backend/contexts/analytics/internal/ports"
)

// TestRoleSeparation проверяет разнесение по роли: полная CRM доступна только
// оператору (и администратору), а клиенту — только дашборд счётчиков.
func TestRoleSeparation(t *testing.T) {
	decider := inmemory.New()
	ctx := context.Background()

	cases := []struct {
		name     string
		role     string
		resource string
		want     bool
	}{
		{"клиент к CRM", "client", ports.ResourceCRM, false},
		{"оператор к CRM", "operator", ports.ResourceCRM, true},
		{"администратор к CRM", "admin", ports.ResourceCRM, true},
		{"клиент к дашборду", "client", ports.ResourceDashboard, true},
		{"неизвестная роль к дашборду", "guest", ports.ResourceDashboard, false},
		{"неизвестный ресурс", "client", "unknown", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			allowed, err := decider.Allow(ctx, tc.role, tc.resource)
			if err != nil {
				t.Fatalf("Allow: %v", err)
			}
			if allowed != tc.want {
				t.Fatalf("Allow(%q, %q) = %v, ожидалось %v", tc.role, tc.resource, allowed, tc.want)
			}
		})
	}
}
