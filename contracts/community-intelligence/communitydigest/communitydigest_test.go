package communitydigest

import (
	"context"
	"errors"
	"testing"
)

func TestInProcHandlerWithoutFuncIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Get(context.Background(), GetQuery{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, получено %v", err)
	}
}

func TestInProcHandlerDelegates(t *testing.T) {
	want := DigestView{DigestID: "d-1", CommunityID: "c-1", Topics: []TopicView{{Name: "тема", Mentions: 2}}}
	port := InProc(InProcHandler{
		GetFunc: func(_ context.Context, q GetQuery) (DigestView, error) {
			if q.TenantID != "t-1" {
				t.Fatalf("TenantID = %q", q.TenantID)
			}
			return want, nil
		},
	})
	got, err := port.Get(context.Background(), GetQuery{DigestID: "d-1", TenantID: "t-1"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.DigestID != want.DigestID || got.Topics[0].Name != "тема" {
		t.Fatalf("результат: %+v", got)
	}
}
