package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html"

	platformgovernance "lead-gen/backend/contexts/platform-governance"
	"lead-gen/backend/platform/composition"
	"lead-gen/backend/platform/observability/observ"
)

const (
	platformAuthAcceptanceOptIn = "LEADGEN_PLATFORM_AUTH_ACCEPTANCE"
	platformAuthAcceptanceDSN   = "LEADGEN_PLATFORM_AUTH_ACCEPTANCE_DSN"
	platformAuthAdminDB         = "leadgen_platform_auth_acceptance_admin"
	platformAuthAdminMarker     = "lead-gen-platform-auth-acceptance-admin-v1"
	platformAuthChildPrefix     = "leadgen_platform_auth_acceptance_"
	platformAuthChildMarker     = "lead-gen-platform-auth-acceptance-child-v1:"
)

// Этот тест намеренно требует явного opt-in и отдельной sacrificial БД. Он
// создаёт/удаляет только собственную помеченную дочернюю БД.
func TestPlatformConsoleAuthLivePostgres(t *testing.T) {
	if os.Getenv(platformAuthAcceptanceOptIn) != "1" {
		t.Skip("задайте LEADGEN_PLATFORM_AUTH_ACCEPTANCE=1 и отдельный disposable PostgreSQL 17 для live acceptance")
	}
	projectRoot := platformAuthProjectRoot(t)
	// В пакете нет параллельных тестов. t.Chdir восстанавливает cwd после
	// завершения теста, а production runner использует миграционные пути от корня.
	t.Chdir(projectRoot)
	resources := newRuntimePoolRegistry()
	fixture := newPlatformAuthAcceptanceDB(t, resources)
	password := randomAcceptanceSecret(t)
	passwordFile := filepath.Join(t.TempDir(), "console-password")
	if err := os.WriteFile(passwordFile, []byte(password), 0o400); err != nil {
		t.Fatal("создание внешнего password file")
	}
	if err := os.Chmod(passwordFile, 0o400); err != nil {
		t.Fatal("установка mode password file")
	}
	info, err := os.Stat(passwordFile)
	if err != nil {
		t.Fatal("stat password file")
	}
	if info.Mode().Perm() != 0o400 {
		t.Fatalf("password file mode = %04o, ожидался 0400", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		t.Fatalf("password file owner должен совпадать с runtime UID %d", os.Getuid())
	}
	loadedPassword, err := resolveConsolePassword("acceptance-operator", passwordFile, func(string) (string, bool) { return "", false })
	if err != nil || loadedPassword != password {
		t.Fatalf("resolveConsolePassword не прочитал owner-only file для текущего runtime UID: err=%v", err)
	}

	var logs strings.Builder
	logger := observ.NewLoggerWithWriter(&logs)
	service, err := buildPlatformGovernance(buildOptions{
		apiAddr:            "127.0.0.1:0",
		supportGreenDSN:    fixture.childDSN,
		runtimeDBResources: resources,
		consoleUser:        "acceptance-operator",
		consolePassword:    loadedPassword,
		logger:             logger,
		metrics:            observ.NewMetrics(),
	}, observ.NewTracer(platformgovernance.ServiceID), nil)
	if err != nil {
		t.Fatalf("production buildPlatformGovernance: %v", err)
	}
	runner, ok := service.RoleRunner(composition.RoleAPI)
	if !ok || runner == nil {
		t.Fatal("production platform-governance API role is not registered")
	}
	server := httptest.NewServer(observ.Middleware(logger, observ.NewMetrics(), observ.NewTracer(platformgovernance.ServiceID), platformgovernance.ServiceID)(service.(*platformgovernance.Service).Slice().Server))
	t.Cleanup(server.Close)
	baseURL := server.URL

	anon := newAcceptanceClient(t, false)
	response := acceptanceRequest(t, anon, http.MethodGet, baseURL+"/console", nil, nil)
	if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/console/login" {
		t.Fatalf("anonymous GET /console: status=%d location=%q", response.StatusCode, response.Header.Get("Location"))
	}
	consoleClient := newAcceptanceClient(t, false)
	login := acceptanceRequest(t, consoleClient, http.MethodPost, baseURL+"/console/login", url.Values{
		"username": {"acceptance-operator"}, "password": {loadedPassword},
	}, nil)
	if login.StatusCode != http.StatusSeeOther || login.Header.Get("Location") != "/console/registrations" {
		t.Fatalf("console login: status=%d location=%q", login.StatusCode, login.Header.Get("Location"))
	}
	consoleSession := cookieValue(t, consoleClient, baseURL, "session")
	if consoleSession == "" {
		t.Fatal("successful console login did not issue session cookie")
	}
	registrationPage := acceptanceRequest(t, consoleClient, http.MethodGet, baseURL+"/console/registrations", nil, nil)
	if registrationPage.StatusCode != http.StatusOK || !strings.Contains(registrationPage.body, "Регистрации") {
		t.Fatalf("registration page: status=%d", registrationPage.StatusCode)
	}
	approved := createPendingRegistration(t, anon, fixture.childPool, baseURL, "approve")
	rejected := createPendingRegistration(t, anon, fixture.childPool, baseURL, "reject")
	registrationPage = acceptanceRequest(t, consoleClient, http.MethodGet, baseURL+"/console/registrations", nil, nil)
	approveAction := "/console/registrations/" + approved.tenantID + "/approve"
	csrf := registrationFormCSRF(t, registrationPage.body, approveAction)
	if csrf == "" {
		t.Fatal("rendered approve form has no session CSRF value")
	}
	approved.commandID = formCommandID(t, registrationPage.body, approved.tenantID, "approve")
	rejected.commandID = formCommandID(t, registrationPage.body, rejected.tenantID, "reject")
	if approved.commandID == "" || rejected.commandID == "" {
		t.Fatal("rendered registration approve/reject forms lack command IDs")
	}
	assertPendingRegistration(t, fixture.childPool, approved.tenantID)
	assertPendingRegistration(t, fixture.childPool, rejected.tenantID)

	for _, csrfValue := range []string{"", "incorrect-csrf"} {
		before := registrationDBSnapshot(t, fixture.childPool, approved.tenantID)
		resp := acceptanceRequest(t, consoleClient, http.MethodPost, baseURL+"/console/registrations/"+url.PathEscape(approved.tenantID)+"/approve", url.Values{
			"csrf": {csrfValue}, "actor": {"forged-body-actor"}, "command_id": {approved.commandID},
		}, nil)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("approve with absent/wrong CSRF returned %d, want 403", resp.StatusCode)
		}
		after := registrationDBSnapshot(t, fixture.childPool, approved.tenantID)
		if before != after {
			t.Fatalf("rejected CSRF request changed persistent registration/audit/outbox: before=%+v after=%+v", before, after)
		}
	}

	approveResponse := acceptanceRequest(t, consoleClient, http.MethodPost, baseURL+"/console/registrations/"+url.PathEscape(approved.tenantID)+"/approve", url.Values{
		"csrf": {csrf}, "actor": {"forged-body-actor"}, "command_id": {approved.commandID},
	}, nil)
	if approveResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid approval returned %d, want 303", approveResponse.StatusCode)
	}
	assertRegistrationDecision(t, fixture.childPool, approved.tenantID, "active", "acceptance-operator", "registration-approved")

	// Для отклонения повторно читаем реальную форму с текущим CSRF-токеном.
	pageAfterApproval := acceptanceRequest(t, consoleClient, http.MethodGet, baseURL+"/console/registrations", nil, nil)
	rejectCommandID := formCommandID(t, pageAfterApproval.body, rejected.tenantID, "reject")
	if rejectCommandID == "" {
		t.Fatal("reject form for real pending registration not found")
	}
	rejectCSRF := registrationFormCSRF(t, pageAfterApproval.body, "/console/registrations/"+rejected.tenantID+"/reject")
	rejectResponse := acceptanceRequest(t, consoleClient, http.MethodPost, baseURL+"/console/registrations/"+url.PathEscape(rejected.tenantID)+"/reject", url.Values{
		"csrf": {rejectCSRF}, "actor": {"forged-body-actor"}, "command_id": {rejectCommandID}, "reason": {"acceptance rejection"},
	}, nil)
	if rejectResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("valid rejection returned %d, want 303", rejectResponse.StatusCode)
	}
	assertRegistrationDecision(t, fixture.childPool, rejected.tenantID, "rejected", "acceptance-operator", "registration-rejected")

	publicLogin := acceptanceRequest(t, anon, http.MethodGet, baseURL+"/login", nil, nil)
	if publicLogin.StatusCode != http.StatusOK {
		t.Fatalf("anonymous GET /login returned %d, want 200", publicLogin.StatusCode)
	}
	portalLogin := acceptanceRequest(t, anon, http.MethodPost, baseURL+"/login", url.Values{
		"email": {approved.email}, "password": {approved.password},
	}, nil)
	if portalLogin.StatusCode != http.StatusSeeOther {
		t.Fatalf("actual client login returned %d, want 303", portalLogin.StatusCode)
	}
	portalSession := cookieValue(t, anon, baseURL, "portal_session")
	if portalSession == "" || portalSession == consoleSession {
		t.Fatal("portal session cookie missing or shared with console session")
	}
	if got := cookieValue(t, anon, baseURL, "session"); got != "" {
		t.Fatal("client login created an operator console session")
	}
	profile := acceptanceRequest(t, anon, http.MethodGet, baseURL+"/profile", nil, nil)
	if profile.StatusCode != http.StatusOK {
		t.Fatalf("authenticated client GET /profile returned %d, want 200", profile.StatusCode)
	}
	if got := cookieValue(t, consoleClient, baseURL, "portal_session"); got != "" {
		t.Fatal("console login created a client portal session")
	}

	logText := logs.String()
	secrets := []string{
		password, consoleSession, portalSession, csrf, rejectCSRF, approved.password, rejected.password,
		"incorrect-csrf", "acceptance rejection", approved.email, rejected.email,
		"Acceptance approve", "Acceptance reject", "Synthetic acceptance registration",
		"company-approve@example.invalid", "company-reject@example.invalid", "forged-body-actor",
	}
	secrets = append(secrets, acceptanceCookieValues(t, consoleClient, baseURL)...)
	secrets = append(secrets, acceptanceCookieValues(t, anon, baseURL)...)
	for _, secret := range secrets {
		if secret != "" && strings.Contains(logText, secret) {
			t.Fatal("server logs contain sensitive acceptance input")
		}
	}
	assertNoUnexpectedAcceptanceLogs(t, logText)
}

func platformAuthProjectRoot(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("не удалось определить путь acceptance test file")
	}
	for dir := filepath.Dir(testFile); ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "openspec", "config.yaml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("не найден корень проекта по marker openspec/config.yaml")
		}
	}
}

func assertNoUnexpectedAcceptanceLogs(t *testing.T, logText string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logText), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal("не удалось проверить структурированную HTTP-журнализацию")
		}
		if entry["level"] == "ERROR" {
			t.Fatal("в логах acceptance обнаружена ошибка сервера")
		}
		if status, ok := entry["status"].(float64); ok && status >= 500 {
			t.Fatalf("HTTP лог содержит неожиданный 5xx статус: %.0f", status)
		}
	}
}

type platformAuthAcceptanceFixture struct {
	mu          sync.Mutex
	parent      acceptanceAdminPool
	childPool   *pgxpool.Pool
	childCloser interface{ Close() }
	childDSN    string
	database    string
	databaseOID uint32
	ownerOID    uint32
	created     bool
	cleanupDone bool
	resources   *runtimePoolRegistry
}

type acceptanceAdminPool interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Close()
}

func newPlatformAuthAcceptanceDB(t *testing.T, resources *runtimePoolRegistry) *platformAuthAcceptanceFixture {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(platformAuthAcceptanceDSN))
	if dsn == "" {
		t.Fatal("LEADGEN_PLATFORM_AUTH_ACCEPTANCE=1 требует отдельный LEADGEN_PLATFORM_AUTH_ACCEPTANCE_DSN")
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil || adminConfig.Database != platformAuthAdminDB {
		t.Fatal("acceptance DSN должен указывать на выделенную sacrificial admin database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parent, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal("подключение к sacrificial acceptance database")
	}
	if err := parent.Ping(ctx); err != nil {
		parent.Close()
		t.Fatal("sacrificial acceptance database недоступна")
	}
	var marker string
	var createdb bool
	var version int
	if err := parent.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()`).Scan(&marker); err != nil || marker != platformAuthAdminMarker {
		parent.Close()
		t.Fatal("marker sacrificial acceptance database не совпал; создание запрещено")
	}
	if err := parent.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&createdb); err != nil || !createdb {
		parent.Close()
		t.Fatal("тестовая роль должна иметь CREATEDB в выделенном test-кластере")
	}
	if err := parent.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil || version < 170000 || version >= 180000 {
		parent.Close()
		t.Fatalf("live acceptance требует PostgreSQL 17 (server_version_num=%d)", version)
	}
	databaseToken := make([]byte, 12)
	if _, err := rand.Read(databaseToken); err != nil {
		parent.Close()
		t.Fatal("создание уникального токена child database")
	}
	database := platformAuthChildPrefix + hex.EncodeToString(databaseToken)
	if _, err := parent.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{database}.Sanitize()); err != nil {
		parent.Close()
		t.Fatalf("CREATE DATABASE завершился ошибкой; автоудаление не разрешено без подтверждения успешного CREATE. Проверьте sacrificial кластер вручную: имя %q, ожидаемый marker %q", database, platformAuthChildMarker+database)
	}
	fixture := &platformAuthAcceptanceFixture{parent: parent, database: database, created: true, resources: resources}
	t.Cleanup(func() { fixture.cleanup(t) })
	if err := fixture.captureDatabaseIdentity(ctx); err != nil {
		t.Fatalf("не удалось подтвердить создание child database; cleanup оставит её и сообщит данные для ручной проверки: %v", err)
	}
	if err := fixture.ensureMarker(ctx); err != nil {
		t.Fatalf("не удалось установить/проверить marker child database; cleanup выполнит только доказуемо безопасное удаление, иначе сообщит имя базы и marker: %v", err)
	}
	childConfig := adminConfig.Copy()
	childConfig.Database = database
	childDSN := childConfig.ConnString()
	childPool, err := pgxpool.New(ctx, childDSN)
	if err != nil {
		t.Fatal("создание пула изолированной child database")
	}
	fixture.childDSN = childDSN
	fixture.childPool = childPool
	fixture.childCloser = childPool
	if err := childPool.Ping(ctx); err != nil {
		t.Fatal("изолированная child database недоступна")
	}
	return fixture
}

func (f *platformAuthAcceptanceFixture) captureDatabaseIdentity(ctx context.Context) error {
	return f.parent.QueryRow(ctx, `SELECT oid, datdba FROM pg_database WHERE datname=$1`, f.database).Scan(&f.databaseOID, &f.ownerOID)
}

func (f *platformAuthAcceptanceFixture) ensureMarker(ctx context.Context) error {
	marker := platformAuthChildMarker + f.database
	for attempt := 0; attempt < 2; attempt++ {
		if err := f.verifyIdentity(ctx, true); err != nil {
			return err
		}
		var existing string
		err := f.parent.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, f.database).Scan(&existing)
		if err != nil {
			return err
		}
		if existing == marker {
			return nil
		}
		if existing != "" {
			return fmt.Errorf("database marker differs")
		}
		if _, err := f.parent.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{f.database}.Sanitize()+" IS '"+marker+"'"); err == nil {
			continue
		}
		// COMMENT может завершиться ошибкой после применения; следующий проход
		// повторно читает marker и либо принимает его, либо повторяет установку.
	}
	if err := f.verifyIdentity(ctx, true); err != nil {
		return err
	}
	var markerNow string
	if err := f.parent.QueryRow(ctx, `SELECT COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, f.database).Scan(&markerNow); err != nil {
		return err
	}
	if markerNow != marker {
		return fmt.Errorf("marker не подтвердился")
	}
	return nil
}

func (f *platformAuthAcceptanceFixture) verifyIdentity(ctx context.Context, allowMissingMarker bool) error {
	var oid, ownerOID uint32
	var marker string
	err := f.parent.QueryRow(ctx, `SELECT oid, datdba, COALESCE(shobj_description(oid,'pg_database'),'') FROM pg_database WHERE datname=$1`, f.database).Scan(&oid, &ownerOID, &marker)
	if err != nil {
		return err
	}
	if !f.created || !strings.HasPrefix(f.database, platformAuthChildPrefix) || oid != f.databaseOID || ownerOID != f.ownerOID {
		return fmt.Errorf("identity child database не совпала")
	}
	wantMarker := platformAuthChildMarker + f.database
	if marker != wantMarker && !(allowMissingMarker && marker == "") {
		return fmt.Errorf("marker child database не совпал")
	}
	return nil
}

func (f *platformAuthAcceptanceFixture) cleanup(t *testing.T) {
	t.Helper()
	if err := f.cleanupOnce(); err != nil {
		t.Errorf("%v", err)
	}
}

func (f *platformAuthAcceptanceFixture) cleanupOnce() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cleanupDone {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	manual := fmt.Sprintf("БД %q; ожидаемый marker %q", f.database, platformAuthChildMarker+f.database)
	failClosed := func(reason string) error {
		return fmt.Errorf("cleanup child database запрещён: %s. Возможна ручная проверка sacrificial БД: %s", reason, manual)
	}
	if !f.created || f.database == "" || f.databaseOID == 0 || f.ownerOID == 0 {
		return failClosed("нет полного доказательства CREATE/identity")
	}
	// Close синхронно дренирует production pools; сначала убираем все их
	// подключения, затем закрываем fixture pool и проверяем PostgreSQL перед DROP.
	if f.resources != nil {
		f.resources.closeAll()
		f.resources.mu.Lock()
		registryClosed := f.resources.closed && len(f.resources.pools) == 0 && len(f.resources.servicePool) == 0
		f.resources.mu.Unlock()
		if !registryClosed {
			f.closeChildPool()
			return failClosed("runtime pool registry не подтвердил закрытие")
		}
	}
	f.closeChildPool()
	var openConnections int
	if err := f.parent.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=$1`, f.database).Scan(&openConnections); err != nil || openConnections != 0 {
		return failClosed(fmt.Sprintf("соединения child database остаются (count=%d, err=%v)", openConnections, err))
	}
	if err := f.verifyIdentity(ctx, true); err != nil {
		return failClosed(err.Error())
	}
	if _, err := f.parent.Exec(ctx, "DROP DATABASE "+pgx.Identifier{f.database}.Sanitize()+" WITH (FORCE)"); err != nil {
		return fmt.Errorf("не удалось удалить child database; parent connection сохранён для retry: %s", manual)
	}
	f.parent.Close()
	f.cleanupDone = true
	return nil
}

func (f *platformAuthAcceptanceFixture) closeChildPool() {
	if f.childCloser != nil {
		f.childCloser.Close()
		return
	}
	if f.childPool != nil {
		f.childPool.Close()
	}
}

type acceptanceHTTPResponse struct {
	StatusCode int
	Header     http.Header
	body       string
}

func newAcceptanceClient(t *testing.T, redirect bool) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal("создание cookie jar")
	}
	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	if !redirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return client
}

func acceptanceRequest(t *testing.T, client *http.Client, method, target string, form url.Values, extra http.Header) acceptanceHTTPResponse {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, target, body)
	if err != nil {
		t.Fatal("сборка HTTP acceptance request")
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for name, values := range extra {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("HTTP acceptance request %s %s failed", method, req.URL.Path)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("HTTP response body read for %s", req.URL.Path)
	}
	if resp.StatusCode >= http.StatusInternalServerError {
		t.Fatalf("HTTP acceptance %s %s вернул неожиданный 5xx", method, req.URL.Path)
	}
	return acceptanceHTTPResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone(), body: string(data)}
}

type acceptanceRegistration struct{ tenantID, commandID, email, password string }

func createPendingRegistration(t *testing.T, client *http.Client, pool *pgxpool.Pool, baseURL, suffix string) acceptanceRegistration {
	t.Helper()
	email := "operator-" + suffix + "@example.invalid"
	portalPassword := randomAcceptanceSecret(t)
	form := url.Values{
		"company_name": {"Acceptance " + suffix}, "description": {"Synthetic acceptance registration"},
		"email": {email}, "password": {portalPassword},
		"inn":           {"99100000000" + map[string]string{"approve": "1", "reject": "2"}[suffix]},
		"company_email": {"company-" + suffix + "@example.invalid"},
	}
	response := acceptanceRequest(t, client, http.MethodPost, baseURL+"/register", form, nil)
	if response.StatusCode != http.StatusOK || !strings.Contains(response.body, "Заявка отправлена") {
		t.Fatalf("actual client registration for %s failed: HTTP %d", suffix, response.StatusCode)
	}
	var tenantID string
	if err := pool.QueryRow(context.Background(), `SELECT id FROM "platform-governance".tenant WHERE inn=$1`, form.Get("inn")).Scan(&tenantID); err != nil {
		t.Fatalf("registration wasn't persisted by production HTTP route")
	}
	return acceptanceRegistration{tenantID: tenantID, email: email, password: portalPassword}
}

func formCommandID(t *testing.T, html, tenantID, decision string) string {
	t.Helper()
	pattern := `(?s)<form[^>]*action="/console/registrations/` + regexp.QuoteMeta(tenantID) + `/` + regexp.QuoteMeta(decision) + `".*?name="command_id" value="([^"]+)"`
	return acceptanceHTMLValue(t, html, pattern)
}

func assertPendingRegistration(t *testing.T, pool *pgxpool.Pool, tenantID string) {
	t.Helper()
	var state string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM "platform-governance".tenant WHERE id=$1`, tenantID).Scan(&state); err != nil || state != "pending" {
		t.Fatalf("registration state before decision = %q, err=%v; want pending", state, err)
	}
}

type registrationSnapshot struct {
	state                    string
	audit, outbox, commandID int
}

func registrationDBSnapshot(t *testing.T, pool *pgxpool.Pool, tenantID string) registrationSnapshot {
	t.Helper()
	var snapshot registrationSnapshot
	if err := pool.QueryRow(context.Background(), `SELECT state FROM "platform-governance".tenant WHERE id=$1`, tenantID).Scan(&snapshot.state); err != nil {
		t.Fatal("read registration state")
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "platform-governance".audit_record WHERE tenant_id=$1`, tenantID).Scan(&snapshot.audit); err != nil {
		t.Fatal("read registration audit")
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "platform-governance".outbox_message`).Scan(&snapshot.outbox); err != nil {
		t.Fatal("read registration outbox")
	}
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "platform-governance".command_key WHERE tenant_id=$1`, tenantID).Scan(&snapshot.commandID); err != nil {
		t.Fatal("read registration command keys")
	}
	return snapshot
}

func assertRegistrationDecision(t *testing.T, pool *pgxpool.Pool, tenantID, state, actor, auditAction string) {
	t.Helper()
	var actualState string
	if err := pool.QueryRow(context.Background(), `SELECT state FROM "platform-governance".tenant WHERE id=$1`, tenantID).Scan(&actualState); err != nil || actualState != state {
		t.Fatalf("registration state = %q, err=%v; want %q", actualState, err, state)
	}
	var audits int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "platform-governance".audit_record WHERE tenant_id=$1 AND actor=$2 AND action=$3`, tenantID, actor, auditAction).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit records for decision = %d, err=%v; want 1", audits, err)
	}
	eventChange := map[string]string{
		"registration-approved": "tenant-registered",
		"registration-rejected": "tenant-registration-rejected",
	}[auditAction]
	var decisions int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM "platform-governance".outbox_message WHERE tenant_id=$1 AND contract='tenant-access-changed' AND payload->>'Change'=$2`, tenantID, eventChange).Scan(&decisions); err != nil || decisions != 1 {
		t.Fatalf("decision outbox events for registration = %d, err=%v; want one %q event", decisions, err, eventChange)
	}
}

func registrationFormCSRF(t *testing.T, source, action string) string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatal("не удалось разобрать HTML формы регистраций")
	}
	var find func(*html.Node) string
	find = func(node *html.Node) string {
		if node.Type == html.ElementNode && node.Data == "form" {
			formAction := ""
			for _, attr := range node.Attr {
				if attr.Key == "action" {
					formAction = attr.Val
				}
			}
			if (action == "" && strings.HasPrefix(formAction, "/console/registrations/")) || (action != "" && formAction == action) {
				var csrf func(*html.Node) string
				csrf = func(child *html.Node) string {
					if child.Type == html.ElementNode && child.Data == "input" {
						name, value := "", ""
						for _, attr := range child.Attr {
							switch attr.Key {
							case "name":
								name = attr.Val
							case "value":
								value = attr.Val
							}
						}
						if name == "csrf" {
							return value
						}
					}
					for child := child.FirstChild; child != nil; child = child.NextSibling {
						if value := csrf(child); value != "" {
							return value
						}
					}
					return ""
				}
				return csrf(node)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if value := find(child); value != "" {
				return value
			}
		}
		return ""
	}
	return find(doc)
}

func TestRegistrationFormCSRFExtractsTokenFromRequestedRenderedForm(t *testing.T) {
	page := `<html><body>
<form method="post" action="/console/registrations/tenant-approve/approve"><input name="csrf" value="approve-token"></form>
<form method="post" action="/console/registrations/tenant-reject/reject"><input name="csrf" value="reject-token"></form>
</body></html>`
	if got := registrationFormCSRF(t, page, "/console/registrations/tenant-approve/approve"); got != "approve-token" {
		t.Fatalf("approve form CSRF = %q, want token from exact approve action", got)
	}
	if got := registrationFormCSRF(t, page, "/console/registrations/tenant-reject/reject"); got != "reject-token" {
		t.Fatalf("reject form CSRF = %q, want token from exact reject action", got)
	}
}

func acceptanceHTMLValue(t *testing.T, html, expression string) string {
	t.Helper()
	re := regexp.MustCompile(expression)
	match := re.FindStringSubmatch(html)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func cookieValue(t *testing.T, client *http.Client, baseURL, name string) string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal("parse acceptance server URL")
	}
	for _, cookie := range client.Jar.Cookies(u) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func acceptanceCookieValues(t *testing.T, client *http.Client, baseURL string) []string {
	t.Helper()
	u, err := url.Parse(baseURL)
	if err != nil {
		t.Fatal("parse acceptance server URL")
	}
	cookies := client.Jar.Cookies(u)
	values := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		values = append(values, cookie.Value)
	}
	return values
}

func randomAcceptanceSecret(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal("generate in-memory acceptance credential")
	}
	return hex.EncodeToString(bytes)
}

type acceptanceFakePool struct {
	marker       string
	databaseOID  uint32
	ownerOID     uint32
	commentFails int
	dropFails    int
	connections  int
	dropCalls    int
	closed       int
}

func (p *acceptanceFakePool) QueryRow(_ context.Context, query string, _ ...any) pgx.Row {
	switch {
	case strings.Contains(query, "pg_stat_activity"):
		return acceptanceFakeRow{values: []any{p.connections}}
	case strings.Contains(query, "SELECT oid, datdba, COALESCE"):
		return acceptanceFakeRow{values: []any{p.databaseOID, p.ownerOID, p.marker}}
	case strings.Contains(query, "SELECT COALESCE(shobj_description"):
		return acceptanceFakeRow{values: []any{p.marker}}
	default:
		return acceptanceFakeRow{err: fmt.Errorf("unexpected fixture query")}
	}
}

func (p *acceptanceFakePool) Exec(_ context.Context, query string, _ ...any) (pgconn.CommandTag, error) {
	if strings.HasPrefix(query, "COMMENT ON DATABASE") {
		p.marker = platformAuthChildMarker + platformAuthChildPrefix + "unit"
		if p.commentFails > 0 {
			p.commentFails--
			return pgconn.CommandTag{}, fmt.Errorf("simulated COMMENT response error")
		}
		return pgconn.CommandTag{}, nil
	}
	if strings.HasPrefix(query, "DROP DATABASE") {
		p.dropCalls++
		if p.dropFails > 0 {
			p.dropFails--
			return pgconn.CommandTag{}, fmt.Errorf("simulated transient DROP error")
		}
		return pgconn.CommandTag{}, nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected fixture command")
}

func (p *acceptanceFakePool) Close() { p.closed++ }

type acceptanceFakeRow struct {
	values []any
	err    error
}

func (r acceptanceFakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if len(dest) != len(r.values) {
		return fmt.Errorf("unexpected scan arity")
	}
	for i := range dest {
		v := reflect.ValueOf(dest[i])
		if v.Kind() != reflect.Pointer || !v.Elem().CanSet() {
			return fmt.Errorf("invalid scan destination")
		}
		value := reflect.ValueOf(r.values[i])
		if !value.Type().AssignableTo(v.Elem().Type()) {
			return fmt.Errorf("incompatible fake scan value")
		}
		v.Elem().Set(value)
	}
	return nil
}

func newAcceptanceFixtureForTest(pool *acceptanceFakePool) *platformAuthAcceptanceFixture {
	name := platformAuthChildPrefix + "unit"
	pool.databaseOID, pool.ownerOID = 42, 7
	return &platformAuthAcceptanceFixture{
		parent: pool, database: name, databaseOID: 42, ownerOID: 7, created: true,
	}
}

func TestPlatformAuthAcceptanceMarkerCommentResponseErrorIsRechecked(t *testing.T) {
	pool := &acceptanceFakePool{commentFails: 1}
	fixture := newAcceptanceFixtureForTest(pool)
	if err := fixture.ensureMarker(context.Background()); err != nil {
		t.Fatalf("marker should be confirmed after COMMENT response error: %v", err)
	}
	if pool.marker != platformAuthChildMarker+fixture.database {
		t.Fatal("test fixture did not set marker")
	}
}

func TestPlatformAuthAcceptanceCleanupFailsClosedOnForeignMarker(t *testing.T) {
	pool := &acceptanceFakePool{marker: "foreign-marker"}
	fixture := newAcceptanceFixtureForTest(pool)
	if err := fixture.cleanupOnce(); err == nil {
		t.Fatal("foreign marker should fail closed")
	}
	if pool.dropCalls != 0 || pool.closed != 0 {
		t.Fatalf("foreign marker must prevent DROP and preserve parent connection: drops=%d closes=%d", pool.dropCalls, pool.closed)
	}
}

func TestPlatformAuthAcceptanceCleanupFailsClosedOnOwnerMismatch(t *testing.T) {
	pool := &acceptanceFakePool{marker: platformAuthChildMarker + platformAuthChildPrefix + "unit"}
	fixture := newAcceptanceFixtureForTest(pool)
	pool.ownerOID++
	if err := fixture.cleanupOnce(); err == nil {
		t.Fatal("owner mismatch should fail closed")
	}
	if pool.dropCalls != 0 || pool.closed != 0 {
		t.Fatal("owner mismatch must prevent DROP and preserve parent connection")
	}
}

func TestPlatformAuthAcceptanceCleanupFailsClosedOnOIDMismatch(t *testing.T) {
	pool := &acceptanceFakePool{marker: platformAuthChildMarker + platformAuthChildPrefix + "unit"}
	fixture := newAcceptanceFixtureForTest(pool)
	pool.databaseOID++
	if err := fixture.cleanupOnce(); err == nil {
		t.Fatal("database OID mismatch should fail closed")
	}
	if pool.dropCalls != 0 || pool.closed != 0 {
		t.Fatal("database OID mismatch must prevent DROP and preserve parent connection")
	}
}

func TestPlatformAuthAcceptanceCleanupRetriesDropAndIsIdempotent(t *testing.T) {
	pool := &acceptanceFakePool{dropFails: 1}
	fixture := newAcceptanceFixtureForTest(pool)
	if err := fixture.cleanupOnce(); err == nil {
		t.Fatal("transient DROP failure should be returned")
	}
	if pool.closed != 0 || fixture.cleanupDone {
		t.Fatal("failed DROP must retain parent connection and permit retry")
	}
	if err := fixture.cleanupOnce(); err != nil {
		t.Fatalf("cleanup retry: %v", err)
	}
	if pool.dropCalls != 2 || pool.closed != 1 || !fixture.cleanupDone {
		t.Fatalf("retry did not finish cleanup exactly once: drops=%d closes=%d done=%v", pool.dropCalls, pool.closed, fixture.cleanupDone)
	}
	if err := fixture.cleanupOnce(); err != nil {
		t.Fatalf("repeated cleanup: %v", err)
	}
	if pool.dropCalls != 2 || pool.closed != 1 {
		t.Fatal("repeated successful cleanup must be a no-op")
	}
}

func TestPlatformAuthAcceptanceCleanupRequiresCreationProof(t *testing.T) {
	pool := &acceptanceFakePool{marker: platformAuthChildMarker + platformAuthChildPrefix + "unit"}
	fixture := newAcceptanceFixtureForTest(pool)
	fixture.created = false
	if err := fixture.cleanupOnce(); err == nil {
		t.Fatal("missing creation proof should fail closed")
	}
	if pool.dropCalls != 0 || pool.closed != 0 {
		t.Fatal("fixture must not drop a database without its CREATE proof")
	}
}
