package datagovernance_test

import (
	"context"
	"errors"
	"testing"
	"time"

	datagovernance "lead-gen/backend/contexts/data-governance"
	"lead-gen/backend/contexts/data-governance/internal/app/command"
	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/contracts/data-governance/datazonepolicy"
	"lead-gen/contracts/data-governance/processingauthority"
	"lead-gen/contracts/data-governance/suppressionpolicy"
)

var (
	ctx      = context.Background()
	baseTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
)

// newSlice собирает срез и наполняет его одним основанием с записью стоп-листа.
func newSlice(t *testing.T) *datagovernance.Slice {
	t.Helper()
	slice, err := datagovernance.Build(datagovernance.Config{})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	if _, err := slice.CreateBasis.Handle(ctx, command.CreateBasis{
		BasisID: "basis-1", TenantID: "tenant-1", Purpose: "outreach", Category: domain.CategoryRed,
		RetentionDays: 90, ValidFrom: baseTime, ValidUntil: baseTime.AddDate(0, 0, 90), Actor: "actor-1",
	}); err != nil {
		t.Fatalf("создание основания: %v", err)
	}
	if _, err := slice.SuppressContact.Handle(ctx, command.SuppressContact{
		BasisID: "basis-1", TenantID: "tenant-1", SuppressionID: "sup-1",
		Contact: "a@example.com", Reason: "отзыв согласия", Actor: "actor-1",
	}); err != nil {
		t.Fatalf("стоп-лист: %v", err)
	}
	return slice
}

// TestZonePolicyContract проверяет контракт data-zone-policy.
func TestZonePolicyContract(t *testing.T) {
	t.Parallel()

	port := datagovernance.NewZonePolicy(newSlice(t))
	decision, err := port.Decide(ctx, datazonepolicy.ZoneQuery{TenantID: "tenant-1", Purpose: "outreach", Category: "red", At: baseTime})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if !decision.Authorized || decision.Handling != string(domain.HandlingClientSide) || decision.SharedStorageAllowed {
		t.Fatalf("решение = %+v", decision)
	}
}

// TestProcessingAuthorityContract проверяет контракт processing-authority.
func TestProcessingAuthorityContract(t *testing.T) {
	t.Parallel()

	port := datagovernance.NewProcessingAuthority(newSlice(t))
	authority, err := port.Check(ctx, processingauthority.AuthorityQuery{
		TenantID: "tenant-1", Purpose: "outreach", Category: "red", At: baseTime,
	})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !authority.Authorized || authority.BasisID != "basis-1" || authority.RetentionDays != 90 {
		t.Fatalf("полномочие = %+v", authority)
	}

	if _, err := port.Check(ctx, processingauthority.AuthorityQuery{
		TenantID: "tenant-1", Purpose: "outreach", Category: "green", At: baseTime,
	}); !errors.Is(err, processingauthority.ErrNotFound) {
		t.Fatalf("чужая категория: ошибка = %v, ожидалась ErrNotFound", err)
	}
}

// TestSuppressionPolicyContract проверяет контракт suppression-policy.
func TestSuppressionPolicyContract(t *testing.T) {
	t.Parallel()

	port := datagovernance.NewSuppressionPolicy(newSlice(t))
	decision, err := port.Check(ctx, suppressionpolicy.SuppressionQuery{TenantID: "tenant-1", Contact: "a@example.com"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !decision.Suppressed || decision.SuppressionID != "sup-1" || decision.BasisID != "basis-1" {
		t.Fatalf("решение = %+v", decision)
	}

	free, err := port.Check(ctx, suppressionpolicy.SuppressionQuery{TenantID: "tenant-1", Contact: "b@example.com"})
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if free.Suppressed {
		t.Fatalf("незапятнанный контакт подавлен: %+v", free)
	}
}

// TestNilSliceContractsUnavailable проверяет, что без среза контракты честно
// сообщают о недоступности владельца, а не выдумывают решение.
func TestNilSliceContractsUnavailable(t *testing.T) {
	t.Parallel()

	if _, err := datagovernance.NewZonePolicy(nil).Decide(ctx, datazonepolicy.ZoneQuery{}); !errors.Is(err, datazonepolicy.ErrUnavailable) {
		t.Fatalf("data-zone-policy: ошибка = %v", err)
	}
	if _, err := datagovernance.NewProcessingAuthority(nil).Check(ctx, processingauthority.AuthorityQuery{}); !errors.Is(err, processingauthority.ErrUnavailable) {
		t.Fatalf("processing-authority: ошибка = %v", err)
	}
	if _, err := datagovernance.NewSuppressionPolicy(nil).Check(ctx, suppressionpolicy.SuppressionQuery{}); !errors.Is(err, suppressionpolicy.ErrUnavailable) {
		t.Fatalf("suppression-policy: ошибка = %v", err)
	}
}
