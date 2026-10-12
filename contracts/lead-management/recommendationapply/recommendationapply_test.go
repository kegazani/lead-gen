package recommendationapply

import (
	"context"
	"errors"
	"testing"
)

func TestChangeValidate(t *testing.T) {
	if err := (Change{Kind: KindNewSearch, Criteria: "агентства"}).Validate(); err != nil {
		t.Fatalf("корректное изменение отвергнуто: %v", err)
	}
	if err := (Change{Kind: KindNewSearch, Targets: []string{"saas"}}).Validate(); err != nil {
		t.Fatalf("изменение с целями отвергнуто: %v", err)
	}
	cases := map[string]Change{
		"неизвестный вид":     {Kind: "wipe", Criteria: "x"},
		"пустой критерий":     {Kind: KindNewSearch},
		"негативный лимит":    {Kind: KindNewSearch, Criteria: "x", Limit: -1},
		"пустые цели и текст": {Kind: KindNewSearch, Targets: []string{" ", ""}},
	}
	for name, change := range cases {
		if err := change.Validate(); err == nil {
			t.Errorf("%s: ошибки не было", name)
		}
	}
}

func TestApplicationValidate(t *testing.T) {
	valid := Application{TenantID: "t1", ChangeID: "c1", Change: Change{Kind: KindNewSearch, Criteria: "x"}}
	if err := valid.Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (Application{ChangeID: "c1", Change: Change{Kind: KindNewSearch, Criteria: "x"}}).Validate(); err == nil {
		t.Fatal("пустой tenant_id должен быть отвергнут")
	}
	if err := (Application{TenantID: "t1", Change: Change{Kind: KindNewSearch, Criteria: "x"}}).Validate(); err == nil {
		t.Fatal("пустой change_id должен быть отвергнут")
	}
}

func TestInProcUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.ApplyRecommendation(context.Background(), Application{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestInProcRoundTrip(t *testing.T) {
	port := InProc(InProcHandler{
		ApplyFunc: func(_ context.Context, application Application) (Receipt, error) {
			if application.ChangeID != "c1" {
				t.Fatalf("change_id = %q", application.ChangeID)
			}
			return Receipt{JobID: "reco-1"}, nil
		},
	})
	receipt, err := port.ApplyRecommendation(context.Background(), Application{
		TenantID: "t1", ChangeID: "c1", Change: Change{Kind: KindNewSearch, Criteria: "x"},
	})
	if err != nil {
		t.Fatalf("ApplyRecommendation: %v", err)
	}
	if receipt.JobID != "reco-1" {
		t.Fatalf("job_id = %q", receipt.JobID)
	}
}
