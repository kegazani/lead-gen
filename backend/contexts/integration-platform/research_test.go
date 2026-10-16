package integrationplatform_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	"lead-gen/contracts/integration-platform/budget"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
	"lead-gen/contracts/integration-platform/search"
)

func TestResearchConstructorUsesDurablePathWithLegacyDisabled(t *testing.T) {
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("нужен PostgreSQL для durable constructor")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	for _, name := range []string{"0001_research_acquisition.sql", "0003_research_provider_admission.sql"} {
		raw, err := os.ReadFile("migrations/red/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	var paid atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":100}}`)
			return
		}
		paid.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com","title":"Компания"}]}`)
	}))
	defer srv.Close()
	fc := integrationplatform.FirecrawlConfig{APIKey: "test-key", BaseURL: srv.URL, DisableLegacyOperations: true}
	legacy, err := integrationplatform.NewFirecrawl(fc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Search(ctx, search.Query{Text: "company"}); !errors.Is(err, budget.ErrLegacyExecutionDisabled) {
		t.Fatalf("legacy raw: %v", err)
	}
	guard, err := integrationplatform.NewFirecrawlGuard(integrationplatform.FirecrawlGuardConfig{APIKey: fc.APIKey, BaseURL: fc.BaseURL, DisableLegacyOperations: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = guard.Begin("legacy", 10); !errors.Is(err, budget.ErrLegacyExecutionDisabled) {
		t.Fatalf("legacy guard: %v", err)
	}
	id := fmt.Sprintf("constructor-%d", time.Now().UnixNano())
	p, ledger, err := integrationplatform.NewResearchAcquisition(integrationplatform.ResearchAcquisitionConfig{Pool: pool, Firecrawl: fc, Account: contract.AccountPolicy{AccountID: id, MaxCredits: 10}, HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	scope := contract.Scope{TenantID: id, PurposeID: "research"}
	grant := contract.Grant{Scope: scope, ID: id, AccountID: id, IntentID: "human-request", IntentKind: "mission", MaxCredits: 10}
	if err = ledger.EnsureGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	receipt, err := p.Search(ctx, contract.SearchRequest{Action: contract.Action{Scope: scope, ActionID: "action", Attempt: 1, GrantID: id, ProfileRevision: p.Capabilities().Revision, Limits: contract.Limits{TimeoutSeconds: 5, MaxBytes: 1 << 20}}, Query: "company", MaxResults: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.Hits) != 1 || paid.Load() != 1 {
		t.Fatalf("research path: %+v paid=%d", receipt, paid.Load())
	}
}
