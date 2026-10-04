package domain

import (
	"errors"
	"testing"
	"time"
)

func TestParseContactAddress_NormalizesDomain(t *testing.T) {
	address, err := ParseContactAddress("  Sales@Example.COM ")
	if err != nil {
		t.Fatalf("ParseContactAddress: %v", err)
	}
	if address.Local() != "Sales" {
		t.Fatalf("Local = %q, ожидалось Sales (регистр локальной части значим)", address.Local())
	}
	if address.Domain() != "example.com" {
		t.Fatalf("Domain = %q, ожидалось example.com", address.Domain())
	}
	if address.String() != "Sales@example.com" {
		t.Fatalf("String = %q", address.String())
	}
}

func TestParseContactAddress_RejectsInvalid(t *testing.T) {
	for _, raw := range []string{"", "   ", "not-an-address", "a@", "@b.com", "a b@c.com"} {
		if _, err := ParseContactAddress(raw); !errors.Is(err, ErrInvalidAddress) {
			t.Fatalf("адрес %q: ошибка = %v, ожидалась ErrInvalidAddress", raw, err)
		}
	}
}

func TestIsRoleAddress(t *testing.T) {
	for _, local := range []string{"info", "Sales", "SUPPORT", "noreply", "no-reply"} {
		if !IsRoleAddress(local) {
			t.Fatalf("%q должен считаться ролевым", local)
		}
	}
	for _, local := range []string{"ivan.petrov", "ceo", "founder"} {
		if IsRoleAddress(local) {
			t.Fatalf("%q не должен считаться ролевым", local)
		}
	}
}

func TestIsDisposableDomain(t *testing.T) {
	if !IsDisposableDomain("Mailinator.com") {
		t.Fatal("mailinator.com должен быть одноразовым")
	}
	if IsDisposableDomain("example.com") {
		t.Fatal("example.com не должен быть одноразовым")
	}
}

func TestCapForCatchAll(t *testing.T) {
	if got := CapForCatchAll(StatusDeliverable); got != StatusRisky {
		t.Fatalf("deliverable в catch-all: got %q, ожидался risky", got)
	}
	for _, status := range []VerificationStatus{StatusUnknown, StatusRole, StatusInvalid, StatusDisposable, StatusAbuse, StatusRisky} {
		if got := CapForCatchAll(status); got != status {
			t.Fatalf("статус %q не должен понижаться, got %q", status, got)
		}
	}
}

func TestVerificationStatus_BlocksSend(t *testing.T) {
	for _, status := range []VerificationStatus{StatusInvalid, StatusDisposable, StatusAbuse} {
		if !status.BlocksSend() {
			t.Fatalf("%q должен блокировать отправку", status)
		}
	}
	for _, status := range []VerificationStatus{StatusDeliverable, StatusRisky, StatusUnknown, StatusRole} {
		if status.BlocksSend() {
			t.Fatalf("%q не должен блокировать отправку", status)
		}
	}
}

func TestNewCheckedEmail_RequiresConsistency(t *testing.T) {
	address, err := ParseContactAddress("sales@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCheckedEmail("sales@example.com", address, StatusDeliverable, "MX и получатель приняты", "provider-x", baseTime, false); err != nil {
		t.Fatalf("корректный результат отвергнут: %v", err)
	}
	cases := []struct {
		name    string
		raw     string
		status  VerificationStatus
		reason  string
		source  string
		checked time.Time
		wantErr error
	}{
		{"неизвестный статус", "sales@example.com", VerificationStatus("maybe"), "причина", "источник", baseTime, ErrInvalidVerificationStatus},
		{"без причины", "sales@example.com", StatusDeliverable, "  ", "источник", baseTime, ErrInvalidVerificationResult},
		{"без источника", "sales@example.com", StatusDeliverable, "причина", "", baseTime, ErrInvalidVerificationResult},
		{"без даты", "sales@example.com", StatusDeliverable, "причина", "источник", time.Time{}, ErrInvalidVerificationResult},
		{"без адреса", "  ", StatusDeliverable, "причина", "источник", baseTime, ErrInvalidVerificationResult},
	}
	for _, tc := range cases {
		if _, err := NewCheckedEmail(tc.raw, address, tc.status, tc.reason, tc.source, tc.checked, false); !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: ошибка = %v, ожидалась %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestDefaultVerificationPolicy(t *testing.T) {
	policy := DefaultVerificationPolicy()

	if decision, share := policy.Decision(StatusDeliverable); decision != DecisionAllow || share != 0 {
		t.Fatalf("deliverable: got %q/%v", decision, share)
	}
	if decision, share := policy.Decision(StatusRisky); decision != DecisionLimit || share <= 0 {
		t.Fatalf("risky: got %q/%v, ожидалось ограничение доли", decision, share)
	}
	for _, status := range []VerificationStatus{StatusInvalid, StatusDisposable, StatusAbuse} {
		if decision, _ := policy.Decision(status); decision != DecisionBlock {
			t.Fatalf("%q: got %q, ожидался block", status, decision)
		}
	}
}
