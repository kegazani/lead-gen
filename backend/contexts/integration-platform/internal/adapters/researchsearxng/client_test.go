package researchsearxng

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

func setup(t *testing.T, h http.HandlerFunc) (*Client, *Store, contract.SearchRequest) {
	t.Helper()
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("нужен PostgreSQL для technical ledger")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "red", "0002_proxy_research_acquisition.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	store, err := NewStore(context.Background(), pool, id, 1, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	client, err := NewClient(Config{BaseURL: srv.URL, HTTPClient: srv.Client(), Engines: []string{"duckduckgo", "brave"}}, store)
	if err != nil {
		t.Fatal(err)
	}
	req := contract.SearchRequest{Action: contract.Action{Scope: contract.Scope{TenantID: id, PurposeID: "research"}, ActionID: "search", Attempt: 1, ProfileRevision: "policy-v1", Limits: contract.Limits{TimeoutSeconds: 3, MaxBytes: 1 << 20}}, Query: "строительные компании", MaxResults: 10, Geography: contract.Geography{TargetCountry: "RU", RequestedCountry: "RU", Language: "ru-RU", RequireLanguage: true}}
	return client, store, req
}
func TestSearchNoMoneyReplayLanguageAndEngineEvidence(t *testing.T) {
	var calls atomic.Int32
	c, s, r := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.RawQuery != "" {
			t.Error("query попал в URL access log")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("format") != "json" || r.Form.Get("language") != "ru-RU" || r.Form.Get("engines") != "duckduckgo,brave" {
			t.Errorf("parameters: %v", r.Form)
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.com","title":"<b>Компания</b>","content":"Проекты &amp; строительство","engine":"brave","engines":["duckduckgo","brave"]},{"url":"https://example.com","title":"duplicate"}],"unresponsive_engines":[]}`)
	})
	first, err := c.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := NewClient(Config{BaseURL: c.baseURL, HTTPClient: c.http, Engines: c.engines}, s)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := restarted.Search(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || first.SHA256 != replay.SHA256 || first.Persistence != "persisted" || replay.Persistence != "persisted" || len(first.Hits) != 1 || len(first.Hits[0].Engines) != 2 {
		t.Fatalf("receipt: %+v calls=%d", first, calls.Load())
	}
	if first.Hits[0].Title != "Компания" || first.Geography.AppliedLanguage != "ru-RU" || first.Geography.AppliedCountry != "" || first.Geography.ObservedEgressCountry != "" || first.CostState != "not_reported" || first.CreditsReserved != 0 {
		t.Fatalf("claims/monetary: %+v", first)
	}
	r.Query = "другой запрос"
	if _, err = c.Search(context.Background(), r); !errors.Is(err, contract.ErrConflict) {
		t.Fatal(err)
	}
	if _, err = c.Lookup(context.Background(), contract.Scope{TenantID: "other", PurposeID: r.Action.Scope.PurposeID}, r.Action.ActionID, 1); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("tenant: %v", err)
	}
	r.Geography.RequireCountry = true
	r.Action.ActionID = "strict-region"
	if _, err = c.Search(context.Background(), r); !errors.Is(err, contract.ErrUnsupported) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("unsupported performed HTTP")
	}
}
func TestEnginePartialAndAllFailedAreNotEmpty(t *testing.T) {
	for _, tc := range []struct {
		name, body, status string
		want               error
	}{
		{"partial", `{"results":[{"url":"https://example.com","engine":"brave"}],"unresponsive_engines":[["duckduckgo","CAPTCHA"]]}`, "partial", nil},
		{"failed", `{"results":[],"unresponsive_engines":[["duckduckgo","timeout"],["brave","CAPTCHA"]]}`, "unavailable", contract.ErrUnavailable},
		{"empty", `{"results":[],"unresponsive_engines":[]}`, "empty", nil},
		{"invalid", `{"results":[{"url":"javascript:alert(1)"}]}`, "unavailable", contract.ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, r := setup(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, tc.body) })
			out, err := c.Search(context.Background(), r)
			if out.Status != tc.status || !errors.Is(err, tc.want) {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
}
func TestResponseBoundsAndJSONDisabled(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body, code string
		bytes      int64
	}{
		{"403", 403, "Forbidden", "json_disabled_or_access_denied", 1024},
		{"html", 200, "<html>not json</html>", "invalid_json_response", 1024},
		{"missing", 200, `{"message":"failure"}`, "invalid_json_response", 1024},
		{"large", 200, `{"results":[],"unresponsive_engines":[]}`, "response_limit", 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _, r := setup(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) })
			r.Action.Limits.MaxBytes = tc.bytes
			out, err := c.Search(context.Background(), r)
			if !errors.Is(err, contract.ErrUnavailable) || out.ErrorCode != tc.code || out.CostState != "not_reported" {
				t.Fatalf("%+v %v", out, err)
			}
		})
	}
}
func TestRetryAfterSharedAndAttemptsBounded(t *testing.T) {
	var calls atomic.Int32
	c, s, r := setup(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(429)
	})
	if _, err := c.Search(context.Background(), r); !errors.Is(err, contract.ErrThrottled) {
		t.Fatal(err)
	}
	r.Action.Scope.TenantID += "-second"
	r.Action.ActionID = "other"
	if _, err := c.Search(context.Background(), r); !errors.Is(err, contract.ErrThrottled) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("другой tenant обошёл source cooldown")
	}
	r.Action.Attempt = 4
	if _, err := c.Search(context.Background(), r); !errors.Is(err, contract.ErrAttemptLimit) {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(context.Background(), `UPDATE "integration-platform".research_source_policy SET next_allowed_at='epoch' WHERE source_id=$1`, s.source); err != nil {
		t.Fatal(err)
	}
	r.Action.Attempt = 1
	if _, err := c.Search(context.Background(), r); !errors.Is(err, contract.ErrThrottled) {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal(calls.Load())
	}
}
func TestReadOnlyRetryAfterCrashFencesLateWorker(t *testing.T) {
	c, s, r := setup(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"results":[{"url":"https://example.com"}]}`)
	})
	first, err := s.begin(context.Background(), r.Action, hashValue(r))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `UPDATE "integration-platform".research_source_attempt SET lease_until=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, first.id); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Lookup(context.Background(), r.Action.Scope, r.Action.ActionID, 1); !errors.Is(err, contract.ErrUnknown) {
		t.Fatal(err)
	}
	oldAction := r.Action
	r.Action.Attempt = 2
	out, err := c.Search(context.Background(), r)
	if err != nil || len(out.Hits) != 1 {
		t.Fatalf("read-only retry: %+v %v", out, err)
	}
	late := contract.Receipt{Status: "success", SHA256: "late"}
	if err = s.finish(context.Background(), oldAction, first, late); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("late worker: %v", err)
	}
	var saved bool
	if err = s.pool.QueryRow(context.Background(), `SELECT late_receipt IS NOT NULL FROM "integration-platform".research_source_attempt WHERE operation_id=$1`, first.id).Scan(&saved); err != nil || !saved {
		t.Fatalf("late evidence: %v %v", saved, err)
	}
	got, err := c.Lookup(context.Background(), r.Action.Scope, r.Action.ActionID, 2)
	if err != nil || got.SHA256 != out.SHA256 {
		t.Fatalf("late overwrite: %+v %v", got, err)
	}
}
func TestCrossOriginRedirectDoesNotLeakQuery(t *testing.T) {
	var leaked atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
	defer other.Close()
	c, _, r := setup(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", other.URL)
		w.WriteHeader(307)
	})
	_, err := c.Search(context.Background(), r)
	if !errors.Is(err, contract.ErrUnavailable) || leaked.Load() != 0 {
		t.Fatalf("redirect: %v leaked=%d", err, leaked.Load())
	}
}
