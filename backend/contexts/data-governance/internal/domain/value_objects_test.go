package domain_test

import (
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
)

// TestProcessingPurposeValidation проверяет VO processing-purpose: цель и
// категория обязательны и валидируются при создании.
func TestProcessingPurposeValidation(t *testing.T) {
	t.Parallel()

	if _, err := domain.NewProcessingPurpose("  ", domain.CategoryGreen); !errors.Is(err, domain.ErrEmptyPurpose) {
		t.Fatalf("пустая цель: ошибка = %v, ожидалась ErrEmptyPurpose", err)
	}
	if _, err := domain.NewProcessingPurpose("outreach", domain.DataCategory("pink")); !errors.Is(err, domain.ErrUnknownCategory) {
		t.Fatalf("неизвестная категория: ошибка = %v, ожидалась ErrUnknownCategory", err)
	}

	purpose, err := domain.NewProcessingPurpose("outreach", domain.CategoryMixed)
	if err != nil {
		t.Fatalf("корректная цель: %v", err)
	}
	if purpose.Name() != "outreach" || purpose.Category() != domain.CategoryMixed {
		t.Fatalf("цель = %+v", purpose)
	}
	if !purpose.Equal(mustPurpose(t, "outreach", domain.CategoryMixed)) {
		t.Fatal("равные цели не совпали")
	}
}

// TestRetentionPeriodValidation проверяет границы срока хранения.
func TestRetentionPeriodValidation(t *testing.T) {
	t.Parallel()

	for _, days := range []int{0, -1, 3651} {
		if _, err := domain.NewRetentionPeriod(days); !errors.Is(err, domain.ErrInvalidRetention) {
			t.Fatalf("срок %d: ошибка = %v, ожидалась ErrInvalidRetention", days, err)
		}
	}

	period, err := domain.NewRetentionPeriod(30)
	if err != nil {
		t.Fatalf("корректный срок: %v", err)
	}
	if period.Days() != 30 {
		t.Fatalf("дни = %d", period.Days())
	}
	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if got := period.Until(from); !got.Equal(time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("Until = %s", got)
	}
}

// mustPurpose — вспомогательный конструктор цели для тестов.
func mustPurpose(t *testing.T, name string, category domain.DataCategory) domain.ProcessingPurpose {
	t.Helper()
	purpose, err := domain.NewProcessingPurpose(name, category)
	if err != nil {
		t.Fatalf("цель %q/%q: %v", name, category, err)
	}
	return purpose
}
