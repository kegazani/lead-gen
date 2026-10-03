package leadsearch_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/lead-management/leadsearch"
)

// TestInProcDelegates проверяет, что прямой адаптер передаёт запрос обработчику и
// возвращает его результат.
func TestInProcDelegates(t *testing.T) {
	var got leadsearch.Query
	port := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(_ context.Context, query leadsearch.Query) (leadsearch.Result, error) {
			got = query
			return leadsearch.Result{
				Found:    2,
				FromPool: 1,
				Crawled:  1,
				Leads: []leadsearch.LeadRef{
					{ID: "pool.example", Origin: leadsearch.OriginPool},
					{ID: "crawl.example", Origin: leadsearch.OriginCrawl},
				},
			}, nil
		},
	})

	result, err := port.Search(context.Background(), leadsearch.Query{
		RequestID: "req-1", TenantID: "tenant-1", Criteria: "разработка", Limit: 2,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if got.Criteria != "разработка" || got.Limit != 2 || got.RequestID != "req-1" {
		t.Fatalf("обработчик получил %+v", got)
	}
	if result.Found != 2 || result.FromPool != 1 || result.Crawled != 1 || len(result.Leads) != 2 {
		t.Fatalf("результат = %+v", result)
	}
}

// TestInProcNilHandlerUnavailable проверяет, что незаданный обработчик честно
// сообщает о недоступности владельца, а не выдаёт пустой успех.
func TestInProcNilHandlerUnavailable(t *testing.T) {
	port := leadsearch.InProc(leadsearch.InProcHandler{})
	if _, err := port.Search(context.Background(), leadsearch.Query{Criteria: "x"}); !errors.Is(err, leadsearch.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

// TestInProcPropagatesDomainErrors проверяет, что доменный отказ владельца
// доходит до потребителя без подмены успехом.
func TestInProcPropagatesDomainErrors(t *testing.T) {
	port := leadsearch.InProc(leadsearch.InProcHandler{
		SearchFunc: func(context.Context, leadsearch.Query) (leadsearch.Result, error) {
			return leadsearch.Result{}, leadsearch.ErrRejected
		},
	})
	if _, err := port.Search(context.Background(), leadsearch.Query{Criteria: "x"}); !errors.Is(err, leadsearch.ErrRejected) {
		t.Fatalf("ошибка = %v, ожидалась ErrRejected", err)
	}
}

// TestQueryValidate проверяет обязательные поля запроса.
func TestQueryValidate(t *testing.T) {
	if err := (leadsearch.Query{Criteria: "x", Limit: 1}).Validate(); err != nil {
		t.Fatalf("корректный запрос отвергнут: %v", err)
	}
	if err := (leadsearch.Query{Limit: 1}).Validate(); err == nil {
		t.Fatal("пустой критерий не отвергнут")
	}
	if err := (leadsearch.Query{Criteria: "x", Limit: -1}).Validate(); err == nil {
		t.Fatal("отрицательный лимит не отвергнут")
	}
}
