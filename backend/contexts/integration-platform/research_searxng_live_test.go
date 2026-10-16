package integrationplatform_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	integrationplatform "lead-gen/backend/contexts/integration-platform"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

// Live acceptance вызывается явно; обычный go test не обращается к интернету.
func TestSearXNGResearchLive(t *testing.T) {
	endpoint := os.Getenv("LEADGEN_SEARXNG_LIVE_URL")
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	if endpoint == "" || dsn == "" {
		t.Skip("для live нужны LEADGEN_SEARXNG_LIVE_URL и реальный PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	raw, err := os.ReadFile("migrations/red/0002_proxy_research_acquisition.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	engines := []string{"duckduckgo", "brave"}
	if v := os.Getenv("RESEARCH_SEARXNG_ENGINES"); v != "" {
		engines = strings.Split(v, ",")
	}
	client, err := integrationplatform.NewSearXNGResearch(integrationplatform.SearXNGResearchConfig{Pool: pool, BaseURL: endpoint, Engines: engines})
	if err != nil {
		t.Fatal(err)
	}
	scope := contract.Scope{TenantID: fmt.Sprintf("searxng-live-%d", time.Now().UnixNano()), PurposeID: "acquisition-validation"}
	receipt, callErr := client.Search(ctx, contract.SearchRequest{Action: contract.Action{Scope: scope, ActionID: "live-search", Attempt: 1, ProfileRevision: client.Capabilities().Revision, Limits: contract.Limits{TimeoutSeconds: 30, MaxBytes: 1 << 20}}, Query: "commercial construction companies London contact", MaxResults: 5, Geography: contract.Geography{TargetCountry: "GB", RequestedCountry: "GB", Language: "en-GB"}})
	if path := os.Getenv("LEADGEN_SEARXNG_LIVE_RECEIPT"); path != "" {
		raw, e := json.MarshalIndent(receipt, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, raw, 0o600); e != nil {
			t.Fatal(e)
		}
	}
	t.Logf("provider=%s status=%s hits=%d code=%s engine_failures=%+v", receipt.Provider, receipt.Status, len(receipt.Hits), receipt.ErrorCode, receipt.EngineFailures)
	if callErr != nil {
		t.Fatalf("live acquisition: %v", callErr)
	}
	if len(receipt.Hits) == 0 {
		t.Fatal("контейнер доступен, но реальных внешних результатов нет")
	}
	if receipt.Geography.AppliedCountry != "" || receipt.CostState != "not_reported" || receipt.CreditsReserved != 0 {
		t.Fatal("неверные географические/денежные claims")
	}
	lookup := client.(contract.ReceiptReader)
	replay, err := lookup.Lookup(ctx, scope, "live-search", 1)
	if err != nil || replay.SHA256 != receipt.SHA256 {
		t.Fatalf("persisted live receipt: %v", err)
	}
}
