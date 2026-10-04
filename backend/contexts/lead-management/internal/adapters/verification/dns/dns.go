// Package dns реализует ports.DNSResolver поверх стандартного резолвера Go:
// проверка существования домена и записей MX с откатом на запись A по RFC 5321.
//
// Пакет — реальный адаптер за портом: он выполняет DNS-запросы, а не возвращает
// фиксированный результат. Отсутствие домена (NXDOMAIN) отличается от сбоя
// резолвера: первое — доменный исход, второе — недоступность, которая не
// выдаётся за «домен есть» или «домена нет».
package dns

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// lookup — минимальная поверхность резолвера, чтобы адаптер был тестируем без
// сети. Её реализует *net.Resolver.
type lookup interface {
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
	LookupHost(ctx context.Context, host string) ([]string, error)
}

// Resolver — адаптер DNS-проверки домена.
type Resolver struct {
	lookup lookup
}

// Resolver реализует порт DNS-проверки.
var _ ports.DNSResolver = (*Resolver)(nil)

// New создаёт адаптер поверх системного резолвера.
func New() *Resolver {
	return &Resolver{lookup: net.DefaultResolver}
}

// NewWith создаёт адаптер поверх заданного резолвера (для тестов и сборки).
func NewWith(l lookup) *Resolver {
	return &Resolver{lookup: l}
}

// LookupMX возвращает хосты MX домена. Домен без записей даёт пустой список без
// ошибки — вызывающий выполняет откат на запись A.
func (r *Resolver) LookupMX(ctx context.Context, domain string) ([]string, error) {
	mxs, err := r.lookup.LookupMX(ctx, domain)
	if err != nil {
		return nil, r.translate(err, "MX", domain)
	}
	hosts := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		hosts = append(hosts, strings.TrimSuffix(mx.Host, "."))
	}
	return hosts, nil
}

// LookupHost возвращает адреса A/AAAA домена. Домен без записей даёт пустой
// список без ошибки.
func (r *Resolver) LookupHost(ctx context.Context, domain string) ([]string, error) {
	hosts, err := r.lookup.LookupHost(ctx, domain)
	if err != nil {
		return nil, r.translate(err, "A", domain)
	}
	return hosts, nil
}

// translate переводит ошибку резолвера в исход порта: отсутствие домена —
// ErrDomainNotFound, отмена контекста — как есть, прочее — ErrResolverUnavailable.
func (r *Resolver) translate(err error, record, domain string) error {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return fmt.Errorf("%w: %s", ports.ErrDomainNotFound, domain)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%w: %s %s: %v", ports.ErrResolverUnavailable, record, domain, err)
}
