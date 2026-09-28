package firecrawl

import "testing"

func TestCostModel(t *testing.T) {
	t.Run("scrape и map — 1 кредит", func(t *testing.T) {
		if got := CostScrape(); got != 1 {
			t.Fatalf("CostScrape = %d, want 1", got)
		}
		if got := CostMap(); got != 1 {
			t.Fatalf("CostMap = %d, want 1", got)
		}
	})

	t.Run("crawl — 1 кредит за страницу", func(t *testing.T) {
		cases := map[int]int{0: 0, 1: 1, 10: 10, 100: 100, -5: 0}
		for in, want := range cases {
			if got := CostCrawl(in); got != want {
				t.Fatalf("CostCrawl(%d) = %d, want %d", in, got, want)
			}
		}
	})

	t.Run("search — 2 кредита за каждые начатые 10 результатов", func(t *testing.T) {
		cases := map[int]int{
			0:   0,
			1:   2,
			9:   2,
			10:  2,
			11:  4,
			20:  4,
			21:  6,
			100: 20,
		}
		for in, want := range cases {
			if got := CostSearch(in); got != want {
				t.Fatalf("CostSearch(%d) = %d, want %d", in, got, want)
			}
		}
	})
}
