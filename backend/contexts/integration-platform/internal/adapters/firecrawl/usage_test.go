package firecrawl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreditUsageSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data":    map[string]any{"remainingCredits": 1234, "planCredits": 10000},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	usage, err := client.CreditUsage(context.Background())
	if err != nil {
		t.Fatalf("CreditUsage: %v", err)
	}
	if usage.RemainingCredits != 1234 {
		t.Fatalf("RemainingCredits = %d, want 1234", usage.RemainingCredits)
	}
	if usage.PlanCredits != 10000 {
		t.Fatalf("PlanCredits = %d, want 10000", usage.PlanCredits)
	}
}

func TestCreditUsageMissingFieldIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"planCredits": 10000}})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, err := client.CreditUsage(context.Background()); err == nil {
		t.Fatal("CreditUsage без remainingCredits должен вернуть ошибку")
	}
}

func TestTokenUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{
			"success": true,
			"data":    map[string]any{"remainingTokens": 555},
		})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	usage, err := client.TokenUsage(context.Background())
	if err != nil {
		t.Fatalf("TokenUsage: %v", err)
	}
	if usage.RemainingTokens != 555 {
		t.Fatalf("RemainingTokens = %d, want 555", usage.RemainingTokens)
	}
}

func TestTokenUsageMissingFieldIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	client := New(Config{BaseURL: srv.URL})
	if _, err := client.TokenUsage(context.Background()); err == nil {
		t.Fatal("TokenUsage без remainingTokens должен вернуть ошибку")
	}
}

func TestUsageCacheUnavailableOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	u := NewUsage(New(Config{BaseURL: srv.URL}), nil)
	if err := u.RefreshCredit(context.Background()); err == nil {
		t.Fatal("RefreshCredit при ошибке сервера должен вернуть ошибку")
	}
	if bal := u.CreditBalance(); bal.Available {
		t.Fatal("остаток должен быть недоступен после неудачного обновления")
	}
}

func TestUsageCacheKeepsLastSuccessfulOnError(t *testing.T) {
	ok := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if ok {
			writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"remainingCredits": 777}})
			return
		}
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	u := NewUsage(New(Config{BaseURL: srv.URL}), nil)
	if err := u.RefreshCredit(context.Background()); err != nil {
		t.Fatalf("первое обновление: %v", err)
	}

	ok = false
	if err := u.RefreshCredit(context.Background()); err == nil {
		t.Fatal("второе обновление при ошибке должно вернуть ошибку")
	}
	if bal := u.CreditBalance(); !bal.Available || bal.Value != 777 {
		t.Fatalf("последнее успешное значение должно сохраниться: %+v", bal)
	}
}
