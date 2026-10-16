package firecrawl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lead-gen/contracts/integration-platform/budget"
	"lead-gen/contracts/integration-platform/crawl"
	"lead-gen/contracts/integration-platform/documentparse"
	"lead-gen/contracts/integration-platform/integrationerror"
	"lead-gen/contracts/integration-platform/search"
)

func TestLegacyDisabledBlocksEveryPaidPathBeforeNetwork(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		fmt.Fprint(w, `{"success":true,"data":{"remainingCredits":100}}`)
	}))
	defer srv.Close()
	c := New(Config{BaseURL: srv.URL, LegacyDisabled: true, HTTPClient: srv.Client()})
	ctx := context.Background()
	checks := map[string]func() error{
		"search": func() error { _, e := c.Search(ctx, search.Query{Text: "company", MaxResults: 5}); return e },
		"crawl": func() error {
			_, e := c.Crawl(ctx, crawl.Request{StartURL: "https://example.com", MaxPages: 1})
			return e
		},
		"extract": func() error { _, e := c.Extract(ctx, crawl.ExtractRequest{URL: "https://example.com"}); return e },
		"scrape":  func() error { _, e := c.Scrape(ctx, "https://example.com"); return e },
		"map":     func() error { _, e := c.Map(ctx, "https://example.com"); return e },
		"document": func() error {
			_, e := c.ParseDocument(ctx, documentparse.Request{Source: "https://example.com/file.pdf"})
			return e
		},
	}
	for name, f := range checks {
		t.Run(name, func(t *testing.T) {
			err := f()
			if !errors.Is(err, budget.ErrLegacyExecutionDisabled) {
				t.Fatal(err)
			}
			var normalized integrationerror.Error
			if !errors.As(err, &normalized) || normalized.Category != integrationerror.CategoryTemporarilyUnavailable {
				t.Fatalf("unavailable category: %v", err)
			}
		})
	}
	g := NewGuard(GuardConfig{Client: c})
	if _, err := g.Begin("task", 100); !errors.Is(err, budget.ErrLegacyExecutionDisabled) {
		t.Fatal(err)
	}
	if g.RegistryStatus().Entries != 0 || calls != 0 {
		t.Fatalf("запрет оставил reserve/network: %+v %d", g.RegistryStatus(), calls)
	}
	if _, err := c.CreditUsage(ctx); err != nil {
		t.Fatalf("read-only probe запрещён: %v", err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}
