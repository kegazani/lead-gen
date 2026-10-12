package leadrelevance

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRequestValidate(t *testing.T) {
	if err := (Request{JobID: "j1", CompanyID: "c1"}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (Request{CompanyID: "c1"}).Validate(); err == nil {
		t.Fatal("пустой job_id должен быть отвергнут")
	}
	if err := (Request{JobID: "j1"}).Validate(); err == nil {
		t.Fatal("пустой company_id должен быть отвергнут")
	}
}

func TestInProcUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Read(context.Background(), Request{JobID: "j1", CompanyID: "c1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestInProcRoundTrip(t *testing.T) {
	at := time.Date(2026, time.October, 6, 10, 0, 0, 0, time.UTC)
	port := InProc(InProcHandler{
		ReadFunc: func(_ context.Context, request Request) (Assessment, error) {
			if request.JobID != "j1" || request.CompanyID != "c1" {
				t.Fatalf("запрос = %+v", request)
			}
			return Assessment{
				Contract:      ContractName,
				SchemaVersion: SchemaVersion,
				JobID:         "j1",
				CompanyID:     "c1",
				Verdict:       VerdictFits,
				SignalKind:    SignalDirect,
				Evidence:      []Evidence{{Criterion: "интерес", Fact: "заявили", SourceURL: "https://x.example", Reasoning: "прямо", SignalKind: SignalDirect}},
				AssessedAt:    at,
			}, nil
		},
	})
	assessment, err := port.Read(context.Background(), Request{JobID: "j1", CompanyID: "c1"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if assessment.Verdict != VerdictFits || assessment.SignalKind != SignalDirect || len(assessment.Evidence) != 1 {
		t.Fatalf("оценка = %+v", assessment)
	}
	if assessment.Evidence[0].SourceURL != "https://x.example" {
		t.Fatalf("свидетельство = %+v", assessment.Evidence[0])
	}
}
