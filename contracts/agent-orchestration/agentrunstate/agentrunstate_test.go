package agentrunstate

import (
	"context"
	"errors"
	"testing"
)

func TestRequestValidate(t *testing.T) {
	if err := (Request{RunID: "run-1", TenantID: "tenant-1"}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (Request{RunID: "run-1"}).Validate(); err == nil {
		t.Fatal("запрос без тенанта должен быть отвергнут")
	}
}

func TestListRequestValidate(t *testing.T) {
	if err := (ListRequest{TenantID: "tenant-1"}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (ListRequest{}).Validate(); err == nil {
		t.Fatal("запрос без тенанта должен быть отвергнут")
	}
	if err := (ListRequest{TenantID: "tenant-1", Limit: -1}).Validate(); err == nil {
		t.Fatal("отрицательный предел должен быть отвергнут")
	}
}

func TestInProcUnavailable(t *testing.T) {
	reader := InProc(InProcHandler{})
	if _, err := reader.RunState(context.Background(), Request{RunID: "run-1", TenantID: "tenant-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
	if _, err := reader.List(context.Background(), ListRequest{TenantID: "tenant-1"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("список без функции = %v, ожидалась ErrUnavailable", err)
	}
}

func TestInProcList(t *testing.T) {
	reader := InProc(InProcHandler{
		ListFunc: func(context.Context, ListRequest) ([]RunSummary, error) {
			return []RunSummary{{RunID: "run-1", TenantID: "tenant-1"}}, nil
		},
	})
	summaries, err := reader.List(context.Background(), ListRequest{TenantID: "tenant-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(summaries) != 1 || summaries[0].RunID != "run-1" {
		t.Fatalf("список = %+v", summaries)
	}
}
