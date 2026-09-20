package web

import (
	"strings"
	"testing"

	"lead-gen/frontend/public/internal/ports"
)

// filledSubmission — заявка без ошибок; отдельные тесты портят одно поле.
func filledSubmission() ports.Submission {
	return ports.Submission{
		Form:    "demo",
		Name:    "Иван",
		Contact: "ivan@example.com",
		Message: "Здравствуйте",
		Consent: true,
	}
}

func TestValidateSubmissionAcceptsFilledSubmission(t *testing.T) {
	if problems := validateSubmission(filledSubmission()); len(problems) != 0 {
		t.Fatalf("корректная заявка отвергнута: %v", problems)
	}
}

func TestValidateSubmissionAcceptsPhone(t *testing.T) {
	submission := filledSubmission()
	submission.Contact = "+7 (999) 123-45-67"
	if problems := validateSubmission(submission); len(problems) != 0 {
		t.Fatalf("телефон отвергнут: %v", problems)
	}
}

func TestValidateSubmissionRejectsBlankFields(t *testing.T) {
	problems := validateSubmission(ports.Submission{})
	for _, field := range []string{"name", "contact", "consent"} {
		if problems[field] == "" {
			t.Errorf("пустая заявка не отметила поле %q: %v", field, problems)
		}
	}
}

func TestValidateSubmissionRejectsNonContact(t *testing.T) {
	for _, contact := range []string{
		"без собаки и цифр",
		"@example.com",
		"ivan@",
		"ivan@example",
		"ivan@example.",
		"ivan@@example.com",
		"12345",
	} {
		t.Run(contact, func(t *testing.T) {
			submission := filledSubmission()
			submission.Contact = contact
			if problems := validateSubmission(submission); problems["contact"] == "" {
				t.Errorf("неконтактное значение %q принято", contact)
			}
		})
	}
}

func TestValidateSubmissionRejectsLongMessage(t *testing.T) {
	submission := filledSubmission()
	submission.Message = strings.Repeat("я", maxMessageLen+1)
	if problems := validateSubmission(submission); problems["message"] == "" {
		t.Error("слишком длинное сообщение принято")
	}
}

func TestValidateSubmissionAcceptsMessageAtLimit(t *testing.T) {
	submission := filledSubmission()
	submission.Message = strings.Repeat("я", maxMessageLen)
	if problems := validateSubmission(submission); problems["message"] != "" {
		t.Errorf("сообщение на пределе длины отвергнуто: %v", problems)
	}
}

func TestValidateSubmissionRejectsMissingConsent(t *testing.T) {
	submission := filledSubmission()
	submission.Consent = false
	if problems := validateSubmission(submission); problems["consent"] == "" {
		t.Error("заявка без согласия принята")
	}
}

func TestValuesOfKeepsEnteredValues(t *testing.T) {
	submission := filledSubmission()
	values := valuesOf(submission)
	if values.Name != submission.Name || values.Contact != submission.Contact ||
		values.Message != submission.Message || values.Consent != submission.Consent {
		t.Errorf("значения формы потеряны: %+v", values)
	}
}
