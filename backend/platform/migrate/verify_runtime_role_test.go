package migrate

import "testing"

func TestSupportedProvisionRelationKindRegistryIsClosed(t *testing.T) {
	for _, kind := range []byte{'r', 'p', 'v', 'm', 'S', 'i', 'I', 't', 'c'} {
		if !supportedProvisionRelationKind(kind) {
			t.Errorf("provision-supported relkind %q was not recognized by runtime verifier", kind)
		}
	}
	for _, kind := range []byte{'f', 'x', 'z'} {
		if supportedProvisionRelationKind(kind) {
			t.Errorf("unsupported relkind %q passed runtime verification", kind)
		}
	}
}

func TestSupportedProvisionTypeKindRegistryIsClosed(t *testing.T) {
	for _, kind := range []byte{'b', 'c', 'd', 'e'} {
		if !supportedProvisionTypeKind(kind) {
			t.Errorf("provision-supported type kind %q was not verified", kind)
		}
	}
	for _, kind := range []byte{'m', 'p', 'r', 'x'} {
		if supportedProvisionTypeKind(kind) {
			t.Errorf("unsupported type kind %q passed runtime verification", kind)
		}
	}
}
