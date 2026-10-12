package firecrawl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/search"
)

// guardTestState — изменяемое состояние фейкового сервера Firecrawl для тестов
// обёртки бюджета.
type guardTestState struct {
	mu sync.Mutex

	remainingCredits int
	creditFail       bool
	creditCalls      int

	remainingTokens int
	tokenFail       bool
	tokenCalls      int

	searchResults    int
	searchHTTP       int    // 0 — успех; иначе HTTP-статус (например, 429)
	searchFailures   int    // число раз вернуть 429, прежде чем отвечать успехом
	searchRetryAfter string // значение заголовка Retry-After при 429 (пусто — без заголовка)
	searchCalls      int

	crawlPages int
	crawlHTTP  int // 0 — успешный синхронный ответ; иначе HTTP-статус ошибки
	crawlLimit []int

	scrapeHTTP   int
	mapHTTP      int
	extractCalls int
}

// guardTestView — согласованный снимок состояния сервера без мьютекса.
type guardTestView struct {
	crawlLimit  []int
	searchCalls int
}

func (s *guardTestState) snapshot() guardTestView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return guardTestView{
		crawlLimit:  append([]int(nil), s.crawlLimit...),
		searchCalls: s.searchCalls,
	}
}

// newGuardTest поднимает фейковый Firecrawl и собирает Guard с фейковыми часами
// и записывающей задержкой повторов.
func newGuardTest(t *testing.T) (*Guard, *guardTestState, *fakeClock, *recordedSleep) {
	t.Helper()
	st := &guardTestState{remainingCredits: 1000, remainingTokens: 500, searchResults: 2, crawlPages: 3}

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/team/credit-usage", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		st.creditCalls++
		fail := st.creditFail
		rem := st.remainingCredits
		st.mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"remainingCredits": rem, "planCredits": 10000}})
	})
	mux.HandleFunc("/v2/team/token-usage", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		st.tokenCalls++
		fail := st.tokenFail
		tokens := st.remainingTokens
		st.mu.Unlock()
		if fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"remainingTokens": tokens}})
	})
	mux.HandleFunc("/v1/search", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		st.searchCalls++
		if st.searchFailures > 0 {
			st.searchFailures--
			retryAfter := st.searchRetryAfter
			st.mu.Unlock()
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		status := st.searchHTTP
		n := st.searchResults
		st.mu.Unlock()
		if status == http.StatusTooManyRequests {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		if status != 0 && status != http.StatusOK {
			http.Error(w, "search error", status)
			return
		}
		results := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			results = append(results, map[string]any{"url": "https://example.com/" + string(rune('a'+i)), "title": "T", "description": "d"})
		}
		writeJSON(t, w, map[string]any{"success": true, "data": results})
	})
	mux.HandleFunc("/v1/crawl", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			URL   string `json:"url"`
			Limit int    `json:"limit"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		st.mu.Lock()
		st.crawlLimit = append(st.crawlLimit, body.Limit)
		status := st.crawlHTTP
		n := st.crawlPages
		st.mu.Unlock()
		switch {
		case status == http.StatusTooManyRequests:
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		case status == http.StatusPaymentRequired:
			http.Error(w, "payment required", http.StatusPaymentRequired)
			return
		case status != 0 && status != http.StatusOK:
			http.Error(w, "crawl error", status)
			return
		}
		pages := make([]map[string]any, 0, n)
		for i := 0; i < n; i++ {
			pages = append(pages, map[string]any{"url": "https://example.com/page/" + string(rune('a'+i)), "markdown": "# p"})
		}
		writeJSON(t, w, map[string]any{"success": true, "data": pages})
	})
	mux.HandleFunc("/v1/scrape", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		status := st.scrapeHTTP
		st.extractCalls++
		st.mu.Unlock()
		if status == http.StatusTooManyRequests {
			http.Error(w, "rate limited", http.StatusTooManyRequests)
			return
		}
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"url": "https://example.com", "markdown": "# page"}})
	})
	mux.HandleFunc("/v1/map", func(w http.ResponseWriter, _ *http.Request) {
		st.mu.Lock()
		status := st.mapHTTP
		st.mu.Unlock()
		if status != 0 && status != http.StatusOK {
			http.Error(w, "err", status)
			return
		}
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"links": []string{"https://example.com/a", "https://example.com/b"}}})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	client := New(Config{BaseURL: srv.URL})
	clock := newFakeClock()
	recSleep := &recordedSleep{}
	limiter := NewLimiter(LimiterConfig{
		MinInterval:   time.Nanosecond,
		CrawlInterval: time.Nanosecond,
		Clock:         clock,
		Sleep:         advancingSleep(clock),
	})
	guard := NewGuard(GuardConfig{
		Client:     client,
		Limiter:    limiter,
		BalanceTTL: time.Minute,
		MaxRetries: 3,
		RetryBase:  time.Second,
		Clock:      clock,
		Sleep:      recSleep.fn(),
		Jitter:     func(int64) int64 { return 0 },
	})
	return guard, st, clock, recSleep
}

func guardTestUnknownOutcomeRetainsReservationAndDoesNotRetry(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.searchHTTP = http.StatusServiceUnavailable
	st.mu.Unlock()

	task, err := guard.Task("unknown", 2)
	if err != nil {
		t.Fatal(err)
	}
	_, err = task.Search(context.Background(), search.Query{Text: "q", MaxResults: 5})
	if err == nil {
		t.Fatal("5xx с неизвестным исходом должен вернуть ошибку")
	}
	if got := st.snapshot().searchCalls; got != 1 {
		t.Fatalf("неизвестный исход повторён: попыток %d, want 1", got)
	}
	if snap := task.Snapshot(); snap.ReservedCredits != 2 || snap.ConfirmedSpentCredits != 0 || snap.Status != "stopped" {
		t.Fatalf("резерв неизвестного исхода потерян: %+v", snap)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q", MaxResults: 5}); err == nil {
		t.Fatal("новый вызов должен быть заблокирован удержанным резервом")
	}
}

func guardTestCancellationBeforeCallDoesNotSpendOrReserve(t *testing.T) {
	guard, _, _, _ := newGuardTest(t)
	task, err := guard.Task("cancelled", 5)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := task.Search(ctx, search.Query{Text: "q", MaxResults: 5}); err == nil {
		t.Fatal("отменённый контекст должен быть отклонён")
	}
	if snap := task.Snapshot(); snap.ReservedCredits != 0 || snap.ConfirmedSpentCredits != 0 {
		t.Fatalf("отмена до сети изменила бюджет: %+v", snap)
	}
}

func guardTestFailedSettlementKeepsReservation(t *testing.T) {
	b, _ := NewTaskBudget(5)
	if err := b.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := b.Settle(4, 1); err == nil {
		t.Fatal("settlement большей суммы, чем активный резерв, должен отказать")
	}
	if snap := b.Snapshot(); snap.ReservedCredits != 3 || snap.ConfirmedSpentCredits != 0 {
		t.Fatalf("ошибочный settlement изменил бюджет: %+v", snap)
	}
}

func guardTestOverrunIsRecordedAndStopsAdmission(t *testing.T) {
	b, _ := NewTaskBudget(5)
	if err := b.Reserve(3); err != nil {
		t.Fatal(err)
	}
	if err := b.Settle(3, 4); err == nil {
		t.Fatal("расход выше резерва должен вернуть ошибку")
	}
	snap := b.Snapshot()
	if snap.ConfirmedSpentCredits != 4 || snap.ReservedCredits != 0 || snap.Status != BudgetStopped {
		t.Fatalf("overrun не записан явно: %+v", snap)
	}
	if err := b.Reserve(1); err == nil {
		t.Fatal("overrun должен блокировать дальнейший admission")
	}
}

func guardTestExtractWithoutConfirmedPriceIsUncallable(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	task, err := guard.Task("extract", 5)
	if err != nil {
		t.Fatal(err)
	}
	_, err = task.Extract(context.Background(), crawl.ExtractRequest{URL: "https://example.com"})
	if err != ErrExtractCostUnknown {
		t.Fatalf("Extract error = %v, want ErrExtractCostUnknown", err)
	}
	st.mu.Lock()
	calls := st.extractCalls
	st.mu.Unlock()
	if calls != 0 {
		t.Fatalf("Extract вызван до подтверждения цены: %d", calls)
	}
	if snap := task.Snapshot(); snap.ReservedCredits != 0 {
		t.Fatalf("неподдержанный Extract создал резерв: %+v", snap)
	}
}

func guardTestDoesNotRetryCrawlCreateOnUnknownOutcome(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.crawlHTTP = http.StatusServiceUnavailable
	st.mu.Unlock()
	task, err := guard.Task("crawl-unknown", 5)
	if err != nil {
		t.Fatal(err)
	}
	_, err = task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5})
	if err == nil {
		t.Fatal("неизвестный исход crawl create должен вернуть ошибку")
	}
	if got := len(st.snapshot().crawlLimit); got != 1 {
		t.Fatalf("crawl create POST повторён %d раз, want 1", got)
	}
	if snap := task.Snapshot(); snap.ReservedCredits != 5 {
		t.Fatalf("неизвестный crawl исход не удержал резерв: %+v", snap)
	}
}

func guardTestPaymentRequiredStopsAdmissionForAllTasks(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.crawlHTTP = http.StatusPaymentRequired
	st.mu.Unlock()
	first, _ := guard.Task("first", 5)
	second, _ := guard.Task("second", 5)
	_, _ = first.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5})
	st.mu.Lock()
	st.crawlHTTP = 0
	st.mu.Unlock()
	if err := guard.RefreshCredit(context.Background()); err != nil {
		t.Fatalf("обновить balance после 402: %v", err)
	}
	before := len(st.snapshot().crawlLimit)
	if _, err := second.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5}); err == nil {
		t.Fatal("Guard должен блокировать новую задачу после 402")
	}
	if after := len(st.snapshot().crawlLimit); after != before {
		t.Fatalf("после 402 отправлено новых crawl create: before=%d after=%d", before, after)
	}
}

func TestGuardSafetyRegressionMatrix(t *testing.T) {
	cases := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "unknown outcome retains reservation and blocks retry", run: guardTestUnknownOutcomeRetainsReservationAndDoesNotRetry},
		{name: "429 retry is independently reserved", run: guardTestRateLimitRetriesThenSucceeds},
		{name: "cancellation before call", run: guardTestCancellationBeforeCallDoesNotSpendOrReserve},
		{name: "failed settlement retains reservation", run: guardTestFailedSettlementKeepsReservation},
		{name: "overrun is recorded without clipping", run: guardTestOverrunIsRecordedAndStopsAdmission},
		{name: "unsupported Extract is uncallable", run: guardTestExtractWithoutConfirmedPriceIsUncallable},
		{name: "crawl create is not duplicated", run: guardTestDoesNotRetryCrawlCreateOnUnknownOutcome},
		{name: "402 blocks all tasks", run: guardTestPaymentRequiredStopsAdmissionForAllTasks},
	}
	for _, tc := range cases {
		t.Run(tc.name, tc.run)
	}
}

func TestBudgetFactoryLifecycleContractThroughPublishedInterface(t *testing.T) {
	guard, _, _, _ := newGuardTest(t)
	var factory budget.Factory = guard
	var statusReader budget.RegistryStatusReader = guard
	task, err := factory.Begin("contract-lifecycle", 10)
	if err != nil {
		t.Fatalf("budget.Factory.Begin: %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "lifecycle", MaxResults: 5}); err != nil {
		t.Fatalf("budget.Task.Search: %v", err)
	}
	if err := factory.Finish(context.Background(), "contract-lifecycle"); err != nil {
		t.Fatalf("budget.Factory.Finish: %v", err)
	}
	status := statusReader.RegistryStatus()
	if status.Entries != 1 || status.FinishedScopes != 1 || status.Capacity <= 0 {
		t.Fatalf("published registry status = %+v", status)
	}
}

func TestGuardUnknownNetworkTimeoutRetainsReservationAndStopsTask(t *testing.T) {
	var searchCalls atomic.Int32
	releaseHandler := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/team/credit-usage", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, map[string]any{"success": true, "data": map[string]any{"remainingCredits": 100}})
	})
	mux.HandleFunc("/v1/search", func(_ http.ResponseWriter, r *http.Request) {
		searchCalls.Add(1)
		select {
		case <-r.Context().Done():
		case <-releaseHandler:
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	defer close(releaseHandler)

	client := New(Config{BaseURL: srv.URL})
	clock := newFakeClock()
	limiter := NewLimiter(LimiterConfig{
		MinInterval: time.Nanosecond,
		Clock:       clock,
		Sleep:       advancingSleep(clock),
	})
	guard := NewGuard(GuardConfig{
		Client:     client,
		Limiter:    limiter,
		BalanceTTL: time.Minute,
		Clock:      clock,
		MaxRetries: 1,
		RetryBase:  time.Millisecond,
	})
	task, err := guard.Task("network-timeout", 10)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := task.Search(ctx, search.Query{Text: "timeout", MaxResults: 5}); err == nil {
		t.Fatal("таймаут сетевого запроса должен быть неизвестным исходом")
	}
	if got := task.Snapshot(); got.ReservedCredits != 2 || got.ConfirmedSpentCredits != 0 || got.Status != "stopped" {
		t.Fatalf("резерв неизвестного сетевого исхода не удержан: %+v", got)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "second", MaxResults: 5}); err == nil {
		t.Fatal("задача должна блокировать новые сетевые попытки после неизвестного исхода")
	}
	if got := searchCalls.Load(); got != 1 {
		t.Fatalf("после неизвестного исхода отправлено %d запросов, want 1", got)
	}
}

type panicSearchRoundTripper struct{ remaining int }

func (p *panicSearchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "/v1/search" {
		panic("synthetic transport panic after paid attempt admission")
	}
	if req.URL.Path != "/v2/team/credit-usage" {
		return nil, fmt.Errorf("unexpected Firecrawl path %s", req.URL.Path)
	}
	body := fmt.Sprintf(`{"success":true,"data":{"remainingCredits":%d}}`, p.remaining)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

func TestGuardPanicCleansInflightButKeepsUnknownPinnedWithoutOperationEvidence(t *testing.T) {
	clock := newFakeClock()
	roundTripper := &panicSearchRoundTripper{remaining: 100}
	client := New(Config{BaseURL: "http://firecrawl.test"})
	client.httpClient = &http.Client{Transport: roundTripper}
	usage := NewUsage(client, clock)
	usage.credits = &usageSnapshot{value: 100, updatedAt: clock.Now()}
	guard := NewGuard(GuardConfig{Client: client, Usage: usage, Clock: clock})
	task, err := guard.Task("panic-unknown", 10)
	if err != nil {
		t.Fatal(err)
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = task.Search(context.Background(), search.Query{Text: "panic", MaxResults: 5})
	}()
	if recovered == nil {
		t.Fatal("panic провайдера должен быть перепаничен вызывающему")
	}
	if got := task.Snapshot(); got.OutstandingReservations != 0 || got.UnknownCredits != 2 || got.ReservedCredits != 2 {
		t.Fatalf("panic утёк как in-flight или освободил unknown: %+v", got)
	}
	status := guard.RegistryStatus()
	if status.ActiveScopes != 1 || status.OutstandingReservations != 0 || status.UnknownReservations != 1 {
		t.Fatalf("status Guard не показывает pinned unknown pressure: %+v", status)
	}

	if err := guard.Finish(context.Background(), "panic-unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("неизменившийся balance не должен разрешить unknown: %v", err)
	}
	roundTripper.remaining = 98 // агрегированное уменьшение не указывает на этот вызов.
	clock.Advance(time.Second)
	if err := guard.RefreshCredit(context.Background()); err != nil {
		t.Fatalf("обновить агрегированный account balance: %v", err)
	}
	if err := guard.Finish(context.Background(), "panic-unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("aggregate balance не даёт operation-level evidence: %v", err)
	}
	if got := task.Snapshot(); got.UnknownCredits != 2 || got.ConfirmedSpentCredits != 0 || got.ReservedCredits != 2 || got.Status != string(BudgetStopped) {
		t.Fatalf("unknown reservation был освобождён без exact evidence: %+v", got)
	}
}

func TestGuardFinishAndUnrelatedBalanceRefreshKeepUnknownPinned(t *testing.T) {
	guard, state, clock, _ := newGuardTest(t)
	state.mu.Lock()
	state.searchHTTP = http.StatusServiceUnavailable
	state.mu.Unlock()
	task, err := guard.Task("refresh-unknown", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "unknown", MaxResults: 5}); err == nil {
		t.Fatal("5xx outcome должен быть unknown")
	}
	state.mu.Lock()
	state.creditFail = true
	state.mu.Unlock()
	if err := guard.RefreshCredit(context.Background()); err == nil {
		t.Fatal("неуспешный authoritative refresh должен вернуть ошибку")
	}
	if err := guard.Finish(context.Background(), "refresh-unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("ошибка authoritative refresh должна удержать unknown: %v", err)
	}
	if got := task.Snapshot(); got.UnknownCredits != 2 || got.ReservedCredits != 2 {
		t.Fatalf("неуспешный refresh изменил reservation: %+v", got)
	}
	state.mu.Lock()
	state.creditFail = false
	state.remainingCredits = 998
	state.mu.Unlock()
	clock.Advance(time.Second)
	if err := guard.RefreshCredit(context.Background()); err != nil {
		t.Fatalf("RefreshCredit: %v", err)
	}
	if err := guard.Finish(context.Background(), "refresh-unknown"); !errors.Is(err, budget.ErrUnknownOutcomeUnreconciled) {
		t.Fatalf("неперсонализированный account delta не разрешает unknown: %v", err)
	}
	if got := task.Snapshot(); got.ReservedCredits != 2 || got.UnknownCredits != 2 || got.ConfirmedSpentCredits != 0 {
		t.Fatalf("balance refresh освободил unknown reservation: %+v", got)
	}
}

// TestCrawlWithoutExplicitLimitUsesBudget проверяет новую семантику: обход без
// явного max_pages при заданном бюджете отправляется, а граница страниц равна
// доступному бюджету задачи.
func TestCrawlWithoutExplicitLimitUsesBudget(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	outcome, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Crawl без max_pages при бюджете: %v", err)
	}
	if len(outcome.Pages) != 3 {
		t.Fatalf("страниц получено = %d, want 3", len(outcome.Pages))
	}
	limits := st.snapshot().crawlLimit
	if len(limits) != 1 || limits[0] != 20 {
		t.Fatalf("переданный limit = %v, want [20] (граница = доступный бюджет)", limits)
	}
	// Резерв равен границе и освобождается; подтверждённый расход — 3 страницы.
	snap := task.Snapshot()
	if snap.ConfirmedSpentCredits != 3 || snap.ReservedCredits != 0 || snap.Available != 17 {
		t.Fatalf("снимок: spent=%d reserved=%d available=%d, want 3/0/17",
			snap.ConfirmedSpentCredits, snap.ReservedCredits, snap.Available)
	}
}

// TestCrawlWithoutBudgetUsesBalanceLimit проверяет семантику «бюджет не задан»:
// обход без явного max_pages отправляется, а граница страниц равна
// подтверждённому остатку аккаунта (единственный денежный тормоз).
func TestCrawlWithoutBudgetUsesBalanceLimit(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.remainingCredits = 42
	st.crawlPages = 3
	st.mu.Unlock()

	task, err := guard.Task("task-1", 0)
	if err != nil {
		t.Fatalf("Task(0): %v", err)
	}

	_, err = task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com"})
	if err != nil {
		t.Fatalf("Crawl без бюджета и лимита: %v", err)
	}
	limits := st.snapshot().crawlLimit
	if len(limits) != 1 || limits[0] != 42 {
		t.Fatalf("переданный limit = %v, want [42] (граница = остаток аккаунта)", limits)
	}
	snap := task.Snapshot()
	if snap.ConfirmedSpentCredits != 3 || snap.ReservedCredits != 0 {
		t.Fatalf("снимок: spent=%d reserved=%d, want 3/0", snap.ConfirmedSpentCredits, snap.ReservedCredits)
	}
}

// TestCrawlCapsLimitByBudget проверяет приёмку 2: при бюджете 20 кредитов обход
// с max_pages=100 передаёт limit не выше 20.
func TestCrawlCapsLimitByBudget(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	_, err = task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 100})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	limits := st.snapshot().crawlLimit
	if len(limits) != 1 || limits[0] != 20 {
		t.Fatalf("переданный limit = %v, want [20] (обход стоимостью выше бюджета)", limits)
	}
}

// TestCrawlSettlesActualSpend проверяет приёмку 6: после обхода зарезервированный,
// но не потраченный бюджет освобождается, а подтверждённый расход равен
// фактическому числу страниц.
func TestCrawlSettlesActualSpend(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.crawlPages = 3
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	outcome, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 10})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if len(outcome.Pages) != 3 {
		t.Fatalf("число страниц = %d, want 3", len(outcome.Pages))
	}

	snap := task.Snapshot()
	if snap.ConfirmedSpentCredits != 3 {
		t.Fatalf("confirmed = %d, want 3", snap.ConfirmedSpentCredits)
	}
	if snap.ReservedCredits != 0 {
		t.Fatalf("reserved = %d, want 0 (резерв освобождён)", snap.ReservedCredits)
	}
	if snap.Available != 17 {
		t.Fatalf("available = %d, want 17", snap.Available)
	}
}

// TestSearchBudgetsCost проверяет стоимость поиска и подтверждение фактического
// расхода по числу результатов.
func TestSearchBudgetsCost(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.searchResults = 5
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	results, err := task.Search(context.Background(), search.Query{Text: "q", MaxResults: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("число результатов = %d, want 5", len(results))
	}
	// 5 результатов -> ceil(5/10)*2 = 2 кредита.
	if snap := task.Snapshot(); snap.ConfirmedSpentCredits != 2 {
		t.Fatalf("confirmed = %d, want 2", snap.ConfirmedSpentCredits)
	}
}

// TestAccountBalanceRequired проверяет приёмку 3 и 7: если остаток аккаунта
// получить нельзя, затратная операция не запускается.
func TestAccountBalanceRequired(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.creditFail = true
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q"}); err == nil {
		t.Fatal("поиск без доступного остатка аккаунта должен отказать")
	}
	if got := st.snapshot().searchCalls; got != 0 {
		t.Fatalf("поиск отправился в Firecrawl без остатка аккаунта: %d вызовов", got)
	}
}

// TestRateLimitRetriesThenSucceeds проверяет приёмку 5: 429 повторяется с
// возрастающей задержкой, без лавины, и в итоге завершается успехом.
func guardTestRateLimitRetriesThenSucceeds(t *testing.T) {
	guard, st, _, recSleep := newGuardTest(t)
	st.mu.Lock()
	st.searchFailures = 2 // два 429, затем успех
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	results, err := task.Search(context.Background(), search.Query{Text: "q"})
	if err != nil {
		t.Fatalf("Search должен восстановиться после 429: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("Search вернул пустой результат после восстановления")
	}
	// Задержки повторов должны возрастать (1s, 2s, ...) и быть записаны.
	delays := recSleep.list()
	if len(delays) == 0 {
		t.Fatal("ожидались задержки повторов при 429")
	}
	for i := 1; i < len(delays); i++ {
		if delays[i] < delays[i-1] {
			t.Fatalf("задержки повторов не возрастают: %v", delays)
		}
	}
}

// TestRateLimitHonorsRetryAfter проверяет, что при 429 с заголовком Retry-After
// пауза перед повтором берётся из заголовка, а не из локального бэкоффа: провайдер
// знает своё окно лимита точнее.
func TestRateLimitHonorsRetryAfter(t *testing.T) {
	guard, st, _, recSleep := newGuardTest(t)
	st.mu.Lock()
	st.searchFailures = 1
	st.searchRetryAfter = "30"
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q"}); err != nil {
		t.Fatalf("Search должен восстановиться после 429: %v", err)
	}

	delays := recSleep.list()
	if len(delays) != 1 || delays[0] != 30*time.Second {
		t.Fatalf("задержка повтора = %v, ожидалось 30s из Retry-After", delays)
	}
}

// TestRateLimitBoundedRetries проверяет приёмку 5: число повторов ограничено.
func TestRateLimitBoundedRetries(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.searchHTTP = http.StatusTooManyRequests
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q"}); err == nil {
		t.Fatal("постоянный 429 должен завершиться ошибкой после исчерпания повторов")
	}
	// maxRetries=3 -> не более 4 попыток (1 + 3 повтора).
	if got := st.snapshot().searchCalls; got > 4 {
		t.Fatalf("число попыток = %d, want <= 4", got)
	}
}

// TestHTTP400NotRetried проверяет приёмку: HTTP 400 (например, недопустимая
// комбинация URL и maxDepth) не ретраится, в отличие от 429.
func TestHTTP400NotRetried(t *testing.T) {
	guard, st, _, recSleep := newGuardTest(t)
	st.mu.Lock()
	st.crawlHTTP = http.StatusBadRequest
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5}); err == nil {
		t.Fatal("Crawl при 400 должен вернуть ошибку")
	}
	if got := len(st.snapshot().crawlLimit); got != 1 {
		t.Fatalf("число вызовов crawl = %d, want 1 (400 не ретраится)", got)
	}
	if delays := recSleep.list(); len(delays) != 0 {
		t.Fatalf("ожидалось отсутствие задержек повторов при 400, получено %v", delays)
	}
}

// TestPaymentRequiredStops проверяет приёмку 5: HTTP 402 останавливает новые
// затратные операции.
func TestPaymentRequiredStops(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.crawlHTTP = http.StatusPaymentRequired
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5}); err == nil {
		t.Fatal("Crawl при 402 должен вернуть ошибку")
	}
	if snap := task.Snapshot(); snap.Status != "stopped" {
		t.Fatalf("статус бюджета = %q, want stopped", snap.Status)
	}
	// Следующая затратная операция не должна отправляться.
	st.mu.Lock()
	st.crawlHTTP = 0
	st.mu.Unlock()
	before := len(st.snapshot().crawlLimit)
	if _, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 5}); err == nil {
		t.Fatal("новая затратная операция после 402 должна отказать")
	}
	if after := len(st.snapshot().crawlLimit); after != before {
		t.Fatal("новая затратная операция после 402 отправилась в Firecrawl")
	}
}

// TestSnapshotDistinguishesValues проверяет приёмку 6: остаток аккаунта, расход
// задачи и резерв различаются.
func TestSnapshotDistinguishesValues(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.remainingCredits = 500
	st.mu.Unlock()

	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q", MaxResults: 5}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	bal := guard.CreditBalance()
	if !bal.Available || bal.Value != 500 {
		t.Fatalf("остаток аккаунта = %+v, want 500", bal)
	}
	snap := task.Snapshot()
	if snap.MaxCredits != 20 || snap.ConfirmedSpentCredits != 2 || snap.ReservedCredits != 0 || snap.Available != 18 {
		t.Fatalf("снимок задачи = %+v, want max=20 spent=2 reserved=0 available=18", snap)
	}
	if bal.Value == snap.ConfirmedSpentCredits {
		t.Fatal("остаток аккаунта и расход задачи должны быть разными значениями")
	}
}

// TestTokensAreInformational проверяет приёмку 4: токены показываются только из
// успешного ответа token-usage и не влияют на кредитный лимит.
func TestTokensAreInformational(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.remainingTokens = 42
	st.mu.Unlock()

	// До обновления токены недоступны.
	if tok := guard.TokenBalance(); tok.Available {
		t.Fatal("токены до token-usage должны быть недоступны")
	}
	if err := guard.RefreshToken(context.Background()); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	tok := guard.TokenBalance()
	if !tok.Available || tok.Value != 42 {
		t.Fatalf("остаток токенов = %+v, want 42", tok)
	}

	// Ошибка token-usage скрывает поле.
	st.mu.Lock()
	st.tokenFail = true
	st.mu.Unlock()
	if err := guard.RefreshToken(context.Background()); err == nil {
		t.Fatal("RefreshToken при ошибке должен вернуть ошибку")
	}
}

// TestScrapeAndMapBudgets проверяет фиксированную стоимость scrape и map.
func TestScrapeAndMapBudgets(t *testing.T) {
	guard, _, _, _ := newGuardTest(t)
	task, err := guard.Task("task-1", 20)
	if err != nil {
		t.Fatalf("Task: %v", err)
	}

	if _, err := task.Scrape(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if _, err := task.Map(context.Background(), "https://example.com"); err != nil {
		t.Fatalf("Map: %v", err)
	}
	// 2 операции по 1 кредиту.
	if snap := task.Snapshot(); snap.ConfirmedSpentCredits != 2 {
		t.Fatalf("confirmed = %d, want 2", snap.ConfirmedSpentCredits)
	}
}

// TestTaskWithoutBudgetCrawlsAndMeasures проверяет семантику «бюджет не задан»:
// max_credits не применяется (большой MaxPages не отклоняется бюджетом),
// Available не ограничивает, а фактический расход фиксируется.
func TestTaskWithoutBudgetCrawlsAndMeasures(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.crawlPages = 3
	st.mu.Unlock()

	task, err := guard.Task("task-1", 0)
	if err != nil {
		t.Fatalf("Task(0): %v", err)
	}
	if task.Snapshot().MaxCredits != 0 {
		t.Fatalf("max_credits = %d, want 0 (бюджет не задан)", task.Snapshot().MaxCredits)
	}

	// Большой запрос обхода: бюджетом не отклоняется, предел — min(max_pages,
	// остаток аккаунта 1000).
	outcome, err := task.Crawl(context.Background(), crawl.Request{StartURL: "https://example.com", MaxPages: 100})
	if err != nil {
		t.Fatalf("Crawl без бюджета: %v", err)
	}
	if len(outcome.Pages) != 3 {
		t.Fatalf("страниц получено = %d, want 3", len(outcome.Pages))
	}
	if limits := st.snapshot().crawlLimit; len(limits) != 1 || limits[0] != 100 {
		t.Fatalf("переданный limit = %v, want [100] (бюджет не ограничивает)", limits)
	}

	snap := task.Snapshot()
	if snap.MaxCredits != 0 {
		t.Fatalf("max_credits = %d, want 0", snap.MaxCredits)
	}
	if snap.ConfirmedSpentCredits != 3 {
		t.Fatalf("confirmed = %d, want 3 (фактические страницы)", snap.ConfirmedSpentCredits)
	}
	if snap.Available != 0 {
		t.Fatalf("available = %d, want 0 (неиспользуемо без бюджета)", snap.Available)
	}
	if snap.ReservedCredits != 0 {
		t.Fatalf("reserved = %d, want 0 (резерв освобождён)", snap.ReservedCredits)
	}
	if snap.Status != "active" {
		t.Fatalf("статус = %q, want active", snap.Status)
	}
}

// TestTaskWithoutBudgetSearchesAndMeasures проверяет, что без бюджета поиск
// выполняется и его фактический расход фиксируется.
func TestTaskWithoutBudgetSearchesAndMeasures(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.searchResults = 7
	st.mu.Unlock()

	task, err := guard.Task("task-1", 0)
	if err != nil {
		t.Fatalf("Task(0): %v", err)
	}
	// Верхняя оценка стоимости по запрошенному пределу (50 результатов -> 10
	// кредитов) не упирается в бюджет: безлимитный Reserve проходит.
	results, err := task.Search(context.Background(), search.Query{Text: "q", MaxResults: 50})
	if err != nil {
		t.Fatalf("Search без бюджета: %v", err)
	}
	if len(results) != 7 {
		t.Fatalf("результатов = %d, want 7", len(results))
	}
	// 7 результатов -> ceil(7/10)*2 = 2 кредита.
	if snap := task.Snapshot(); snap.ConfirmedSpentCredits != 2 || snap.MaxCredits != 0 {
		t.Fatalf("снимок: spent=%d max=%d, want 2/0", snap.ConfirmedSpentCredits, snap.MaxCredits)
	}
}

// TestTaskWithoutBudgetStillRequiresBalance проверяет, что безлимитная задача
// всё равно требует свежий остаток аккаунта перед затратной операцией.
func TestTaskWithoutBudgetStillRequiresBalance(t *testing.T) {
	guard, st, _, _ := newGuardTest(t)
	st.mu.Lock()
	st.creditFail = true
	st.mu.Unlock()

	task, err := guard.Task("task-1", 0)
	if err != nil {
		t.Fatalf("Task(0): %v", err)
	}
	if _, err := task.Search(context.Background(), search.Query{Text: "q"}); err == nil {
		t.Fatal("поиск без доступного остатка аккаунта должен отказать и без бюджета")
	}
	if got := st.snapshot().searchCalls; got != 0 {
		t.Fatalf("поиск отправился в Firecrawl без остатка аккаунта: %d вызовов", got)
	}
}
