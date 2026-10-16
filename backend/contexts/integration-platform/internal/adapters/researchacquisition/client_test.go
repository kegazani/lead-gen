package researchacquisition

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"lead-gen/backend/contexts/integration-platform/internal/adapters/firecrawl"
	contract "lead-gen/contracts/integration-platform/researchacquisition"
)

func testStore(t *testing.T, cap, concurrency int) (*Store, contract.Grant) {
	t.Helper()
	dsn := os.Getenv("LEADGEN_POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("LEADGEN_POSTGRES_TEST_DSN не задан: нужен реальный PostgreSQL")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, name := range []string{"0001_research_acquisition.sql", "0003_research_provider_admission.sql"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", "migrations", "red", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(context.Background(), string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	id := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	s := NewStore(pool, concurrency, 0)
	if err = s.EnsureAccount(context.Background(), contract.AccountPolicy{AccountID: id, MaxCredits: cap}); err != nil {
		t.Fatal(err)
	}
	g := contract.Grant{Scope: contract.Scope{TenantID: id, PurposeID: "research"}, ID: id, AccountID: id, IntentID: "intent", IntentKind: "mission", MaxCredits: cap}
	if err = s.EnsureGrant(context.Background(), g); err != nil {
		t.Fatal(err)
	}
	return s, g
}
func action(g contract.Grant, id string) contract.Action {
	return contract.Action{Scope: g.Scope, ActionID: id, Attempt: 1, GrantID: g.ID, ProfileRevision: "firecrawl-v1-research-1", Limits: contract.Limits{TimeoutSeconds: 5, MaxBytes: 1 << 20}}
}
func provider(t *testing.T, h http.HandlerFunc) *firecrawl.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/team/credit-usage" {
			fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":1000}}`)
			return
		}
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	return firecrawl.New(firecrawl.Config{BaseURL: srv.URL, HTTPClient: srv.Client()})
}
func snapshot(t *testing.T, s *Store, g contract.Grant) contract.GrantSnapshot {
	t.Helper()
	r, err := s.Snapshot(context.Background(), g.Scope, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSearchReplayRestartAndScope(t *testing.T) {
	s, g := testStore(t, 10, 4)
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com","title":"Компания без email","description":"ИНН 7701234567"},{"url":"https://other.example","title":"Второй источник"}]}`)
	})
	req := contract.SearchRequest{Action: action(g, "search"), Query: "строительные компании", MaxResults: 10, Geography: contract.Geography{TargetCountry: "RU", RequestedCountry: "RU", AppliedCountry: "DE", ObservedEgressCountry: "US"}}
	a, err := NewClient(fc, s, g.AccountID).Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewClient(fc, NewStore(s.pool, 4, 0), g.AccountID).Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || len(a.Hits) != 2 || a.SHA256 != b.SHA256 || a.Persistence != "persisted" || b.Persistence != "persisted" || a.Geography.AppliedCountry != "" || a.Geography.ObservedEgressCountry != "" {
		t.Fatalf("replay/geography: calls=%d a=%+v b=%+v", calls.Load(), a, b)
	}
	if got := snapshot(t, s, g); got.Account.SpentCredits != 2 || got.Account.ReservedCredits != 0 || got.Allocation.SpentCredits != 2 {
		t.Fatalf("двойное списание: %+v", got)
	}
	req.Query = "другой query"
	if _, err = NewClient(fc, s, g.AccountID).Search(context.Background(), req); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("изменение command: %v", err)
	}
	req.Action.Scope.TenantID = "чужой"
	if _, err = NewClient(fc, s, g.AccountID).Search(context.Background(), req); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("tenant: %v", err)
	}
}
func TestConcurrentMissionsShareAccountCap(t *testing.T) {
	s, g := testStore(t, 2, 10)
	g2 := g
	g2.ID += "-study"
	g2.Scope.TenantID += "-other"
	g2.IntentID = "study"
	g2.IntentKind = "market_study"
	if err := s.EnsureGrant(context.Background(), g2); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		time.Sleep(10 * time.Millisecond)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com"}]}`)
	})
	var ok, denied atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			grant := g
			if i%2 == 1 {
				grant = g2
			}
			_, err := NewClient(fc, s, g.AccountID).Search(context.Background(), contract.SearchRequest{Action: action(grant, fmt.Sprint(i)), Query: "company", MaxResults: 10})
			if err == nil {
				ok.Add(1)
			} else if errors.Is(err, contract.ErrBudgetExhausted) || errors.Is(err, contract.ErrThrottled) {
				denied.Add(1)
			} else {
				t.Errorf("ошибка admission: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if calls.Load() != 1 || ok.Load() != 1 || denied.Load() != 15 {
		t.Fatalf("overspend: calls=%d ok=%d denied=%d", calls.Load(), ok.Load(), denied.Load())
	}
	if got := snapshot(t, s, g).Account; got.SpentCredits != 2 || got.ReservedCredits != 0 {
		t.Fatalf("account: %+v", got)
	}
	if _, err := NewClient(fc, s, g.AccountID).Search(context.Background(), contract.SearchRequest{Action: action(g, "after-spend"), Query: "company", MaxResults: 10}); !errors.Is(err, contract.ErrBudgetExhausted) {
		t.Fatalf("подтверждённый расход должен исчерпать cap: %v", err)
	}
}
func TestUnknownReceiptIsPinnedAndExplicitlyReconciled(t *testing.T) {
	s, g := testStore(t, 4, 1)
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
		fmt.Fprint(w, "провайдер мог списать кредиты")
	})
	req := contract.SearchRequest{Action: action(g, "unknown"), Query: "company", MaxResults: 10}
	c := NewClient(fc, s, g.AccountID)
	r, err := c.Search(context.Background(), req)
	if !errors.Is(err, contract.ErrUnknown) {
		t.Fatalf("error: %v", err)
	}
	if _, err = NewClient(fc, NewStore(s.pool, 1, 0), g.AccountID).Search(context.Background(), req); !errors.Is(err, contract.ErrUnknown) {
		t.Fatalf("replay: %v", err)
	}
	req.Action.Attempt = 2
	if _, err = c.Search(context.Background(), req); !errors.Is(err, contract.ErrUnknown) {
		t.Fatalf("слепой retry: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("выполнен повторный HTTP: %d", calls.Load())
	}
	if got := snapshot(t, s, g); got.Account.UnknownCredits != 2 || got.Account.ReservedCredits != 2 || got.Account.SpentCredits != 0 {
		t.Fatalf("unknown: %+v", got)
	}
	rec := contract.Reconciliation{Scope: g.Scope, GrantID: g.ID, OperationID: r.OperationID, ActualCredits: 1, EvidenceRef: "provider-operation/123", Actor: "operator"}
	if err = s.Reconcile(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if err = s.Reconcile(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if got := snapshot(t, s, g); got.Account.SpentCredits != 1 || got.Account.ReservedCredits != 0 || got.Account.UnknownCredits != 0 {
		t.Fatalf("settle: %+v", got)
	}
	rec.ActualCredits = 2
	if err = s.Reconcile(context.Background(), rec); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("changed reconciliation: %v", err)
	}
}
func TestFetchKeepsContactlessMarkdownAndUnsupportedIsFree(t *testing.T) {
	s, g := testStore(t, 10, 1)
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"success":true,"data":{"markdown":"# Компания\nИНН: 7701234567","metadata":{"sourceURL":"https://example.com/about","title":"О нас"}}}`)
	})
	c := NewClient(fc, s, g.AccountID)
	req := contract.FetchRequest{Action: action(g, "fetch"), URL: "https://example.com/about", Representation: "markdown"}
	out, err := c.Fetch(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Artifacts) != 1 || out.Artifacts[0].Representation != "markdown" || out.Artifacts[0].SHA256 != contentHash(out.Artifacts[0].Content) {
		t.Fatalf("artifact: %+v", out)
	}
	req.Action.ActionID = "dom"
	req.Representation = "html"
	if _, err = c.Fetch(context.Background(), req); !errors.Is(err, contract.ErrUnsupported) {
		t.Fatal(err)
	}
	sr := contract.SearchRequest{Action: action(g, "region"), Query: "company", MaxResults: 10, Geography: contract.Geography{RequireCountry: true, RequestedCountry: "DE"}}
	if _, err = c.Search(context.Background(), sr); !errors.Is(err, contract.ErrUnsupported) {
		t.Fatal(err)
	}
	if calls.Load() != 1 || snapshot(t, s, g).Account.SpentCredits != 1 {
		t.Fatal("unsupported вызвал платный HTTP")
	}
}
func TestExpiredLeaseDoesNotRedispatchAndLateReceiptFenced(t *testing.T) {
	s, g := testStore(t, 10, 1)
	a := action(g, "lost")
	res, err := s.begin(context.Background(), a, "search", "hash", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.pool.Exec(context.Background(), `UPDATE "integration-platform".research_acquisition_attempt SET lease_until=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, res.id); err != nil {
		t.Fatal(err)
	}
	if _, err = s.begin(context.Background(), a, "search", "hash", 2); !errors.Is(err, contract.ErrUnknown) {
		t.Fatal(err)
	}
	if got := snapshot(t, s, g); got.Account.UnknownCredits != 2 {
		t.Fatalf("reserve lost: %+v", got)
	}
	rec := contract.Reconciliation{Scope: g.Scope, GrantID: g.ID, OperationID: res.id, ActualCredits: 2, EvidenceRef: "operator-decision/1", Actor: "admin"}
	if err = s.Reconcile(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	r := contract.Receipt{CreditsCharged: 2, SHA256: "late"}
	if err = s.finish(context.Background(), a, res, r, false, false); !errors.Is(err, contract.ErrConflict) {
		t.Fatalf("stale epoch accepted: %v", err)
	}
	if got := snapshot(t, s, g).Account; got.SpentCredits != 2 || got.ReservedCredits != 0 {
		t.Fatalf("late double charge: %+v", got)
	}
}
func TestProviderSlotsSharedAcrossScopes(t *testing.T) {
	s, g := testStore(t, 20, 1)
	g2 := g
	g2.ID += "2"
	g2.Scope.TenantID += "2"
	g2.IntentID = "second"
	if err := s.EnsureGrant(context.Background(), g2); err != nil {
		t.Fatal(err)
	}
	res, err := s.begin(context.Background(), action(g, "first"), "search", "a", 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.begin(context.Background(), action(g2, "second"), "search", "b", 2); !errors.Is(err, contract.ErrThrottled) {
		t.Fatalf("slot bypass: %v", err)
	}
	if got := snapshot(t, s, g2); got.Allocation.ReservedCredits != 0 {
		t.Fatal("technical denial оставил reserve")
	}
	if err = s.finish(context.Background(), action(g, "first"), res, contract.Receipt{SHA256: "finish"}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.begin(context.Background(), action(g2, "second"), "search", "b", 2); err != nil {
		t.Fatal(err)
	}
}
func TestPartialAndResponseLimit(t *testing.T) {
	s, g := testStore(t, 10, 1)
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://one.example"},{"url":"https://two.example"}]}`)
	})
	c := NewClient(fc, s, g.AccountID)
	req := contract.SearchRequest{Action: action(g, "partial"), Query: "company", MaxResults: 1}
	r, err := c.Search(context.Background(), req)
	if err != nil || r.Status != "partial" || len(r.Hits) != 1 {
		t.Fatalf("partial: %+v %v", r, err)
	}
	req.Action.ActionID = "large"
	req.Action.Limits.MaxBytes = 10
	r, err = c.Search(context.Background(), req)
	if !errors.Is(err, contract.ErrUnknown) || r.ErrorCode != "response_limit" {
		t.Fatalf("bounded bytes: %+v %v", r, err)
	}
}
func Test429ReleasesCreditReservationAnd402StopsAccount(t *testing.T) {
	s, g := testStore(t, 10, 1)
	var status atomic.Int32
	status.Store(429)
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "999999")
		w.WriteHeader(int(status.Load()))
		fmt.Fprint(w, "отказ")
	})
	c := NewClient(fc, s, g.AccountID)
	req := contract.SearchRequest{Action: action(g, "throttle"), Query: "company", MaxResults: 10}
	r, err := c.Search(context.Background(), req)
	if !errors.Is(err, contract.ErrThrottled) || r.RetryAfterSeconds != 300 {
		t.Fatalf("throttle: %+v %v", r, err)
	}
	if got := snapshot(t, s, g).Account; got.SpentCredits != 0 || got.ReservedCredits != 0 {
		t.Fatal(got)
	}
	req.Action.ActionID = "other-during-backoff"
	if _, err = c.Search(context.Background(), req); !errors.Is(err, contract.ErrThrottled) {
		t.Fatalf("общий backoff не сохранён: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("другой action обошёл provider backoff")
	}
	// Продвигаем тестовые часы source state, не ждём пять минут в тесте.
	if _, err = s.pool.Exec(context.Background(), `UPDATE "integration-platform".research_fc_account SET next_allowed_at='epoch' WHERE account_id=$1`, g.AccountID); err != nil {
		t.Fatal(err)
	}
	status.Store(402)
	req.Action.ActionID = "payment"
	if _, err = c.Search(context.Background(), req); !errors.Is(err, contract.ErrBudgetExhausted) {
		t.Fatal(err)
	}
	req.Action.ActionID = "stopped"
	if _, err = c.Search(context.Background(), req); !errors.Is(err, contract.ErrBudgetExhausted) {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("повтор после 402: %d", calls.Load())
	}
}

func TestLedgerLookupNeverDispatchesAndHonorsScope(t *testing.T) {
	s, g := testStore(t, 10, 1)
	var calls atomic.Int32
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com"}]}`)
	})
	c := NewClient(fc, s, g.AccountID)
	req := contract.SearchRequest{Action: action(g, "lookup"), Query: "company", MaxResults: 10}
	sent, err := c.Search(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.Lookup(context.Background(), g.Scope, req.Action.ActionID, 1)
	if err != nil || got.SHA256 != sent.SHA256 {
		t.Fatalf("receipt replay: %+v %v", got, err)
	}
	if _, err = c.Lookup(context.Background(), contract.Scope{TenantID: "other", PurposeID: g.Scope.PurposeID}, req.Action.ActionID, 1); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("scope: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatal("lookup выполнил новый HTTP")
	}
}
func TestClientCannotChargeAnotherProviderAccount(t *testing.T) {
	s, g := testStore(t, 10, 1)
	fc := provider(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("отправлен запрос ключом другого account")
	})
	c := NewClient(fc, s, "other-provider-account")
	_, err := c.Search(context.Background(), contract.SearchRequest{Action: action(g, "wrong-account"), Query: "company", MaxResults: 10})
	if !errors.Is(err, contract.ErrConflict) {
		t.Fatal(err)
	}
	if snapshot(t, s, g).Account.ReservedCredits != 0 {
		t.Fatal("создан reserve на чужой account")
	}
}
func TestReconcileCannotUseForeignOperation(t *testing.T) {
	s, g := testStore(t, 10, 1)
	a := action(g, "unknown-scope")
	res, err := s.begin(context.Background(), a, "search", "hash", 2)
	if err != nil {
		t.Fatal(err)
	}
	r := contract.Receipt{CostState: "unknown", Status: "unknown", SHA256: "unknown"}
	if err = s.finish(context.Background(), a, res, r, true, false); err != nil {
		t.Fatal(err)
	}
	rec := contract.Reconciliation{Scope: g.Scope, GrantID: g.ID, OperationID: res.id, ActualCredits: 1, EvidenceRef: "op/1", Actor: "admin"}
	if err = s.Reconcile(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	other := g
	other.ID += "-other"
	other.IntentID = "other"
	other.Scope.TenantID = g.Scope.TenantID + "-foreign"
	if err = s.EnsureGrant(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	rec.Scope = other.Scope
	rec.GrantID = other.ID
	if err = s.Reconcile(context.Background(), rec); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("idempotency leaked foreign scope: %v", err)
	}
}

func TestProviderBalancePreventsPaidDispatchWithoutPinningReserve(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       error
		code       string
	}{
		{"zero", `{"success":true,"data":{"remainingCredits":0}}`, contract.ErrBudgetExhausted, "provider_balance_insufficient"},
		{"missing", `{"success":true,"data":{"planCredits":10000}}`, contract.ErrUnavailable, "provider_balance_unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, g := testStore(t, 10, 1)
			paid := 0
			gets := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					gets++
					fmt.Fprint(w, tc.body)
					return
				}
				paid++
				fmt.Fprint(w, `{"success":true,"data":[]}`)
			}))
			defer srv.Close()
			c := NewClient(firecrawl.New(firecrawl.Config{BaseURL: srv.URL, HTTPClient: srv.Client()}), s, g.AccountID)
			req := contract.SearchRequest{Action: action(g, "probe"), Query: "company", MaxResults: 10}
			r, err := c.Search(context.Background(), req)
			if !errors.Is(err, tc.want) || r.ErrorCode != tc.code {
				t.Fatalf("probe: %+v %v", r, err)
			}
			if _, err = c.Search(context.Background(), req); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if paid != 0 || gets != 1 {
				t.Fatalf("paid=%d probes=%d", paid, gets)
			}
			if got := snapshot(t, s, g).Account; got.SpentCredits != 0 || got.UnknownCredits != 0 || got.ReservedCredits != 0 {
				t.Fatalf("preflight pin: %+v", got)
			}
		})
	}
}

func TestConcurrentProviderLastCreditsAdmitOneWithoutFalseExhaustion(t *testing.T) {
	s, g := testStore(t, 0, 2)
	var probes, paid atomic.Int32
	ready := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if probes.Add(1) == 2 {
				close(ready)
			}
			select {
			case <-ready:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":2}}`)
			return
		}
		paid.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com"}]}`)
	}))
	defer srv.Close()
	c := NewClient(firecrawl.New(firecrawl.Config{BaseURL: srv.URL, HTTPClient: srv.Client()}), s, g.AccountID)
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			_, err := c.Search(context.Background(), contract.SearchRequest{Action: action(g, fmt.Sprint(i)), Query: "company", MaxResults: 10})
			results <- err
		}(i)
	}
	var success, waiting int
	for i := 0; i < 2; i++ {
		err := <-results
		switch {
		case err == nil:
			success++
		case errors.Is(err, contract.ErrThrottled):
			waiting++
		default:
			t.Errorf("временный конкурирующий резерв не означает исчерпание: %v", err)
		}
	}
	if success != 1 || waiting != 1 || paid.Load() != 1 {
		t.Fatalf("success=%d waiting=%d paid=%d", success, waiting, paid.Load())
	}
	if got := snapshot(t, s, g).Account; got.SpentCredits != 2 || got.ReservedCredits != 0 || got.UnknownCredits != 0 {
		t.Fatalf("account: %+v", got)
	}
}

func TestProviderAdmissionKeepsUnknownReservationAcrossActions(t *testing.T) {
	s, g := testStore(t, 0, 2)
	var paid atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":2}}`)
			return
		}
		paid.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	c := NewClient(firecrawl.New(firecrawl.Config{BaseURL: srv.URL, HTTPClient: srv.Client()}), s, g.AccountID)
	if _, err := c.Search(context.Background(), contract.SearchRequest{Action: action(g, "unknown"), Query: "company", MaxResults: 10}); !errors.Is(err, contract.ErrUnknown) {
		t.Fatalf("потерянный ответ: %v", err)
	}
	if _, err := c.Search(context.Background(), contract.SearchRequest{Action: action(g, "other"), Query: "company", MaxResults: 10}); !errors.Is(err, contract.ErrThrottled) {
		t.Fatalf("unknown удерживает общий остаток: %v", err)
	}
	if paid.Load() != 1 {
		t.Fatalf("второй вызов обошёл неизвестный расход: %d", paid.Load())
	}
	if got := snapshot(t, s, g).Account; got.SpentCredits != 0 || got.ReservedCredits != 2 || got.UnknownCredits != 2 {
		t.Fatalf("account: %+v", got)
	}
}

func TestLocalReservationWaitsAndSameAttemptCanDispatchAfterRelease(t *testing.T) {
	s, g := testStore(t, 2, 2)
	ctx := context.Background()
	first := action(g, "held")
	res, err := s.begin(ctx, first, "search", "pending-probe", 2)
	if err != nil {
		t.Fatal(err)
	}
	var paid atomic.Int32
	c := NewClient(provider(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com"}]}`)
	}), s, g.AccountID)
	request := contract.SearchRequest{Action: action(g, "waiting"), Query: "company", MaxResults: 10}
	if receipt, err := c.Search(ctx, request); !errors.Is(err, contract.ErrThrottled) || receipt.OperationID != "" {
		t.Fatalf("резерв до сети должен ждать без нового attempt: %+v %v", receipt, err)
	}
	if err = s.finish(ctx, first, res, contract.Receipt{Status: "unavailable", CostState: "known", SHA256: "no-paid-dispatch"}, false, false); err != nil {
		t.Fatal(err)
	}
	if _, err = c.Search(ctx, request); err != nil {
		t.Fatalf("повтор того же attempt после освобождения: %v", err)
	}
	if paid.Load() != 1 {
		t.Fatalf("число платных вызовов: %d", paid.Load())
	}
}

func TestUsefulResultReportsUncertainPersistenceAndRecoveryPinsReserve(t *testing.T) {
	s, g := testStore(t, 10, 1)
	var paid atomic.Int32
	c := NewClient(provider(t, func(w http.ResponseWriter, r *http.Request) {
		paid.Add(1)
		// Обрыв доступа к owner store после HTTP эффекта: сетевой вызов не
		// держит SQL connection, поэтому закрытие этого pool не блокируется.
		s.pool.Close()
		fmt.Fprint(w, `{"success":true,"data":[{"url":"https://example.com","title":"Сохранить полезный результат"}]}`)
	}), s, g.AccountID)
	request := contract.SearchRequest{Action: action(g, "receipt-db-failure"), Query: "company", MaxResults: 10}
	receipt, err := c.Search(context.Background(), request)
	if !errors.Is(err, contract.ErrUnknown) || receipt.Persistence != "unknown" || receipt.Status != "success" || len(receipt.Hits) != 1 {
		t.Fatalf("полезный результат с неподтверждённой фиксацией: %+v %v", receipt, err)
	}
	if receipt.SHA256 != receiptHash(receipt) {
		t.Fatal("storage status изменил immutable payload hash")
	}
	pool, err := pgxpool.New(context.Background(), os.Getenv("LEADGEN_POSTGRES_TEST_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err = pool.Exec(context.Background(), `UPDATE "integration-platform".research_acquisition_attempt SET lease_until=clock_timestamp()-interval '1 second' WHERE operation_id=$1`, receipt.OperationID); err != nil {
		t.Fatal(err)
	}
	recoveredStore := NewStore(pool, 1, 0)
	recovered, err := recoveredStore.Lookup(context.Background(), g.Scope, request.Action.ActionID, 1)
	if !errors.Is(err, contract.ErrUnknown) || recovered.Persistence != "persisted" || recovered.OperationID != receipt.OperationID || recovered.SHA256 != "" {
		t.Fatalf("durable inferred outcome: %+v %v", recovered, err)
	}
	if got := snapshot(t, recoveredStore, g).Account; got.ReservedCredits != 2 || got.UnknownCredits != 2 || got.SpentCredits != 0 || paid.Load() != 1 {
		t.Fatalf("recovery не должен повторять POST или терять резерв: %+v paid=%d", got, paid.Load())
	}
}
