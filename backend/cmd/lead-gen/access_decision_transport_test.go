package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/platform-governance/accessdecision"
)

// TestAssembleAccessDecisionChoosesNetwork проверяет выбор транспорта по
// топологии: заданный адрес владельца переключает решение на сетевой клиент
// контракта, а владелец в этом процессе не собирается.
func TestAssembleAccessDecisionChoosesNetwork(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != accessdecision.PathCanAct {
			t.Errorf("путь = %q", r.URL.Path)
		}
		var req accessdecision.CanActRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		_ = json.NewEncoder(w).Encode(accessdecision.CanActResponse{Allowed: req.Actor == "user-1"})
	}))
	defer server.Close()

	opts := buildOptions{accessDecisionURL: server.URL}
	assembleAccessDecision(nil, &opts, func(selectedService) (composition.Service, error) {
		t.Fatal("владелец не должен собираться при сетевом транспорте")
		return nil, nil
	}, observ.NewLogger())

	if opts.accessDecision == nil {
		t.Fatal("решение о доступе не подключено")
	}
	allowed, err := opts.accessDecision.CanAct(context.Background(), "user-1", "tenant-1")
	if err != nil || !allowed {
		t.Fatalf("сетевое решение = %v, %v", allowed, err)
	}
}

// TestAssembleAccessDecisionWithoutOwnerIsUnavailable проверяет честный отказ:
// без адреса и без выбранного владельца решение недоступно, а не «разрешено».
func TestAssembleAccessDecisionWithoutOwnerIsUnavailable(t *testing.T) {
	opts := buildOptions{}
	assembleAccessDecision(nil, &opts, func(selectedService) (composition.Service, error) {
		return nil, nil
	}, observ.NewLogger())

	if opts.accessDecision == nil {
		t.Fatal("решение не задано")
	}
	if allowed, err := opts.accessDecision.CanAct(context.Background(), "user-1", "tenant-1"); allowed || !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("решение без владельца = %v, %v", allowed, err)
	}
}
