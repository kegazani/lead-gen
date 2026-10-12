package httpapi_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	requestfulfillment "lead-gen/backend/contexts/request-fulfillment"
	"lead-gen/backend/contexts/request-fulfillment/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/platform/migrate"
)

const (
	g15AdminDB     = "leadgen_provision_test_admin"
	g15AdminMarker = "lead-gen-provision-test-admin-v1"
	g15ChildPrefix = "leadgen_g15_bridge_"
	g15ChildMarker = "lead-gen-g15-bridge-child-v1:"
)

// TestG15BridgeV1HandlersAndPostgresAfter0001To0010 exercises real request
// handlers and PostgreSQL storage against a synthetic G1 schema created from
// 0001-0004, then upgraded through the exact G1.5 set 0001-0010. The workflow
// port is a test-only bridge double; Temporal history replay has separate
// acceptance and is not claimed by this test.
func TestG15BridgeV1HandlersAndPostgresAfter0001To0010(t *testing.T) {
	pool, cleanup := newSyntheticG15Database(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	migrationDir := filepath.Join("..", "..", "..", "migrations", "green")
	migrations, err := migrate.Load(migrationDir, migrate.Schema("request-fulfillment"))
	if err != nil || len(migrations) != 10 {
		t.Fatalf("pinned request migrations: count=%d err=%v", len(migrations), err)
	}
	for _, migration := range migrations[:4] {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("synthetic G1 apply %s: %v", migration.Name, err)
		}
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE "request-fulfillment".schema_migrations (version text PRIMARY KEY, applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal("create synthetic G1 migration ledger:", err)
	}
	for _, migration := range migrations[:4] {
		if _, err := pool.Exec(ctx, `INSERT INTO "request-fulfillment".schema_migrations(version) VALUES($1)`, migration.Name); err != nil {
			t.Fatalf("record synthetic G1 migration %s: %v", migration.Name, err)
		}
	}

	legacyID := "g15-legacy-request"
	_, oldServer, _ := newPostgresV1Slice(t, pool)
	legacyCreate := do(t, oldServer, http.MethodPost, "/requests", map[string]any{
		"request_id": legacyID, "offer_id": "g15-legacy-offer", "tenant_id": "g15-tenant",
		"actor": "legacy-client", "content": "Synthetic G1 request", "target": "IT",
	})
	if legacyCreate.StatusCode != http.StatusCreated {
		t.Fatalf("pre-upgrade v1 request create = %d: %s", legacyCreate.StatusCode, readBody(t, legacyCreate))
	}
	legacyCreate.Body.Close()
	oldServer.Close()

	if err := migrate.Apply(ctx, pool, migrationDir, migrate.Schema("request-fulfillment")); err != nil {
		t.Fatalf("upgrade synthetic G1 schema through 0010: %v", err)
	}
	versions, err := migrate.Load(migrationDir, migrate.Schema("request-fulfillment"))
	if err != nil || len(versions) != 10 {
		t.Fatalf("exact G1.5 verification set unavailable: migrations=%d err=%v", len(versions), err)
	}
	if err := migrate.Verify(ctx, pool, migrationDir, migrate.Schema("request-fulfillment")); err != nil {
		t.Fatalf("read-only exact 0001-0010 verification failed: %v", err)
	}

	// Cold-start the same v1 handlers against the upgraded database and verify the
	// pre-upgrade aggregate remained visible before performing v1 mutations.
	slice, server, workflow := newPostgresV1Slice(t, pool)
	defer server.Close()
	legacy, err := slice.Repository.FindByID(ctx, domain.RequestID(legacyID))
	if err != nil || legacy.ID().String() != legacyID || legacy.Status() != domain.StatusSubmitted {
		t.Fatalf("legacy request changed/lost across G1.5 upgrade: request=%v err=%v", legacy, err)
	}
	workflow.slice = slice

	start := do(t, server, http.MethodPost, "/requests/"+legacyID+"/workflow", map[string]any{"tenant_id": "g15-tenant", "lead_target": 7})
	if start.StatusCode != http.StatusAccepted {
		t.Fatalf("v1 direct-start after G1.5 upgrade = %d: %s", start.StatusCode, readBody(t, start))
	}
	start.Body.Close()
	workflow.mu.Lock()
	started := len(workflow.started)
	workflow.mu.Unlock()
	if started != 1 {
		t.Fatalf("v1 direct-start count = %d, want 1", started)
	}
	driveRequestToProcessing(t, server, legacyID, "g15-tenant")

	if _, err := slice.FailRequest.Handle(ctx, command.FailRequest{
		LifecycleFields: command.LifecycleFields{RequestID: domain.RequestID(legacyID), TenantID: "g15-tenant", Actor: "g15-test"},
		ReasonCode:      "search_failed", Reason: "synthetic v1 bridge retry fixture",
	}); err != nil {
		t.Fatalf("legacy v1 failure transition: %v", err)
	}
	client := newConsoleClient(server)
	client.login(t)
	csrf := client.csrfForPage(t, "/company?tenant_id=g15-tenant")
	rerun := client.postForm(t, "/requests/"+legacyID+"/rerun", url.Values{"tenant_id": {"g15-tenant"}, "csrf": {csrf}})
	if rerun.StatusCode != http.StatusSeeOther {
		t.Fatalf("v1 rerun after G1.5 upgrade = %d: %s", rerun.StatusCode, readBody(t, rerun))
	}
	rerun.Body.Close()
	updated, err := slice.Repository.FindByID(ctx, domain.RequestID(legacyID))
	if err != nil || updated.Status() != domain.StatusValidated {
		t.Fatalf("v1 rerun storage result = %v err=%v", updated, err)
	}

	approvalID := "g15-v1-approval"
	created := do(t, server, http.MethodPost, "/requests", map[string]any{
		"request_id": approvalID, "offer_id": "g15-approval-offer", "tenant_id": "g15-tenant",
		"actor": "legacy-client", "content": "Synthetic G1.5 approve fixture", "target": "IT",
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("v1 approval request create = %d: %s", created.StatusCode, readBody(t, created))
	}
	created.Body.Close()
	driveRequestToProcessing(t, server, approvalID, "g15-tenant")
	approvalClient := newConsoleClient(server)
	approvalClient.login(t)
	decision := approvalClient.postForm(t, "/requests/"+approvalID+"/approval/approve", url.Values{
		"tenant_id": {"g15-tenant"}, "csrf": {approvalClient.csrfForPage(t, "/requests/"+approvalID+"/approval?tenant_id=g15-tenant")},
	})
	if decision.StatusCode != http.StatusSeeOther {
		t.Fatalf("v1 approve after G1.5 upgrade = %d: %s", decision.StatusCode, readBody(t, decision))
	}
	decision.Body.Close()
	approved, err := slice.Repository.FindByID(ctx, domain.RequestID(approvalID))
	if err != nil || approved.Status() != domain.StatusFulfilled {
		t.Fatalf("v1 approve storage result = %v err=%v", approved, err)
	}
	if got := len(workflow.decisions); got != 1 || !workflow.decisions[0].Approved {
		t.Fatalf("v1 approve workflow bridge decisions = %+v", workflow.decisions)
	}
}

func driveRequestToProcessing(t *testing.T, server *httptest.Server, id, tenant string) {
	t.Helper()
	for _, path := range []string{"validation/start", "validation/complete", "processing/start"} {
		resp := do(t, server, http.MethodPost, "/requests/"+id+"/"+path, transition(tenant))
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("G1.5 v1 transition %s = %d: %s", path, resp.StatusCode, readBody(t, resp))
		}
		resp.Body.Close()
	}
}

func newPostgresV1Slice(t *testing.T, pool *pgxpool.Pool) (*requestfulfillment.Slice, *httptest.Server, *statefulWorkflow) {
	t.Helper()
	fake := &statefulWorkflow{tenant: "g15-tenant"}
	slice, err := requestfulfillment.Build(requestfulfillment.Config{
		Repository:       postgres.NewRepository(pool),
		UnitOfWork:       postgres.NewUnitOfWork(pool),
		WorkflowStarter:  fake,
		WorkflowStatus:   fake,
		WorkflowSignaler: fake,
		Clock:            fixedClock{at: e2eTime},
		ConsoleUser:      consoleUser,
		ConsolePassword:  consolePassword,
	})
	if err != nil {
		t.Fatalf("G1.5 v1 PostgreSQL service build: %v", err)
	}
	fake.slice = slice
	return slice, httptest.NewServer(slice.Server), fake
}

func newSyntheticG15Database(t *testing.T) (*pgxpool.Pool, func()) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN is required for disposable PostgreSQL 17 G1.5 acceptance")
	}
	if os.Getenv("LEADGEN_POSTGRES_TEST_ALLOW_DATABASE_CREATE") != "1" {
		t.Fatal("G1.5 acceptance requires explicit disposable database creation opt-in")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil || config.ConnConfig.Database != g15AdminDB || os.Getenv("LEADGEN_POSTGRES_TEST_DATABASE") != g15AdminDB {
		t.Fatal("G1.5 test DSN must point exactly to the marked sacrificial admin database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parent, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal("connect sacrificial G1.5 admin database:", err)
	}
	var marker string
	var canCreate bool
	var version int
	if err := parent.QueryRow(ctx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=current_database()`).Scan(&marker); err != nil || marker != g15AdminMarker {
		parent.Close()
		t.Fatal("sacrificial admin database marker mismatch; refusing child creation")
	}
	if err := parent.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname=current_user`).Scan(&canCreate); err != nil || !canCreate {
		parent.Close()
		t.Fatal("sacrificial database identity lacks disposable CREATEDB permission")
	}
	if err := parent.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil || version < 170000 || version >= 180000 {
		parent.Close()
		t.Fatalf("G1.5 acceptance requires PostgreSQL 17, server_version_num=%d", version)
	}
	name := fmt.Sprintf("%s%d", g15ChildPrefix, time.Now().UnixNano())
	if _, err := parent.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
		parent.Close()
		t.Fatal("create disposable G1.5 database:", err)
	}
	childMarker := g15ChildMarker + name
	if _, err := parent.Exec(ctx, "COMMENT ON DATABASE "+pgx.Identifier{name}.Sanitize()+" IS '"+childMarker+"'"); err != nil {
		parent.Close()
		t.Fatal("set child marker; database left untouched:", err)
	}
	childConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		parent.Close()
		t.Fatal("parse child DSN:", err)
	}
	childConfig.ConnConfig.Database = name
	child, err := pgxpool.NewWithConfig(ctx, childConfig)
	if err != nil {
		parent.Close()
		t.Fatal("connect disposable G1.5 database:", err)
	}
	cleanup := func() {
		child.Close()
		cleanCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		var current string
		if err := parent.QueryRow(cleanCtx, `SELECT shobj_description(oid,'pg_database') FROM pg_database WHERE datname=$1`, name).Scan(&current); err != nil || current != childMarker {
			t.Errorf("child database marker mismatch; refusing cleanup")
			parent.Close()
			return
		}
		if _, err := parent.Exec(cleanCtx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()+" WITH (FORCE)"); err != nil {
			t.Errorf("drop disposable G1.5 database: %v", err)
		}
		parent.Close()
	}
	return child, cleanup
}
