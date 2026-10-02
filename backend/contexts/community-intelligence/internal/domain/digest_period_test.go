package domain

import (
	"errors"
	"testing"
	"time"
)

func TestNewDigestPeriodAcceptsOrderedBounds(t *testing.T) {
	from := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(24 * time.Hour)
	period, err := NewDigestPeriod(from, to)
	if err != nil {
		t.Fatalf("NewDigestPeriod: %v", err)
	}
	if !period.Contains(from) || period.Contains(to) {
		t.Fatal("период должен быть полуинтервалом [from, to)")
	}
	if !period.From().Equal(from) || !period.To().Equal(to) {
		t.Fatalf("границы периода: %s..%s", period.From(), period.To())
	}
}

func TestNewDigestPeriodRejectsReversedBounds(t *testing.T) {
	to := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	_, err := NewDigestPeriod(to.Add(time.Hour), to)
	if !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("ожидался ErrInvalidPeriod, получено %v", err)
	}
}

func TestNewDigestPeriodRejectsZeroBounds(t *testing.T) {
	if _, err := NewDigestPeriod(time.Time{}, time.Now()); !errors.Is(err, ErrInvalidPeriod) {
		t.Fatalf("ожидался ErrInvalidPeriod для нулевого начала, получено %v", err)
	}
}
