package publicationresult_test

import (
	"context"
	"errors"
	"testing"

	"lead-gen/contracts/publishing/publicationresult"
)

func TestInProcUnsetIsUnavailable(t *testing.T) {
	port := publicationresult.InProc(publicationresult.InProcHandler{})
	if _, err := port.Query(context.Background(), publicationresult.Query{TenantID: "t"}); !errors.Is(err, publicationresult.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}

func TestInProcDelegates(t *testing.T) {
	port := publicationresult.InProc(publicationresult.InProcHandler{
		QueryFunc: func(context.Context, publicationresult.Query) (publicationresult.Result, error) {
			return publicationresult.Result{Records: []publicationresult.Record{{Status: publicationresult.StatusPublished}}}, nil
		},
	})
	result, err := port.Query(context.Background(), publicationresult.Query{TenantID: "t"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(result.Records) != 1 || result.Records[0].Status != publicationresult.StatusPublished {
		t.Fatalf("результат неверен: %+v", result)
	}
}
