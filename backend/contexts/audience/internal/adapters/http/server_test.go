package httpapi_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	httpapi "lead-gen/backend/contexts/audience/internal/adapters/http"
	outboxinmemory "lead-gen/backend/contexts/audience/internal/adapters/outbox/inmemory"
	persistence "lead-gen/backend/contexts/audience/internal/adapters/persistence/inmemory"
	"lead-gen/backend/contexts/audience/internal/app/command"
	"lead-gen/backend/contexts/audience/internal/app/query"
	"lead-gen/backend/contexts/audience/internal/domain"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
)

type fixedClock struct{ at time.Time }

func (c fixedClock) Now() time.Time { return c.at }

type seqIDs struct{ n int }

func (g *seqIDs) NewEventID() domain.EventID {
	g.n++
	return domain.EventID(fmt.Sprintf("evt-%d", g.n))
}

func (g *seqIDs) NewSnapshotID() domain.SnapshotID {
	g.n++
	return domain.SnapshotID(fmt.Sprintf("snap-%d", g.n))
}

// newServer собирает HTTP-адаптер над реальными in-memory адаптерами.
func newServer() *httpapi.Server {
	segments := persistence.NewSegmentRepository()
	snapshots := persistence.NewSnapshotRepository()
	journal := platformoutbox.NewMemoryJournal()
	uow := outboxinmemory.NewUnitOfWorkWith(segments, snapshots, journal)
	clock := fixedClock{at: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)}
	ids := &seqIDs{}
	return httpapi.NewServer(httpapi.Dependencies{
		CreateSegment:  command.NewCreateSegmentHandler(uow),
		ScoreLead:      command.NewScoreLeadHandler(uow, clock, ids),
		FixSnapshot:    command.NewFixAudienceSnapshotHandler(uow, clock, ids),
		GetSnapshot:    query.NewGetAudienceSnapshotHandler(snapshots),
		SegmentMembers: query.NewSegmentMembersHandler(segments),
	})
}

// do выполняет запрос к серверу и возвращает ответ.
func do(t *testing.T, server *httpapi.Server, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body == "" {
		reader = bytes.NewReader(nil)
	} else {
		reader = bytes.NewReader([]byte(body))
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	return rec
}

// TestHealthz — проверка живости роли api.
func TestHealthz(t *testing.T) {
	rec := do(t, newServer(), http.MethodGet, "/healthz", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("статус = %d, ожидался 200", rec.Code)
	}
	if got := rec.Body.String(); !bytes.Contains([]byte(got), []byte(`"status":"ok"`)) {
		t.Fatalf("тело = %s", got)
	}
}

// TestSnapshotWireFormat — GET /audience-snapshots/{id} отдаёт контракт
// audience-snapshot в проводной форме, понятной потребителю.
func TestSnapshotWireFormat(t *testing.T) {
	server := newServer()

	if rec := do(t, server, http.MethodPost, "/segments",
		`{"segment_id":"segment-1","tenant_id":"tenant-1","name":"Тёплые лиды","model_version":"v1","min_score":0.5}`); rec.Code != http.StatusCreated {
		t.Fatalf("создание сегмента: статус = %d, тело %s", rec.Code, rec.Body)
	}
	if rec := do(t, server, http.MethodPost, "/segments/segment-1/scores",
		`{"lead_id":"lead-1","tenant_id":"tenant-1","observed_at":"2026-10-01T12:00:00Z","signals":[{"name":"fit","strength":0.9}],"actor":"actor-1"}`); rec.Code != http.StatusOK {
		t.Fatalf("скоринг: статус = %d, тело %s", rec.Code, rec.Body)
	}
	fixRec := do(t, server, http.MethodPost, "/audience-snapshots",
		`{"segment_id":"segment-1","tenant_id":"tenant-1","actor":"actor-1"}`)
	if fixRec.Code != http.StatusCreated {
		t.Fatalf("фиксация снимка: статус = %d, тело %s", fixRec.Code, fixRec.Body)
	}
	var fixed struct {
		SnapshotID string `json:"snapshot_id"`
	}
	if err := json.Unmarshal(fixRec.Body.Bytes(), &fixed); err != nil {
		t.Fatalf("разбор ответа фиксации: %v", err)
	}

	rec := do(t, server, http.MethodGet, "/audience-snapshots/"+fixed.SnapshotID+"?tenant_id=tenant-1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("чтение снимка: статус = %d, тело %s", rec.Code, rec.Body)
	}
	var wire struct {
		Name        string `json:"name"`
		SnapshotID  string `json:"snapshot_id"`
		SegmentID   string `json:"segment_id"`
		Version     int64  `json:"version"`
		MemberCount int64  `json:"member_count"`
		GeneratedAt string `json:"generated_at"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatalf("разбор снимка: %v", err)
	}
	if wire.Name != "audience-snapshot" || wire.SegmentID != "segment-1" || wire.MemberCount != 1 || wire.Version < 1 {
		t.Fatalf("проводная форма снимка: %+v", wire)
	}
	if wire.GeneratedAt == "" {
		t.Fatal("generated_at обязателен в контракте")
	}
}

// TestGetSnapshotNotFound — отсутствующий снимок отдаётся как 404.
func TestGetSnapshotNotFound(t *testing.T) {
	rec := do(t, newServer(), http.MethodGet, "/audience-snapshots/missing?tenant_id=tenant-1", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("статус = %d, ожидался 404, тело %s", rec.Code, rec.Body)
	}
}
