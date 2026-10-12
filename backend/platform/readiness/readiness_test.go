package readiness

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRequiredOptionalAndMissingChecks(t *testing.T) {
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute})
	profile := Profile{Capability: "sample", Checks: []CheckSpec{
		{ID: "core", Key: "core", Required: true, Probe: upProbe},
		{ID: "extra", Key: "extra", Probe: func(context.Context) (ProbeResult, error) {
			return ProbeResult{Status: StatusDown, Reason: ReasonProbeFailed}, nil
		}},
	}}
	got := evaluator.Evaluate(context.Background(), profile)
	if got.Status != StatusDegraded || !got.Ready {
		t.Fatalf("optional failure must be visible without blocking readiness: %#v", got)
	}

	profile.Checks = []CheckSpec{{ID: "required", Key: "required", Required: true}}
	got = evaluator.Evaluate(context.Background(), profile)
	if got.Status != StatusUnknown || got.Ready || got.Checks[0].Reason != ReasonProbeNotConfigured {
		t.Fatalf("nil required probe must be unknown/not-ready: %#v", got)
	}

	profile.Checks = nil
	got = evaluator.Evaluate(context.Background(), profile)
	if got.Status != StatusUnknown || got.Ready || len(got.Checks) != 0 {
		t.Fatalf("empty profile must not default to success: %#v", got)
	}
}

func TestRequiredDownAndUnknownAreNotReady(t *testing.T) {
	for _, test := range []struct {
		name   string
		result ProbeResult
		status Status
	}{
		{"down", ProbeResult{Status: StatusDown, Reason: ReasonProbeFailed}, StatusDown},
		{"unknown", ProbeResult{Status: StatusUnknown, Reason: ReasonProbeNotConfigured}, StatusUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := New(Config{}).Evaluate(context.Background(), Profile{Capability: "x", Checks: []CheckSpec{{
				ID: "required", Key: "dep", Required: true, Probe: func(context.Context) (ProbeResult, error) { return test.result, nil },
			}}})
			if got.Ready || got.Status != test.status {
				t.Fatalf("expected %s not-ready, got %#v", test.status, got)
			}
		})
	}
}

func TestCacheTTLExpiryAndSnapshotIsolation(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute, Now: func() time.Time { return now }})
	profile := Profile{Capability: "cached", Checks: []CheckSpec{{ID: "db", Key: "db/main", Required: true, Probe: func(context.Context) (ProbeResult, error) {
		calls.Add(1)
		return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
	}}}}
	first := evaluator.Evaluate(context.Background(), profile)
	first.Checks[0].Status = StatusDown
	second := evaluator.Evaluate(context.Background(), profile)
	if calls.Load() != 1 || second.Checks[0].Status != StatusUp {
		t.Fatalf("cache miss or mutable shared snapshot: calls=%d snapshot=%#v", calls.Load(), second)
	}
	now = now.Add(time.Minute)
	evaluator.Evaluate(context.Background(), profile)
	if calls.Load() != 2 {
		t.Fatalf("expired result was not refreshed: calls=%d", calls.Load())
	}
}

func TestConcurrentCallsCoalesceByStableDependencyKey(t *testing.T) {
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	probe := func(context.Context) (ProbeResult, error) {
		calls.Add(1)
		close(started)
		<-release
		return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
	}
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute})
	profile := Profile{Capability: "concurrent", Checks: []CheckSpec{{ID: "one", Key: "shared-key", Required: true, Probe: probe}}}
	const count = 12
	var wg sync.WaitGroup
	wg.Add(count)
	for range count {
		go func() {
			defer wg.Done()
			if result := evaluator.Evaluate(context.Background(), profile); !result.Ready {
				t.Errorf("coalesced check unexpectedly not ready: %#v", result)
			}
		}()
	}
	<-started
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("expected one shared probe call, got %d", got)
	}
}

func TestTimeoutIsBoundedAndSanitized(t *testing.T) {
	evaluator := New(Config{Timeout: 25 * time.Millisecond})
	secret := "https://user:password@example.test/private?token=credential"
	started := time.Now()
	got := evaluator.Evaluate(context.Background(), Profile{Capability: "timeout", Checks: []CheckSpec{{
		ID: "http", Key: "http/secret", Required: true,
		Probe: func(ctx context.Context) (ProbeResult, error) {
			<-ctx.Done()
			return ProbeResult{}, fmt.Errorf("request to %s: %w body=private", secret, ctx.Err())
		},
	}}})
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("probe exceeded bounded timeout: %s", elapsed)
	}
	if got.Status != StatusUnknown || got.Checks[0].Reason != ReasonProbeTimeout {
		t.Fatalf("timeout should be safe unknown: %#v", got)
	}
	encoded := fmt.Sprintf("%#v", got)
	for _, private := range []string{"password", "credential", "private", "example.test"} {
		if strings.Contains(encoded, private) {
			t.Fatalf("snapshot leaked %q: %s", private, encoded)
		}
	}
}

func TestNonCooperativeProbeReturnsAtTimeoutAndRetainsInflightSlot(t *testing.T) {
	evaluator := New(Config{Timeout: 40 * time.Millisecond, TTL: time.Minute, MaxInflight: 2})
	releases := []chan struct{}{make(chan struct{}), make(chan struct{})}
	releaseOnce := make([]sync.Once, len(releases))
	defer func() {
		for i, release := range releases {
			releaseOnce[i].Do(func() { close(release) })
		}
	}()
	probeFor := func(release <-chan struct{}) Probe {
		return func(context.Context) (ProbeResult, error) {
			<-release // намеренно игнорирует context.Context
			return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
		}
	}
	start := time.Now()
	first := evaluator.Evaluate(context.Background(), Profile{Capability: "blocked", Checks: []CheckSpec{{
		ID: "one", Key: "blocked/one", Required: true, Probe: probeFor(releases[0]),
	}}})
	if elapsed := time.Since(start); elapsed > 300*time.Millisecond {
		t.Fatalf("Evaluate waited for non-cooperative probe: %s", elapsed)
	}
	if first.Status != StatusUnknown || first.Checks[0].Reason != ReasonProbeTimeout {
		t.Fatalf("blocked probe must fail closed as timeout: %#v", first)
	}
	second := evaluator.Evaluate(context.Background(), Profile{Capability: "blocked", Checks: []CheckSpec{{
		ID: "two", Key: "blocked/two", Required: true, Probe: probeFor(releases[1]),
	}}})
	if second.Checks[0].Reason != ReasonProbeTimeout {
		t.Fatalf("second blocked probe should also reach its deadline: %#v", second)
	}
	third := evaluator.Evaluate(context.Background(), Profile{Capability: "blocked", Checks: []CheckSpec{{
		ID: "three", Key: "blocked/three", Required: true, Probe: upProbe,
	}}})
	if third.Status != StatusUnknown || third.Checks[0].Reason != ReasonProbeCapacity {
		t.Fatalf("inflight cap must reject a new unique key: %#v", third)
	}
	evaluator.mu.Lock()
	if len(evaluator.flights) != 2 {
		t.Fatalf("non-cooperative probe slots were not retained: %d", len(evaluator.flights))
	}
	firstFlight := evaluator.flights["blocked/one"]
	evaluator.mu.Unlock()
	releaseOnce[0].Do(func() { close(releases[0]) })
	<-firstFlight.done
	reused := evaluator.Evaluate(context.Background(), Profile{Capability: "released-slot", Checks: []CheckSpec{{
		ID: "after-exit", Key: "blocked/after-exit", Required: true, Probe: upProbe,
	}}})
	if !reused.Ready {
		t.Fatalf("released in-flight slot was not reusable: %#v", reused)
	}
}

func TestProbeContextOmitsCallerValuesAndGlobalKeyCoalescesCallers(t *testing.T) {
	type callerValueKey struct{}
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute})
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	probe := func(ctx context.Context) (ProbeResult, error) {
		calls.Add(1)
		if got := ctx.Value(callerValueKey{}); got != nil {
			return ProbeResult{}, fmt.Errorf("caller value leaked into probe: %v", got)
		}
		close(started)
		<-release
		return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
	}
	profile := Profile{Capability: "global-dependency", Checks: []CheckSpec{{
		ID: "database", Key: "postgres/primary", Required: true, Probe: probe,
	}}}
	firstCtx := context.WithValue(context.Background(), callerValueKey{}, "tenant-a")
	firstDone := make(chan Snapshot, 1)
	go func() { firstDone <- evaluator.Evaluate(firstCtx, profile) }()
	<-started
	secondCtx := context.WithValue(context.Background(), callerValueKey{}, "tenant-b")
	secondDone := make(chan Snapshot, 1)
	go func() { secondDone <- evaluator.Evaluate(secondCtx, profile) }()
	time.Sleep(10 * time.Millisecond)
	releaseOnce.Do(func() { close(release) })
	if first, second := <-firstDone, <-secondDone; !first.Ready || !second.Ready {
		t.Fatalf("shared global dependency probe failed: first=%#v second=%#v", first, second)
	}
	if calls.Load() != 1 {
		t.Fatalf("global dependency key did not coalesce caller contexts: calls=%d", calls.Load())
	}
}

func TestCacheCapacityBoundsCompletedEntriesWithoutEvictingFlights(t *testing.T) {
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute, CacheCapacity: 3, MaxInflight: 2})
	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("completed/%d", i)
		got := evaluator.Evaluate(context.Background(), Profile{Capability: "bounded", Checks: []CheckSpec{{
			ID: key, Key: key, Required: true, Probe: upProbe,
		}}})
		if !got.Ready {
			t.Fatalf("completed probe failed: %#v", got)
		}
	}
	evaluator.mu.Lock()
	if len(evaluator.cache) != 3 {
		evaluator.mu.Unlock()
		t.Fatalf("cache exceeded configured capacity: %d", len(evaluator.cache))
	}
	evaluator.mu.Unlock()

	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	blockedDone := make(chan Snapshot, 1)
	go func() {
		blockedDone <- evaluator.Evaluate(context.Background(), Profile{Capability: "inflight", Checks: []CheckSpec{{
			ID: "active", Key: "active", Required: true,
			Probe: func(context.Context) (ProbeResult, error) {
				close(started)
				<-release
				return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
			},
		}}})
	}()
	<-started
	for i := 20; i < 30; i++ {
		key := fmt.Sprintf("completed/%d", i)
		evaluator.Evaluate(context.Background(), Profile{Capability: "bounded", Checks: []CheckSpec{{
			ID: key, Key: key, Required: true, Probe: upProbe,
		}}})
	}
	evaluator.mu.Lock()
	_, active := evaluator.flights["active"]
	cacheLen := len(evaluator.cache)
	evaluator.mu.Unlock()
	if !active || cacheLen > 3 {
		t.Fatalf("cache eviction affected active work or exceeded bound: active=%v cache=%d", active, cacheLen)
	}
	releaseOnce.Do(func() { close(release) })
	if got := <-blockedDone; !got.Ready {
		t.Fatalf("active probe did not complete after release: %#v", got)
	}
}

func TestCoalescedWaiterDeadlineDoesNotCancelSharedProbe(t *testing.T) {
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute})
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	probe := func(ctx context.Context) (ProbeResult, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		}
	}
	profile := Profile{Capability: "deadline", Checks: []CheckSpec{{ID: "shared", Key: "deadline/shared", Required: true, Probe: probe}}}
	firstDone := make(chan Snapshot, 1)
	go func() { firstDone <- evaluator.Evaluate(context.Background(), profile) }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	second := evaluator.Evaluate(ctx, profile)
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond || second.Checks[0].Reason != ReasonProbeTimeout {
		t.Fatalf("coalesced caller deadline was not honored: elapsed=%s snapshot=%#v", elapsed, second)
	}
	evaluator.mu.Lock()
	_, stillRunning := evaluator.flights["deadline/shared"]
	evaluator.mu.Unlock()
	if !stillRunning {
		t.Fatal("waiter deadline canceled or removed shared probe")
	}
	releaseOnce.Do(func() { close(release) })
	first := <-firstDone
	if !first.Ready || calls.Load() != 1 {
		t.Fatalf("waiter deadline canceled/repeated shared probe: snapshot=%#v calls=%d", first, calls.Load())
	}
}

func TestProviderErrorAndUntrustedReasonAreSanitized(t *testing.T) {
	secret := "postgres://user:pass@db.internal/private"
	profile := Profile{Capability: "safe", Checks: []CheckSpec{{ID: "store", Key: secret, Required: true, Probe: func(context.Context) (ProbeResult, error) {
		return ProbeResult{}, errors.New("dial " + secret + " body=hidden")
	}}}}
	got := New(Config{}).Evaluate(context.Background(), profile)
	if got.Checks[0].Reason != ReasonProbeFailed || got.Status != StatusDown {
		t.Fatalf("provider error was not mapped to fixed code: %#v", got)
	}
	profile.Checks[0].Probe = func(context.Context) (ProbeResult, error) {
		return ProbeResult{Status: StatusUp, Reason: ReasonCode(secret)}, nil
	}
	got = New(Config{}).Evaluate(context.Background(), profile)
	if got.Checks[0].Reason != ReasonInvalidResult || strings.Contains(fmt.Sprintf("%#v", got), "postgres") {
		t.Fatalf("untrusted reason leaked: %#v", got)
	}
}

func TestCallerCancellationDoesNotCancelSharedProbe(t *testing.T) {
	evaluator := New(Config{Timeout: time.Second, TTL: time.Minute})
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	probe := func(ctx context.Context) (ProbeResult, error) {
		calls.Add(1)
		close(started)
		select {
		case <-release:
			return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
		case <-ctx.Done():
			return ProbeResult{}, ctx.Err()
		}
	}
	profile := Profile{Capability: "shared", Checks: []CheckSpec{{ID: "dep", Key: "stable", Required: true, Probe: probe}}}
	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan Snapshot, 1)
	go func() { firstDone <- evaluator.Evaluate(firstCtx, profile) }()
	<-started
	cancelFirst()
	first := <-firstDone
	if first.Ready || first.Checks[0].Reason != ReasonProbeCanceled {
		t.Fatalf("canceled caller should leave without canceling shared execution: %#v", first)
	}
	secondDone := make(chan Snapshot, 1)
	go func() { secondDone <- evaluator.Evaluate(context.Background(), profile) }()
	time.Sleep(20 * time.Millisecond)
	close(release)
	second := <-secondDone
	if !second.Ready || calls.Load() != 1 {
		t.Fatalf("shared probe was canceled or repeated: second=%#v calls=%d", second, calls.Load())
	}
}

func TestInvalidStatusAndDuplicateCheckFailClosed(t *testing.T) {
	profile := Profile{Capability: "invalid", Checks: []CheckSpec{
		{ID: "same", Key: "a", Required: true, Probe: func(context.Context) (ProbeResult, error) {
			return ProbeResult{Status: "configured"}, nil
		}},
		{ID: "same", Key: "b", Required: true, Probe: upProbe},
	}}
	got := New(Config{}).Evaluate(context.Background(), profile)
	if got.Ready || got.Status != StatusUnknown || got.Checks[0].Reason != ReasonInvalidResult || got.Checks[1].Reason != ReasonCheckMissing {
		t.Fatalf("invalid profile must fail closed: %#v", got)
	}
}

func upProbe(context.Context) (ProbeResult, error) {
	return ProbeResult{Status: StatusUp, Reason: ReasonNone}, nil
}
