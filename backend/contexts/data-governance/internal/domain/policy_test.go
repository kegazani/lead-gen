package domain_test

import (
	"errors"
	"testing"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// TestDataZonePolicy проверяет политику data-zone-policy: red не выносится в
// общее хранилище, green допустим, категория задаёт режим обращения.
func TestDataZonePolicy(t *testing.T) {
	t.Parallel()

	cases := []struct {
		category      domain.DataCategory
		handling      domain.Handling
		sharedStorage bool
		minimize      bool
	}{
		{domain.CategoryGreen, domain.HandlingPlatformStorage, true, false},
		{domain.CategoryMixed, domain.HandlingSplitByAttribute, true, true},
		{domain.CategoryRed, domain.HandlingClientSide, false, true},
		{domain.CategoryAnalytics, domain.HandlingAggregateOnly, true, true},
		{domain.CategoryExternal, domain.HandlingReferenceOnly, true, true},
	}
	for _, tc := range cases {
		rule, err := domain.DataZonePolicy(mustPurpose(t, "outreach", tc.category))
		if err != nil {
			t.Fatalf("категория %q: %v", tc.category, err)
		}
		if rule.Handling != tc.handling || rule.SharedStorageAllowed != tc.sharedStorage || rule.Minimize != tc.minimize {
			t.Fatalf("категория %q: правило = %+v", tc.category, rule)
		}
		if rule.Reason == "" {
			t.Fatalf("категория %q: пустое основание правила", tc.category)
		}
	}
}

// TestSuppressionPolicy проверяет инвариант I2: активная запись стоп-листа имеет
// приоритет над кампанией, решением оператора и поставщиком.
func TestSuppressionPolicy(t *testing.T) {
	t.Parallel()

	if decision := domain.SuppressionPolicy(false, domain.ActionCampaign); decision.Blocked {
		t.Fatalf("без стоп-листа действие заблокировано: %+v", decision)
	}
	for _, action := range []domain.SuppressionAction{domain.ActionCampaign, domain.ActionOperatorDecision, domain.ActionProvider} {
		decision := domain.SuppressionPolicy(true, action)
		if !decision.Blocked {
			t.Fatalf("стоп-лист не заблокировал %q", action)
		}
		if decision.Reason == "" {
			t.Fatalf("действие %q: пустое основание", action)
		}
	}
}

// TestClientSideOnly проверяет, что red-данные помечены как клиентские.
func TestClientSideOnly(t *testing.T) {
	t.Parallel()

	if !domain.CategoryRed.ClientSideOnly() {
		t.Fatal("red не помечен клиентским")
	}
	if domain.CategoryGreen.ClientSideOnly() {
		t.Fatal("green помечен клиентским")
	}
	if _, err := domain.DataZonePolicy(domain.ProcessingPurpose{}); !errors.Is(err, domain.ErrEmptyPurpose) {
		t.Fatalf("нулевая цель: ошибка = %v, ожидалась ErrEmptyPurpose", err)
	}
}
