package riskfacts

import (
	"context"
	"errors"
	"testing"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/contracts/data-governance/suppressionpolicy"
	"lead-gen/contracts/lead-management/addressverification"
	"lead-gen/contracts/lead-management/deliverytarget"
)

// resolverStub — двойник контракта delivery-target: отдаёт адрес или ошибку.
type resolverStub struct {
	address string
	err     error
}

func (s resolverStub) ResolveRecipient(context.Context, string, string) (deliverytarget.Recipient, error) {
	if s.err != nil {
		return deliverytarget.Recipient{}, s.err
	}
	return deliverytarget.Recipient{Address: s.address}, nil
}

// suppressionStub — двойник контракта suppression-policy.
type suppressionStub struct {
	suppressed bool
	err        error
}

func (s suppressionStub) Check(context.Context, suppressionpolicy.SuppressionQuery) (suppressionpolicy.SuppressionDecision, error) {
	if s.err != nil {
		return suppressionpolicy.SuppressionDecision{}, s.err
	}
	return suppressionpolicy.SuppressionDecision{Suppressed: s.suppressed}, nil
}

// verificationStub — двойник контракта address-verification.
type verificationStub struct {
	verified bool
	err      error
}

func (s verificationStub) Verification(context.Context, addressverification.Query) (addressverification.Snapshot, error) {
	if s.err != nil {
		return addressverification.Snapshot{}, s.err
	}
	return addressverification.Snapshot{Verified: s.verified, Status: addressverification.StatusDeliverable}, nil
}

func request() domain.RiskFactsRequest {
	return domain.RiskFactsRequest{
		TenantID:  domain.TenantID("tenant-1"),
		DraftID:   domain.DraftID("draft-1"),
		Version:   1,
		Recipient: domain.RecipientID("lead-1"),
	}
}

func TestCleanDraftHasNoReasons(t *testing.T) {
	collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{}, verificationStub{verified: true})
	reasons, err := collector.RiskReasons(context.Background(), request())
	if err != nil {
		t.Fatalf("RiskReasons: %v", err)
	}
	if len(reasons) != 0 {
		t.Fatalf("чистый черновик не должен иметь признаков: %v", reasons)
	}
}

func TestSuppressedContactTriggersStopRule(t *testing.T) {
	collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{suppressed: true}, verificationStub{verified: true})
	reasons, err := collector.RiskReasons(context.Background(), request())
	if err != nil {
		t.Fatalf("RiskReasons: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != domain.RiskStopRuleTriggered {
		t.Fatalf("признаки = %v, ожидался stop-rule-triggered", reasons)
	}
}

func TestUnverifiedAddressTriggersReason(t *testing.T) {
	collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{}, verificationStub{verified: false})
	reasons, err := collector.RiskReasons(context.Background(), request())
	if err != nil {
		t.Fatalf("RiskReasons: %v", err)
	}
	if len(reasons) != 1 || reasons[0] != domain.RiskAddressUnverified {
		t.Fatalf("признаки = %v, ожидался address-unverified", reasons)
	}
}

func TestBothSignalsAccumulate(t *testing.T) {
	collector := New(resolverStub{address: "lead@example.com"}, suppressionStub{suppressed: true}, verificationStub{verified: false})
	reasons, err := collector.RiskReasons(context.Background(), request())
	if err != nil {
		t.Fatalf("RiskReasons: %v", err)
	}
	if len(reasons) != 2 {
		t.Fatalf("признаки = %v, ожидались два", reasons)
	}
}

func TestUnavailableOwnerIsErrorNotEmpty(t *testing.T) {
	cases := map[string]*Collector{
		"delivery-target":      New(resolverStub{err: deliverytarget.ErrUnavailable}, suppressionStub{}, verificationStub{}),
		"suppression":          New(resolverStub{address: "lead@example.com"}, suppressionStub{err: suppressionpolicy.ErrUnavailable}, verificationStub{}),
		"address-verification": New(resolverStub{address: "lead@example.com"}, suppressionStub{}, verificationStub{err: addressverification.ErrUnavailable}),
		"без владельцев":       New(nil, nil, nil),
	}
	for name, collector := range cases {
		t.Run(name, func(t *testing.T) {
			reasons, err := collector.RiskReasons(context.Background(), request())
			if !errors.Is(err, domain.ErrRiskFactsUnavailable) {
				t.Fatalf("ошибка = %v, ожидалась ErrRiskFactsUnavailable", err)
			}
			if reasons != nil {
				t.Fatalf("признаки = %v, недоступность не должна давать пустой список как «рисков нет»", reasons)
			}
		})
	}
}
