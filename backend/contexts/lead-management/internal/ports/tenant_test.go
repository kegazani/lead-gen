package ports_test

import (
	"context"
	"testing"

	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// TestTenantScope проверяет область хранения тенанта: установку, чтение, фолбэк на
// пустую область и отсутствие влияния на родительский контекст.
func TestTenantScope(t *testing.T) {
	base := context.Background()
	if got := ports.TenantFrom(base); got != "" {
		t.Fatalf("пустой контекст: тенант = %q, ожидался \"\"", got)
	}

	scoped := ports.WithTenant(base, "tenant-a")
	if got := ports.TenantFrom(scoped); got != "tenant-a" {
		t.Fatalf("установленный тенант = %q, ожидался tenant-a", got)
	}
	if got := ports.TenantFrom(base); got != "" {
		t.Fatalf("родительский контекст изменился: тенант = %q", got)
	}

	// nil-контекст безопасен: область не установлена.
	if got := ports.TenantFrom(nil); got != "" {
		t.Fatalf("nil-контекст: тенант = %q, ожидался \"\"", got)
	}
}
