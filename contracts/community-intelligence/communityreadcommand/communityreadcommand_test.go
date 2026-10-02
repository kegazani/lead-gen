package communityreadcommand

import (
	"context"
	"errors"
	"testing"
)

func TestInProcHandlerWithoutFuncIsUnavailable(t *testing.T) {
	port := InProc(InProcHandler{})
	if _, err := port.Read(context.Background(), ReadCommand{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидался ErrUnavailable, получено %v", err)
	}
}

func TestInProcHandlerDelegates(t *testing.T) {
	want := ReadResult{DigestID: "d-1", CommunityID: "c-1", TopicCount: 3}
	port := InProc(InProcHandler{
		ReadFunc: func(_ context.Context, cmd ReadCommand) (ReadResult, error) {
			if cmd.CommunityID != "c-1" {
				t.Fatalf("CommunityID = %q", cmd.CommunityID)
			}
			return want, nil
		},
	})
	got, err := port.Read(context.Background(), ReadCommand{CommunityID: "c-1", TenantID: "t-1"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.DigestID != want.DigestID || got.TopicCount != 3 {
		t.Fatalf("результат: %+v", got)
	}
}
