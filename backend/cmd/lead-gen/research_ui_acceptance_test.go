package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	agentorchestration "lead-gen/backend/contexts/agent-orchestration"
	platformgovernance "lead-gen/backend/contexts/platform-governance"
	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/consoleauth"
	"lead-gen/backend/platform/observability/observ"
	"lead-gen/contracts/agent-orchestration/researchmission"
)

// Реальные PostgreSQL, решения доступа, HTTP-формы, Firecrawl и LLM.
// Тест включается отдельно: новый человеческий intent расходует не более20credits.
func TestResearchLiveUI(t *testing.T) {
	if os.Getenv("LEADGEN_RESEARCH_UI_LIVE") != "1" {
		t.Skip("живой UI и платные провайдеры требуют явного запуска")
	}
	for _, key := range []string{"LEADGEN_POSTGRES_TEST_DSN", "FIRECRAWL_API_KEY", "LLM_API_KEY", "LLM_MODEL"} {
		if os.Getenv(key) == "" {
			t.Fatalf("не задан %s", key)
		}
	}
	t.Chdir("../../..")
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, migration := range []struct{ owner, path string }{
		{requestfulfillment.ServiceID, "backend/contexts/request-fulfillment/migrations/green"},
		{platformgovernance.ServiceID, "backend/contexts/platform-governance/migrations"},
	} {
		if err = applyMigrations(pool, migration.path, migration.owner); err != nil {
			t.Fatal(err)
		}
	}
	if err = applyMigrations(pool, "backend/platform/consoleauth/migrations/green", "consoleauth"); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("ui-%d", time.Now().UnixNano())
	reuse := os.Getenv("LEADGEN_RESEARCH_UI_EXISTING_REQUEST")
	if reuse != "" {
		if !regexp.MustCompile(`^ui-[0-9]+$`).MatchString(reuse) {
			t.Fatal("некорректный ID сохранённой тестовой заявки")
		}
		id = reuse
	}
	tenant, other, operator := id+"-a", id+"-b", id+"-human"
	grants, err := platformgovernance.OperatorGrants("ui-setup-admin")
	if err != nil {
		t.Fatal(err)
	}
	platformConfig := platformgovernance.Config{Pool: pool, Grants: grants, Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer(platformgovernance.ServiceID)}
	platformService, err := platformgovernance.New(platformConfig)
	if err != nil {
		t.Fatal(err)
	}
	platform := platformService.Slice()
	platformServer := httptest.NewServer(platform.Server)
	defer platformServer.Close()
	for _, item := range []struct{ tenant, user string }{{tenant, operator}, {other, id + "-other-human"}} {
		payload := map[string]any{"tenant_id": item.tenant, "company_name": "Проверка исследования " + item.tenant, "description": "Тестовый заказчик", "user_id": item.user, "login": item.user, "roles": []string{"client"}}
		body, _ := json.Marshal(payload)
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, platformServer.URL+"/api/tenants", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Actor", "ui-setup-admin")
		request.Header.Set("X-Command-Id", item.tenant)
		response, e := platformServer.Client().Do(request)
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != http.StatusCreated && (reuse == "" || response.StatusCode != http.StatusOK) {
			t.Fatalf("tenant=%d %s", response.StatusCode, data)
		}
	}
	platformService, err = platformgovernance.New(platformConfig)
	if err != nil {
		t.Fatal(err)
	}
	platform = platformService.Slice()
	if allowed, e := platformgovernance.NewAccessDecision(platform).CanAct(ctx, operator, tenant); e != nil || !allowed {
		t.Fatalf("PG ACL после нового экземпляра: allowed=%v err=%v", allowed, e)
	}
	resources := newRuntimePoolRegistry()
	defer resources.closeAll()
	runtime, err := buildResearchRuntime(buildOptions{researchOnlyProvider: true, researchRedDSN: os.Getenv("LEADGEN_POSTGRES_TEST_DSN"), researchAccountID: "live-ui-validation", researchAccountCredits: 80, researchCountries: "RU,GB", researchMaxActions: 16, researchTimeout: 10 * time.Minute, researchContactFreshness: 7 * 24 * time.Hour, firecrawlKey: os.Getenv("FIRECRAWL_API_KEY"), firecrawlURL: os.Getenv("FIRECRAWL_BASE_URL"), firecrawlMaxCredits: 20, firecrawlCrawlInterval: time.Second, llmBaseURL: os.Getenv("LLM_BASE_URL"), llmModel: os.Getenv("LLM_MODEL"), llmKey: os.Getenv("LLM_API_KEY"), accessDecision: platformgovernance.NewAccessDecision(platform), runtimeDBResources: resources})
	if err != nil {
		t.Fatal(err)
	}
	runtime.requests.OnlyResearch = true
	persistence := requestfulfillment.NewPostgresPersistence(pool)
	requests, err := requestfulfillment.New(requestfulfillment.Config{Repository: persistence.Repository, UnitOfWork: persistence.UnitOfWork, Journal: persistence.Journal, DraftStore: persistence.DraftStore, Research: runtime.requests, ConsoleUser: operator, ConsolePassword: "local-ui-test-only", ConsoleStore: consoleauth.NewPostgresStore(pool), BasePath: "/intake", Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer(requestfulfillment.ServiceID)})
	if err != nil {
		t.Fatal(err)
	}
	// Повторное чтение терминальной заявки не запускает worker или внешние API.
	// Новый intent обрабатывают настоящие role runners, без ручных Step/Scan.
	if reuse == "" {
		agents, e := agentorchestration.New(agentorchestration.Config{Persistence: agentorchestration.NewPostgresPersistence(pool), Research: runtime.missions, Logger: observ.NewLoggerWithWriter(io.Discard), Metrics: observ.NewMetrics(), Tracer: observ.NewTracer(agentorchestration.ServiceID)})
		if e != nil {
			t.Fatal(e)
		}
		workerCtx, stopWorkers := context.WithCancel(ctx)
		workerDone := make(chan error, 2)
		for _, service := range []composition.Service{requests, agents} {
			runner, ok := service.RoleRunner(composition.RoleWorker)
			if !ok {
				t.Fatal("в составе нет обязательного worker")
			}
			go func() { workerDone <- runner.Run(workerCtx) }()
		}
		defer func() {
			stopWorkers()
			for range 2 {
				select {
				case e := <-workerDone:
					if e != nil {
						t.Errorf("worker: %v", e)
					}
				case <-time.After(15 * time.Second):
					t.Error("worker не остановился")
				}
			}
		}()
	}
	server := httptest.NewServer(requests.HTTPHandler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 3 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	perform := func(method, path, body, contentType string) (int, string, http.Header) {
		t.Helper()
		req, e := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		data, e := io.ReadAll(io.LimitReader(response.Body, 4<<20))
		if e != nil {
			t.Fatal(e)
		}
		return response.StatusCode, string(data), response.Header
	}
	post := func(path string, values url.Values) (int, string, http.Header) {
		return perform(http.MethodPost, path, values.Encode(), "application/x-www-form-urlencoded")
	}
	if reuse == "" {
		ready := false
		for attempt := 0; attempt < 50; attempt++ {
			status, _, _ := perform(http.MethodGet, "/intake/readyz", "", "")
			if status == http.StatusOK {
				ready = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !ready {
			t.Fatal("настоящие worker/БД не подтвердили готовность до одобрения")
		}
	}
	status, body, _ := post("/intake/login", url.Values{"username": {operator}, "password": {"local-ui-test-only"}})
	if status != http.StatusSeeOther {
		t.Fatalf("login=%d %s", status, body)
	}
	base := "/intake/requests/" + id
	var ownerState struct{ Status string }
	if reuse == "" {
		payload, _ := json.Marshal(map[string]any{"request_id": id, "offer_id": id + "-offer", "tenant_id": tenant, "actor": operator, "content": "Сервис автоматизации обработки заявок покупателей", "target": "Российские производители строительных материалов", "execution_purpose": "editorial"})
		status, body, _ = perform(http.MethodPost, "/intake/requests", string(payload), "application/json")
		if status != http.StatusCreated {
			t.Fatalf("submit=%d %s", status, body)
		}
		csrfPattern := regexp.MustCompile(`name="csrf" value="([^"]+)"`)
		status, body, _ = perform(http.MethodGet, base+"/validation?tenant_id="+tenant, "", "")
		token := csrfPattern.FindStringSubmatch(body)
		if status != http.StatusOK || len(token) != 2 {
			t.Fatalf("validation form=%d", status)
		}
		status, body, _ = post(base+"/validation/take", url.Values{"tenant_id": {tenant}, "csrf": {token[1]}})
		if status != http.StatusSeeOther {
			t.Fatalf("take=%d %s", status, body)
		}
		status, body, _ = perform(http.MethodGet, base+"/research?tenant_id="+tenant, "", "")
		token = csrfPattern.FindStringSubmatch(body)
		if status != http.StatusOK || len(token) != 2 || strings.Contains(body, `value="email-ready"`) {
			t.Fatalf("research form/readiness=%d", status)
		}
		// Старый путь отказал до изменения заявки, новый остаётся доступен.
		status, _, _ = post(base+"/validation/approve", url.Values{"tenant_id": {tenant}, "csrf": {token[1]}})
		if status != http.StatusConflict {
			t.Fatalf("legacy mode was admitted: %d", status)
		}
		status, body, _ = post(base+"/validation/approve", url.Values{"tenant_id": {tenant}, "csrf": {token[1]}, "research_mode": {"research"}, "research_quantity": {"2"}, "research_unit": {"company"}, "research_country": {"RU"}, "research_language_RU": {"ru"}})
		if status != http.StatusSeeOther {
			t.Fatalf("research approve=%d %s", status, body)
		}
		status, body, _ = perform(http.MethodGet, base+"?tenant_id="+tenant, "", "")
		if err = json.Unmarshal([]byte(body), &ownerState); err != nil {
			t.Fatal(err)
		}
		if status != http.StatusOK || ownerState.Status != "processing" {
			t.Fatalf("owner start processing=%d %s", status, body)
		}
	}
	scope := researchmission.Scope{TenantID: tenant, PurposeID: "editorial"}
	missions, err := runtime.missions.FindByIntent(ctx, scope, id)
	if err != nil || len(missions) != 1 {
		t.Fatalf("missions=%d err=%v", len(missions), err)
	}
	snapshot := missions[0]
	if reuse != "" && snapshot.CompletedAt == nil {
		t.Fatal("повторное чтение разрешено только для завершённой заявки; платные действия не запускаются")
	}
	revisionPattern := regexp.MustCompile(`data-revision="([0-9]+)"`)
	var previous int64
	for step := 0; step < 600; step++ {
		status, body, _ = perform(http.MethodGet, base+"/research/progress?tenant_id="+tenant, "", "")
		match := revisionPattern.FindStringSubmatch(body)
		if status != http.StatusOK || len(match) != 2 {
			allowed, accessErr := runtime.requests.Access.CanAct(ctx, operator, tenant)
			check, missionErr := runtime.missions.FindByIntent(ctx, scope, id)
			t.Logf("read diagnostics: allowed=%v accessErr=%v snapshots=%d missionErr=%v", allowed, accessErr, len(check), missionErr)
			if len(check) > 0 {
				t.Logf("scope tenant=%s purpose=%s intent=%s", check[0].Scope.TenantID, check[0].Scope.PurposeID, check[0].IntentID)
			}
			ownerCode, ownerBody, _ := perform(http.MethodGet, base+"?tenant_id="+tenant, "", "")
			var ownerInfo struct{ RequestID, TenantID, ExecutionPurpose, Status string }
			_ = json.Unmarshal([]byte(ownerBody), &ownerInfo)
			t.Logf("owner read status=%d view=%+v", ownerCode, ownerInfo)
			t.Fatalf("progress=%d %s", status, body)
		}
		revision, _ := strconv.ParseInt(match[1], 10, 64)
		if revision < previous {
			t.Fatal("UI откатился к старой версии")
		}
		previous = revision
		if snapshot.CompletedAt != nil {
			break
		}
		time.Sleep(time.Second)
		snapshot, err = runtime.missions.Get(ctx, scope, snapshot.MissionID)
		if err != nil {
			t.Fatalf("step=%d: %v", step, err)
		}
		t.Logf("UI step=%d revision=%d state=%s saved=%d accepted=%d", step, snapshot.Revision, snapshot.State, snapshot.Counters.Saved, snapshot.Counters.Accepted)
	}
	if snapshot.Counters.Saved < 1 || snapshot.CompletedAt == nil {
		t.Fatalf("живой UI результат неполон: state=%s saved=%d accepted=%d stop=%s", snapshot.State, snapshot.Counters.Saved, snapshot.Counters.Accepted, snapshot.StopReason)
	}
	expectedOwnerStatus := "failed"
	if snapshot.Outcome == "goal-reached" {
		if snapshot.Counters.Accepted < snapshot.Goal.Quantity {
			t.Fatal("goal-reached без подтверждённого количества")
		}
		expectedOwnerStatus = "fulfilled"
	}
	if snapshot.State == "cancelled" {
		expectedOwnerStatus = "cancelled"
	}
	for attempt := 0; attempt < 50; attempt++ {
		status, body, _ = perform(http.MethodGet, base+"?tenant_id="+tenant, "", "")
		if err = json.Unmarshal([]byte(body), &ownerState); err != nil {
			t.Fatal(err)
		}
		if ownerState.Status == expectedOwnerStatus {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if status != http.StatusOK || ownerState.Status != expectedOwnerStatus {
		t.Fatalf("owner completion=%d %s", status, body)
	}
	status, _, redirectHeader := perform(http.MethodGet, base+"/approval?tenant_id="+tenant, "", "")
	if status != http.StatusSeeOther || !strings.Contains(redirectHeader.Get("Location"), "/research?") {
		t.Fatalf("legacy detail redirect=%d", status)
	}
	for _, member := range snapshot.Members {
		status, body, _ = perform(http.MethodGet, base+"/research/candidates/"+url.PathEscape(member.EntityID)+"?tenant_id="+tenant, "", "")
		if status != http.StatusOK || !strings.Contains(body, "Источники") || !strings.Contains(body, "Достоверность") {
			t.Fatalf("dossier=%d", status)
		}
	}
	status, _, _ = perform(http.MethodGet, base+"/research?tenant_id="+other+"&actor=ui-setup-admin", "", "")
	if status != http.StatusForbidden {
		t.Fatalf("tenant bypass=%d", status)
	}
	status, body, _ = perform(http.MethodGet, base+"/research?tenant_id="+tenant, "", "")
	if status != http.StatusOK {
		t.Fatal(status)
	}
	directory := os.Getenv("LEADGEN_RESEARCH_UI_ARTIFACT_DIR")
	if directory == "" {
		directory = t.TempDir()
	}
	if err = os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(directory, "research-live.html"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if chromium, lookErr := exec.LookPath("chromium"); lookErr == nil {
		parsed, _ := url.Parse(server.URL)
		cookies := jar.Cookies(parsed)
		session := ""
		for _, cookie := range cookies {
			if cookie.Name == consoleauth.CookieName {
				session = cookie.Value
			}
		}
		config, _ := json.Marshal(map[string]string{"url": server.URL + base + "/research?tenant_id=" + tenant, "session": session, "chromium": chromium, "directory": directory, "dossier": server.URL + base + "/research/candidates/" + snapshot.Members[0].EntityID + "?tenant_id=" + tenant, "queue": server.URL + "/intake/validation?tenant_id=" + tenant, "execution": server.URL + "/intake/approvals?tenant_id=" + tenant})
		configPath := filepath.Join(t.TempDir(), "browser.json")
		if err = os.WriteFile(configPath, config, 0600); err != nil {
			t.Fatal(err)
		}
		output, e := exec.CommandContext(ctx, "node", "backend/cmd/lead-gen/testdata/research-ui-browser.cjs", configPath).CombinedOutput()
		if e != nil {
			t.Fatalf("browser: %v %s", e, output)
		}
	} else {
		t.Fatal("живой UI acceptance требует установленный Chromium")
	}
	t.Logf("UI acceptance: request=%s mission=%s state=%s outcome=%s stop=%s owner=%s saved=%d accepted=%d target=%d Firecrawl=%d unknown=%d; artifacts=%s", id, snapshot.MissionID, snapshot.State, snapshot.Outcome, snapshot.StopReason, ownerState.Status, snapshot.Counters.Saved, snapshot.Counters.Accepted, snapshot.Goal.Quantity, snapshot.Firecrawl.Actual, snapshot.Firecrawl.Unknown, directory)
}
