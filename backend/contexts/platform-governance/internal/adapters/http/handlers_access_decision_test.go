package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	httpapi "lead-gen/backend/contexts/platform-governance/internal/adapters/http"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
	"lead-gen/contracts/platform-governance/accessdecision"
	accessdecisionhttp "lead-gen/contracts/platform-governance/accessdecision/httpport"
)

// TestAccessDecisionContractRoundTrip проверяет сетевой транспорт контракта
// access-decision: клиент потребителя → POST /access-decision → use case
// владельца → решение. Положительное, отрицательное и пустой субъект.
func TestAccessDecisionContractRoundTrip(t *testing.T) {
	server := newAdminServer(t)
	create := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "cmd-ad-1", map[string]any{
		"tenant_id":    "tenant-1",
		"company_name": "ООО Ромашка",
		"description":  "Поставщик промышленного оборудования",
		"user_id":      "user-1",
		"login":        "client-1",
		"roles":        []string{"client"},
	})
	create.Body.Close()
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("создание тенанта = %d, ожидался 201", create.StatusCode)
	}

	client, err := accessdecisionhttp.New(server.URL, accessdecisionhttp.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	ctx := context.Background()

	if allowed, err := client.CanAct(ctx, "user-1", "tenant-1"); err != nil || !allowed {
		t.Fatalf("CanAct члена тенанта = %v, %v", allowed, err)
	}
	if allowed, err := client.CanAct(ctx, "stranger", "tenant-1"); err != nil || allowed {
		t.Fatalf("CanAct постороннего = %v, %v", allowed, err)
	}
	if _, err := client.CanAct(ctx, "", "tenant-1"); !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("CanAct с пустым субъектом = %v, ожидалась ErrUnavailable", err)
	}
}

// TestAccessDecisionNetworkMatchesInProc проверяет, что сетевой транспорт не
// меняет наблюдаемое поведение контракта: для тех же входов решение по сети
// совпадает с in-process решением владельца.
func TestAccessDecisionNetworkMatchesInProc(t *testing.T) {
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{Grants: adminGrants})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	defer server.Close()

	create := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "cmd-match-1", map[string]any{
		"tenant_id":    "tenant-1",
		"company_name": "ООО Ромашка",
		"description":  "Поставщик",
		"user_id":      "user-1",
		"login":        "client-1",
		"roles":        []string{"client"},
	})
	create.Body.Close()

	network, err := accessdecisionhttp.New(server.URL, accessdecisionhttp.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	inproc := platformgovernance.NewAccessDecision(slice)

	ctx := context.Background()
	for _, actor := range []string{"user-1", "stranger"} {
		wantAllowed, wantErr := inproc.CanAct(ctx, actor, "tenant-1")
		gotAllowed, gotErr := network.CanAct(ctx, actor, "tenant-1")
		if wantAllowed != gotAllowed || (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("решение актора %q: in-proc %v/%v, сеть %v/%v", actor, wantAllowed, wantErr, gotAllowed, gotErr)
		}
	}
}

// TestAccessDecisionContractUnavailableWithoutUseCase проверяет честную
// недоступность: сервер без подключённого use case отвечает 503, клиент получает
// отказ, а не выдуманное решение.
func TestAccessDecisionContractUnavailableWithoutUseCase(t *testing.T) {
	server := httptest.NewServer(httpapi.NewServer(httpapi.Dependencies{}))
	defer server.Close()

	client, err := accessdecisionhttp.New(server.URL, accessdecisionhttp.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatalf("httpport.New: %v", err)
	}
	if _, err := client.CanAct(context.Background(), "user-1", "tenant-1"); !errors.Is(err, accessdecision.ErrUnavailable) {
		t.Fatalf("ошибка = %v, ожидалась ErrUnavailable", err)
	}
}
