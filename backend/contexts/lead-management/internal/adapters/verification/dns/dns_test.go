package dns

import (
	"context"
	"errors"
	"net"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// fakeLookup — резолвер для тестов.
type fakeLookup struct {
	mx      []*net.MX
	hosts   []string
	mxErr   error
	hostErr error
}

func (f *fakeLookup) LookupMX(context.Context, string) ([]*net.MX, error) {
	if f.mxErr != nil {
		return nil, f.mxErr
	}
	return f.mx, nil
}

func (f *fakeLookup) LookupHost(context.Context, string) ([]string, error) {
	if f.hostErr != nil {
		return nil, f.hostErr
	}
	return f.hosts, nil
}

func TestLookupMX_TrimsTrailingDot(t *testing.T) {
	resolver := NewWith(&fakeLookup{mx: []*net.MX{{Host: "mx1.example.com."}, {Host: "mx2.example.com."}}})
	hosts, err := resolver.LookupMX(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupMX: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "mx1.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestLookupHost_EmptyIsNotError(t *testing.T) {
	resolver := NewWith(&fakeLookup{})
	hosts, err := resolver.LookupHost(context.Background(), "example.com")
	if err != nil {
		t.Fatalf("LookupHost: %v", err)
	}
	if len(hosts) != 0 {
		t.Fatalf("ожидался пустой список, got %v", hosts)
	}
}

func TestLookupMX_DomainNotFound(t *testing.T) {
	resolver := NewWith(&fakeLookup{mxErr: &net.DNSError{Err: "no such host", Name: "missing.example", IsNotFound: true}})
	if _, err := resolver.LookupMX(context.Background(), "missing.example"); !errors.Is(err, ports.ErrDomainNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrDomainNotFound", err)
	}
}

func TestLookupMX_ResolverUnavailable(t *testing.T) {
	resolver := NewWith(&fakeLookup{mxErr: errors.New("network unreachable")})
	if _, err := resolver.LookupMX(context.Background(), "example.com"); !errors.Is(err, ports.ErrResolverUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrResolverUnavailable", err)
	}
}

func TestLookupHost_ContextCancelled(t *testing.T) {
	resolver := NewWith(&fakeLookup{hostErr: context.Canceled})
	if _, err := resolver.LookupHost(context.Background(), "example.com"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ошибка = %v, ожидалась context.Canceled", err)
	}
}
