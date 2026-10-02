package contentversion

import (
	"context"
	"errors"
	"testing"
)

func TestContractIdentity(t *testing.T) {
	if ContractName != "content-version" {
		t.Errorf("ContractName: получено %q", ContractName)
	}
	if SchemaVersion == "" {
		t.Error("пустая SchemaVersion")
	}
}

func TestInProcUnavailableWithoutFunc(t *testing.T) {
	_, err := InProc(InProcHandler{}).Version(context.Background(), Query{AssetID: "a1"})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("ожидалась ErrUnavailable, получено %v", err)
	}
}

func TestInProcDelegates(t *testing.T) {
	source := InProc(InProcHandler{
		VersionFunc: func(_ context.Context, q Query) (Version, error) {
			return Version{AssetID: q.AssetID, VersionID: "v1", Revision: 1, Body: "text"}, nil
		},
	})
	version, err := source.Version(context.Background(), Query{AssetID: "a1"})
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version.AssetID != "a1" || version.VersionID != "v1" || version.Revision != 1 || version.Body != "text" {
		t.Fatalf("версия неполна: %+v", version)
	}
}
