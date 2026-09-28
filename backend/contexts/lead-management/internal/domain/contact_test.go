package domain

import (
	"errors"
	"testing"
)

func TestCreateContactRequiresValue(t *testing.T) {
	_, err := CreateContact(CreateContactParams{ID: "c1", CompanyID: "example.com"})
	if !errors.Is(err, ErrEmptyContactValue) {
		t.Fatalf("ожидалась ErrEmptyContactValue, получено %v", err)
	}
}

func TestCreateContactRequiresProvenance(t *testing.T) {
	email := &ContactAttribute{Value: "a@example.com"} // без происхождения
	_, err := CreateContact(CreateContactParams{ID: "c1", CompanyID: "example.com", Email: email})
	if !errors.Is(err, ErrMissingProvenance) {
		t.Fatalf("ожидалась ErrMissingProvenance, получено %v", err)
	}
}

func TestContactRoundTrip(t *testing.T) {
	email := &ContactAttribute{Value: "a@example.com", Provenance: attrProvenance("https://example.com", MethodRegex)}
	role := &ContactAttribute{Value: "CEO", Provenance: attrProvenance("https://example.com", MethodLLM)}
	c, err := CreateContact(CreateContactParams{
		ID:        "a@example.com",
		CompanyID: "example.com",
		Email:     email,
		Role:      role,
		CreatedAt: baseTime,
	})
	if err != nil {
		t.Fatalf("CreateContact: %v", err)
	}

	rehydrated, err := RehydrateContact(c.Snapshot())
	if err != nil {
		t.Fatalf("RehydrateContact: %v", err)
	}
	if rehydrated.ID() != c.ID() {
		t.Fatalf("ID %q, ожидалось %q", rehydrated.ID(), c.ID())
	}
	if rehydrated.Email().Value != "a@example.com" {
		t.Fatalf("Email %q", rehydrated.Email().Value)
	}
	if rehydrated.Role().Value != "CEO" {
		t.Fatalf("Role %q", rehydrated.Role().Value)
	}
}

func TestRehydrateContactRejectsContradictorySnapshot(t *testing.T) {
	email := &ContactAttribute{Value: "a@example.com"} // без происхождения
	if _, err := RehydrateContact(ContactSnapshot{ID: "c1", CompanyID: "example.com", Email: email}); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("ожидалась ErrInvalidSnapshot, получено %v", err)
	}
}
