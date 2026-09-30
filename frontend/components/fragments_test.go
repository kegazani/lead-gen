package components

import (
	"bytes"
	"testing"
	"time"
)

func TestRenderTabs(t *testing.T) {
	tabs := Tabs{
		Items: []Tab{
			{Key: "segments", Label: "разведка сегментов", Href: "/segments"},
			{Key: "campaigns", Label: "кампании", Href: "/campaigns"},
			{Key: "crm", Label: "CRM", Href: "/crm"},
		},
		Active: "campaigns",
	}

	var buffer bytes.Buffer
	if err := RenderTabs(&buffer, tabs); err != nil {
		t.Fatalf("RenderTabs: %v", err)
	}

	want := `<nav class="tabs">
  <a class="tab" href="/segments">разведка сегментов</a>
  <a class="tab is-active" href="/campaigns" aria-current="page">кампании</a>
  <a class="tab" href="/crm">CRM</a>
</nav>`
	if got := buffer.String(); got != want {
		t.Fatalf("разметка вкладок:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderTabsWithoutActive(t *testing.T) {
	tabs := Tabs{
		Items: []Tab{{Key: "segments", Label: "разведка сегментов", Href: "/segments"}},
	}

	var buffer bytes.Buffer
	if err := RenderTabs(&buffer, tabs); err != nil {
		t.Fatalf("RenderTabs: %v", err)
	}

	want := `<nav class="tabs">
  <a class="tab" href="/segments">разведка сегментов</a>
</nav>`
	if got := buffer.String(); got != want {
		t.Fatalf("разметка вкладок без активной:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderNav(t *testing.T) {
	nav := Nav{
		Items: []NavItem{
			{Label: "Главная", Href: "/"},
			{Label: "Воронка", Href: "/funnel", Active: true},
			{Label: "Карточки", Href: "/cards"},
		},
	}

	var buffer bytes.Buffer
	if err := RenderNav(&buffer, nav); err != nil {
		t.Fatalf("RenderNav: %v", err)
	}

	want := `<nav class="sidebar">
  <ul class="sidebar-list">
    <li><a href="/">Главная</a></li>
    <li><a href="/funnel" class="is-active" aria-current="page">Воронка</a></li>
    <li><a href="/cards">Карточки</a></li>
  </ul>
  <button class="theme-toggle" id="theme-toggle" type="button" aria-label="Тема оформления">
    <span class="theme-toggle-label">Тема</span>
    <b class="theme-toggle-value" data-theme-name>Как в системе</b>
  </button>
</nav>`
	if got := buffer.String(); got != want {
		t.Fatalf("разметка навигации:\n got: %q\nwant: %q", got, want)
	}
}

func TestRenderStatusBadge(t *testing.T) {
	cases := []struct {
		status string
		class  string
	}{
		{status: "queued", class: "queued"},
		{status: "running", class: "running"},
		{status: "done", class: "done"},
		{status: "failed", class: "failed"},
		{status: "In Progress", class: "in-progress"},
	}

	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			var buffer bytes.Buffer
			if err := RenderStatusBadge(&buffer, tc.status); err != nil {
				t.Fatalf("RenderStatusBadge(%q): %v", tc.status, err)
			}

			want := `<span class="status status-` + tc.class + `">` + tc.status + `</span>`
			if got := buffer.String(); got != want {
				t.Fatalf("бейдж %q:\n got: %q\nwant: %q", tc.status, got, want)
			}
		})
	}
}

func TestRenderProvenance(t *testing.T) {
	provenance := Provenance{
		Source: "https://example.com/team",
		Date:   time.Date(2026, time.January, 2, 0, 0, 0, 0, time.UTC),
	}

	var buffer bytes.Buffer
	if err := RenderProvenance(&buffer, provenance); err != nil {
		t.Fatalf("RenderProvenance: %v", err)
	}

	want := `<span class="provenance"><a href="https://example.com/team" rel="noopener noreferrer">https://example.com/team</a> <time datetime="2026-01-02">2026-01-02</time></span>`
	if got := buffer.String(); got != want {
		t.Fatalf("разметка происхождения:\n got: %q\nwant: %q", got, want)
	}
}
