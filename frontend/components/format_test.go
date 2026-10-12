package components

import (
	"strings"
	"testing"
	"time"
)

// TestFormatterDateAndTime проверяет локализованные форматы даты и времени.
func TestFormatterDateAndTime(t *testing.T) {
	moment := time.Date(2026, time.January, 2, 9, 5, 0, 0, time.UTC)
	formatter := DefaultFormatter()

	if got := formatter.Date(moment); got != "02.01.2026" {
		t.Fatalf("Date = %q, ожидалось 02.01.2026", got)
	}
	if got := formatter.DateTime(moment); got != "02.01.2026 09:05" {
		t.Fatalf("DateTime = %q, ожидалось 02.01.2026 09:05", got)
	}
}

// TestFormatterNumber проверяет группировку разрядов и десятичный разделитель.
func TestFormatterNumber(t *testing.T) {
	formatter := DefaultFormatter()
	cases := []struct {
		value    float64
		decimals int
		want     string
	}{
		{value: 1234567, decimals: 0, want: "1\u00a0234\u00a0567"},
		{value: 1000, decimals: 0, want: "1\u00a0000"},
		{value: -1500, decimals: 0, want: "−1\u00a0500"},
		{value: 1234.5, decimals: 2, want: "1\u00a0234,50"},
	}
	for _, tc := range cases {
		if got := formatter.Number(tc.value, tc.decimals); got != tc.want {
			t.Errorf("Number(%v, %d) = %q, ожидалось %q", tc.value, tc.decimals, got, tc.want)
		}
	}
}

// TestFormatterCurrency проверяет знак валюты и неизвестный код.
func TestFormatterCurrency(t *testing.T) {
	formatter := DefaultFormatter()
	if got := formatter.Currency(1234.5, "RUB"); got != "1\u00a0234,50\u00a0₽" {
		t.Fatalf("Currency(RUB) = %q", got)
	}
	if got := formatter.Currency(10, "xyz"); got != "10,00\u00a0XYZ" {
		t.Fatalf("Currency(unknown) = %q, ожидался код в верхнем регистре", got)
	}
}

// TestFormatterTimeZone проверяет локализацию часового пояса: перевод в именованный
// пояс и честную ошибку на неизвестном поясе.
func TestFormatterTimeZone(t *testing.T) {
	formatter := DefaultFormatter()
	moment := time.Date(2026, time.January, 2, 9, 5, 0, 0, time.UTC)

	got, err := formatter.DateTimeInZone(moment, "Europe/Moscow")
	if err != nil {
		t.Fatalf("DateTimeInZone(Moscow): %v", err)
	}
	if !strings.HasPrefix(got, "02.01.2026 12:05 ") {
		t.Fatalf("DateTimeInZone(Moscow) = %q, ожидалось 12:05 MSK", got)
	}
	if _, err := formatter.DateTimeInZone(moment, "Nowhere/Nope"); err == nil {
		t.Fatal("неизвестный часовой пояс не вернул ошибку")
	}
}
