package httpapi_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/contexts/platform-governance/internal/adapters/grants"
	"lead-gen/backend/contexts/platform-governance/internal/domain"
)

// newAdminServer собирает сервер с платформенным администратором admin-1.
func newAdminServer(t *testing.T) *httptest.Server {
	t.Helper()
	adminGrants, err := grants.FromStrings("admin-1", []string{string(domain.RoleAdmin)})
	if err != nil {
		t.Fatalf("grants: %v", err)
	}
	slice, err := platformgovernance.Build(platformgovernance.Config{Grants: adminGrants})
	if err != nil {
		t.Fatalf("сборка среза: %v", err)
	}
	server := httptest.NewServer(slice.Server)
	t.Cleanup(server.Close)
	return server
}

// apiJSON выполняет JSON-запрос с заголовками актора и команды.
func apiJSON(t *testing.T, server *httptest.Server, method, path, actor, commandID string, payload any) *http.Response {
	t.Helper()
	var body bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&body).Encode(payload); err != nil {
			t.Fatalf("кодирование тела: %v", err)
		}
	}
	req, err := http.NewRequest(method, server.URL+path, &body)
	if err != nil {
		t.Fatalf("создание запроса: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if actor != "" {
		req.Header.Set("X-Actor", actor)
	}
	if commandID != "" {
		req.Header.Set("X-Command-Id", commandID)
	}
	resp, err := server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// decodeJSONResponse разбирает JSON-ответ.
func decodeJSONResponse(t *testing.T, resp *http.Response, target any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
}

// TestAdminAccessLifecycle проверяет сквозной сценарий доступа через HTTP:
// создание тенанта, положительное решение, отключение, отрицательное решение.
func TestAdminAccessLifecycle(t *testing.T) {
	server := newAdminServer(t)

	create := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "cmd-1", map[string]any{
		"tenant_id":    "tenant-1",
		"company_name": "ООО Ромашка",
		"description":  "Поставщик промышленного оборудования",
		"user_id":      "user-1",
		"login":        "client-1",
		"roles":        []string{string(domain.RoleClient)},
	})
	if create.StatusCode != http.StatusCreated {
		t.Fatalf("создание тенанта = %d, ожидался 201", create.StatusCode)
	}
	var created struct {
		TenantID string `json:"tenant_id"`
		Created  bool   `json:"created"`
	}
	decodeJSONResponse(t, create, &created)
	if !created.Created || created.TenantID != "tenant-1" {
		t.Fatalf("ответ создания = %+v", created)
	}

	// Повтор команды с тем же ключом не создаёт второй тенант.
	repeat := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "cmd-1", map[string]any{
		"tenant_id": "tenant-1", "company_name": "ООО Ромашка", "description": "Поставщик",
	})
	var repeated struct {
		Created bool `json:"created"`
	}
	decodeJSONResponse(t, repeat, &repeated)
	if repeated.Created {
		t.Fatal("повторная команда создала эффект")
	}

	// Положительное решение доступа для пользователя тенанта.
	access := apiJSON(t, server, http.MethodGet, "/api/tenants/tenant-1/access?actor=user-1", "", "", nil)
	var decision struct {
		Allowed bool `json:"allowed"`
	}
	decodeJSONResponse(t, access, &decision)
	if !decision.Allowed {
		t.Fatal("решение доступа должно быть положительным")
	}

	// Отключение тенанта.
	disable := apiJSON(t, server, http.MethodPost, "/api/tenants/tenant-1/disable", "admin-1", "cmd-2", nil)
	if disable.StatusCode != http.StatusOK {
		t.Fatalf("отключение = %d, ожидался 200", disable.StatusCode)
	}
	disable.Body.Close()

	// После отключения решение отрицательное.
	after := apiJSON(t, server, http.MethodGet, "/api/tenants/tenant-1/access?actor=user-1", "", "", nil)
	var afterDecision struct {
		Allowed bool `json:"allowed"`
	}
	decodeJSONResponse(t, after, &afterDecision)
	if afterDecision.Allowed {
		t.Fatal("отключённый тенант выдал положительное решение")
	}

	// Журнал аудита содержит создание и отключение.
	audit := apiJSON(t, server, http.MethodGet, "/api/tenants/tenant-1/audit", "", "", nil)
	var journal struct {
		Entries []struct {
			Action string `json:"action"`
		} `json:"entries"`
	}
	decodeJSONResponse(t, audit, &journal)
	actions := map[string]bool{}
	for _, entry := range journal.Entries {
		actions[entry.Action] = true
	}
	if !actions["tenant-created"] || !actions["tenant-disabled"] {
		t.Fatalf("журнал аудита = %+v", journal.Entries)
	}
}

// TestAdminForbidden проверяет, что актор без платформенного права получает 403.
func TestAdminForbidden(t *testing.T) {
	server := newAdminServer(t)
	resp := apiJSON(t, server, http.MethodPost, "/api/tenants", "stranger", "cmd-1", map[string]any{
		"tenant_id": "tenant-1", "company_name": "ООО", "description": "Описание",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("создание без права = %d, ожидался 403", resp.StatusCode)
	}
}

// TestAdminMissingCommandID проверяет, что команда без ключа идемпотентности
// отклоняется как 400.
func TestAdminMissingCommandID(t *testing.T) {
	server := newAdminServer(t)
	resp := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "", map[string]any{
		"tenant_id": "tenant-1", "company_name": "ООО", "description": "Описание",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("без command id = %d, ожидался 400", resp.StatusCode)
	}
}

// TestAdminAssignRevoke проверяет назначение и отзыв роли через HTTP.
func TestAdminAssignRevoke(t *testing.T) {
	server := newAdminServer(t)
	create := apiJSON(t, server, http.MethodPost, "/api/tenants", "admin-1", "cmd-1", map[string]any{
		"tenant_id": "tenant-1", "company_name": "ООО Ромашка", "description": "Описание",
		"user_id": "user-1", "login": "client-1", "roles": []string{string(domain.RoleClient)},
	})
	create.Body.Close()

	assign := apiJSON(t, server, http.MethodPost, "/api/tenants/tenant-1/roles/assign", "admin-1", "cmd-2", map[string]any{
		"user_id": "user-1", "role": string(domain.RoleAdmin),
	})
	if assign.StatusCode != http.StatusOK {
		t.Fatalf("назначение роли = %d, ожидался 200", assign.StatusCode)
	}
	assign.Body.Close()

	revoke := apiJSON(t, server, http.MethodPost, "/api/tenants/tenant-1/roles/revoke", "admin-1", "cmd-3", map[string]any{
		"user_id": "user-1", "role": string(domain.RoleClient),
	})
	if revoke.StatusCode != http.StatusOK {
		t.Fatalf("отзыв роли = %d, ожидался 200", revoke.StatusCode)
	}
	revoke.Body.Close()

	// Отзыв отсутствующей роли — конфликт состояния.
	again := apiJSON(t, server, http.MethodPost, "/api/tenants/tenant-1/roles/revoke", "admin-1", "cmd-4", map[string]any{
		"user_id": "user-1", "role": string(domain.RoleClient),
	})
	defer again.Body.Close()
	if again.StatusCode != http.StatusConflict {
		t.Fatalf("повторный отзыв = %d, ожидался 409", again.StatusCode)
	}
}
