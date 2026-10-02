package attributedresult_test

import (
	"testing"
	"time"

	"lead-gen/contracts/sales-workflow/attributedresult"
	"lead-gen/contracts/sales-workflow/envelope"
)

func valid() attributedresult.AttributedResult {
	result := attributedresult.NewAttributedResult(envelope.Envelope{
		EventID:       "evt-1",
		OccurredAt:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
	})
	result.DealID = "deal-1"
	result.ActionRef = "action-1"
	result.EvidenceRef = "evidence-1"
	result.ExternalRef = "crm-1"
	result.ConfirmedAt = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	return result
}

func TestAttributedResultValidate(t *testing.T) {
	if err := valid().Validate(); err != nil {
		t.Fatalf("корректный контракт: %v", err)
	}

	cases := map[string]func(*attributedresult.AttributedResult){
		"без deal_id":      func(r *attributedresult.AttributedResult) { r.DealID = "" },
		"без action_ref":   func(r *attributedresult.AttributedResult) { r.ActionRef = "" },
		"без evidence_ref": func(r *attributedresult.AttributedResult) { r.EvidenceRef = "" },
		"без external_ref": func(r *attributedresult.AttributedResult) { r.ExternalRef = "" },
		"без confirmed_at": func(r *attributedresult.AttributedResult) { r.ConfirmedAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			result := valid()
			mutate(&result)
			if err := result.Validate(); err == nil {
				t.Fatal("некорректный контракт не отвергнут")
			}
		})
	}
}

func TestIdempotencyKeyStable(t *testing.T) {
	first := valid()
	second := valid()
	second.EventID = "evt-2"
	if first.IdempotencyKey() != second.IdempotencyKey() {
		t.Fatal("ключ идемпотентности зависит от event_id")
	}
	second.ExternalRef = "crm-2"
	if first.IdempotencyKey() == second.IdempotencyKey() {
		t.Fatal("ключ идемпотентности не реагирует на внешнюю ссылку")
	}
}
