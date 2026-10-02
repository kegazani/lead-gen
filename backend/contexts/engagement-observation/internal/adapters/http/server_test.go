package httpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/engagement-observation/internal/adapters/http"
	outboxinmemory "lead-gen/backend/contexts/engagement-observation/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/app/query"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

type seqIDs struct{ n int }

func (s *seqIDs) NewEventID() domain.EventID {
	s.n++
	return domain.EventID(fmt.Sprintf("id-%d", s.n))
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC) }

func newServer(t *testing.T) (*httpapi.Server, *persistence.Repository, *persistence.Links) {
	t.Helper()
	repo := persistence.New()
	ledger := persistence.NewLedger()
	links := persistence.NewLinks()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWorkWith(repo, ledger, links, journal)
	ingest := command.NewIngestEventHandler(uow, fixedClock{}, &seqIDs{})
	record := command.NewRecordTransitionHandler(links, ingest, &seqIDs{})
	server := httpapi.NewServer(httpapi.Dependencies{
		IngestEvent:      ingest,
		RecordTransition: record,
		GetEngagement:    query.NewGetEngagementHandler(repo),
	})
	return server, repo, links
}

func doJSON(t *testing.T, server http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	server, _, _ := newServer(t)
	rec := doJSON(t, server, http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("статус = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("тело = %s", rec.Body.String())
	}
}

func TestIngestDeliveryAndDedup(t *testing.T) {
	server, repo, _ := newServer(t)
	body := `{"signal_id":"sig-1","source":"provider","tenant_id":"tenant-1","company_id":"company-1","kind":"delivery","outcome":"delivered"}`

	first := doJSON(t, server, http.MethodPost, "/events", body)
	if first.Code != http.StatusAccepted {
		t.Fatalf("первый приём: статус = %d, тело = %s", first.Code, first.Body.String())
	}
	var created struct {
		EngagementEventID string `json:"engagement_event_id"`
		Kind              string `json:"kind"`
		Duplicate         bool   `json:"duplicate"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if created.EngagementEventID == "" || created.Kind != "delivery" || created.Duplicate {
		t.Fatalf("ответ = %+v", created)
	}

	second := doJSON(t, server, http.MethodPost, "/events", body)
	if second.Code != http.StatusOK {
		t.Fatalf("повтор: статус = %d", second.Code)
	}
	var duplicate struct {
		Duplicate bool `json:"duplicate"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &duplicate); err != nil {
		t.Fatalf("разбор ответа: %v", err)
	}
	if !duplicate.Duplicate {
		t.Fatalf("повтор должен быть помечен duplicate")
	}

	list, err := repo.ListByTenant(context.Background(), "tenant-1")
	if err != nil || len(list) != 1 {
		t.Fatalf("событий = %d, err = %v; повтор не должен создавать второе", len(list), err)
	}
}

func TestIngestRejectsPixel(t *testing.T) {
	server, _, _ := newServer(t)
	body := `{"signal_id":"sig-1","source":"provider","tenant_id":"tenant-1","company_id":"company-1","kind":"open","outcome":"opened"}`
	rec := doJSON(t, server, http.MethodPost, "/events", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("статус = %d, тело = %s; пиксель должен быть отвергнут", rec.Code, rec.Body.String())
	}
}

func TestIngestRejectsUnknownKind(t *testing.T) {
	server, _, _ := newServer(t)
	body := `{"signal_id":"sig-1","tenant_id":"tenant-1","company_id":"company-1","kind":"purchase"}`
	rec := doJSON(t, server, http.MethodPost, "/events", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("статус = %d, тело = %s", rec.Code, rec.Body.String())
	}
}

func TestResolveTransitionRedirects(t *testing.T) {
	server, _, _ := newServer(t)
	body := `{"signal_id":"sig-1","source":"provider","tenant_id":"tenant-1","company_id":"company-1","kind":"transition","link_ref":"lnk-1","target":"https://example.com/landing"}`
	rec := doJSON(t, server, http.MethodPost, "/events", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("приём перехода: статус = %d, тело = %s", rec.Code, rec.Body.String())
	}

	req := httptest.NewRequest(http.MethodGet, "/r/lnk-1", nil)
	got := httptest.NewRecorder()
	server.ServeHTTP(got, req)
	if got.Code != http.StatusFound {
		t.Fatalf("редирект: статус = %d", got.Code)
	}
	if loc := got.Header().Get("Location"); loc != "https://example.com/landing" {
		t.Fatalf("Location = %q", loc)
	}
}

func TestResolveUnknownLinkNotFound(t *testing.T) {
	server, _, _ := newServer(t)
	req := httptest.NewRequest(http.MethodGet, "/r/missing", nil)
	got := httptest.NewRecorder()
	server.ServeHTTP(got, req)
	if got.Code != http.StatusNotFound {
		t.Fatalf("статус = %d", got.Code)
	}
	if !strings.Contains(got.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("ожидалась HTML-страница, Content-Type = %q", got.Header().Get("Content-Type"))
	}
}

func TestGetEngagementTenantGuard(t *testing.T) {
	server, _, _ := newServer(t)
	body := `{"signal_id":"sig-1","source":"provider","tenant_id":"tenant-1","company_id":"company-1","kind":"reply"}`
	rec := doJSON(t, server, http.MethodPost, "/events", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("приём: статус = %d, тело = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		EngagementEventID string `json:"engagement_event_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	if created.EngagementEventID == "" {
		t.Fatalf("пустой идентификатор события")
	}

	if rec := doJSON(t, server, http.MethodGet, "/engagements/"+created.EngagementEventID, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("без tenant_id: статус = %d", rec.Code)
	}
	ok := doJSON(t, server, http.MethodGet, "/engagements/"+created.EngagementEventID+"?tenant_id=tenant-1", "")
	if ok.Code != http.StatusOK {
		t.Fatalf("чтение: статус = %d, тело = %s", ok.Code, ok.Body.String())
	}
	foreign := doJSON(t, server, http.MethodGet, "/engagements/"+created.EngagementEventID+"?tenant_id=other", "")
	if foreign.Code != http.StatusNotFound {
		t.Fatalf("чужой тенант: статус = %d", foreign.Code)
	}
}
