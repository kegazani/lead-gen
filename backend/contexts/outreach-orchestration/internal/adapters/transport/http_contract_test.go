package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/outbox/relay"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracts"
	platformcontracts "lead-gen/backend/platform/contracts"
	"lead-gen/backend/platform/reliability/resilience"
	httptransport "lead-gen/backend/platform/transport/http"
)

// ApprovedActionPublisher обязан оставаться пригодным как relay.Sink: публикация
// идёт после commit outbox без переходников.
var _ relay.Sink = (*ApprovedActionPublisher)(nil)

// ownerApprovedActionDTO — АНТИКОРРУПЦИОННЫЙ DTO потребителя на стороне
// message-delivery, объявленный в тесте отдельно от типа владельца. Совпадение
// полей проверяется контрактом, а не общим импортом.
type ownerApprovedActionDTO struct {
	Name             string `json:"name"`
	SchemaVersion    string `json:"schema_version"`
	EventID          string `json:"event_id"`
	TenantID         string `json:"tenant_id"`
	CorrelationID    string `json:"correlation_id"`
	CausationID      string `json:"causation_id"`
	ApprovedActionID string `json:"approved_action_id"`
	CampaignID       string `json:"campaign_id"`
	DraftID          string `json:"draft_id"`
	DraftVersion     int64  `json:"draft_version"`
	DecisionID       string `json:"decision_id"`
	Channel          string `json:"channel"`
	RecipientRef     string `json:"recipient_ref"`

	// Наблюдения из транспорта, а не из тела.
	IdempotencyKey string `json:"-"`
	HeaderName     string `json:"-"`
}

// TestOutreachToDeliveryApprovedActionOverHTTP — контрактный тест связи
// outreach-to-delivery (architecture/domain-catalog.yaml): интеграция command,
// транспорт sync/http-json.
//
// Владелец контракта — outreach-orchestration (этот контекст), потребитель —
// message-delivery. Обе стороны смоделированы in-memory: HTTP-обработчик
// потребителя (platform/transport/http.NewServer) против издателя владельца
// (ApprovedActionPublisher поверх platform/transport/http.NewClient). Внешняя
// сеть не используется.
func TestOutreachToDeliveryApprovedActionOverHTTP(t *testing.T) {
	var (
		mu       sync.Mutex
		received []ownerApprovedActionDTO
	)

	owner := httptransport.NewServer(func(_ context.Context, req httptransport.Request) (httptransport.Response, error) {
		var dto ownerApprovedActionDTO
		if err := json.Unmarshal(req.Body, &dto); err != nil {
			return httptransport.Response{StatusCode: http.StatusBadRequest}, nil
		}
		dto.IdempotencyKey = req.IdempotencyKey
		dto.HeaderName = req.Envelope.Name
		mu.Lock()
		received = append(received, dto)
		mu.Unlock()
		return httptransport.Response{
			StatusCode: http.StatusOK,
			Body:       []byte(`{"accepted":true}`),
			Envelope: platformcontracts.Envelope{
				Name:          contracts.ContractApprovedAction,
				SchemaVersion: contracts.ApprovedActionSchemaVersion,
			},
		}, nil
	})
	server := httptest.NewServer(owner)
	defer server.Close()

	client := httptransport.NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, nil)
	publisher := NewApprovedActionPublisher(client, server.URL, time.Second)

	action := validApprovedAction()
	if err := publisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("публикация approved-action: %v", err)
	}
	// Повторная публикация того же действия: второй внешний эффект создаёт
	// потребитель, а издатель обязан повторить тот же ключ идемпотентности.
	if err := publisher.Publish(context.Background(), action); err != nil {
		t.Fatalf("повторная публикация approved-action: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("потребитель принял %d вызовов, ожидалось 2", len(received))
	}
	first, second := received[0], received[1]

	if first.Name != contracts.ContractApprovedAction {
		t.Errorf("имя контракта в теле: got %q", first.Name)
	}
	if first.HeaderName != contracts.ContractApprovedAction {
		t.Errorf("имя контракта в конверте-заголовке: got %q", first.HeaderName)
	}
	if first.SchemaVersion != contracts.ApprovedActionSchemaVersion {
		t.Errorf("версия схемы: got %q", first.SchemaVersion)
	}
	if first.EventID != action.EventID {
		t.Errorf("event_id: got %q, want %q", first.EventID, action.EventID)
	}
	if first.CorrelationID != action.CorrelationID {
		t.Errorf("correlation_id: got %q, want %q", first.CorrelationID, action.CorrelationID)
	}
	if first.DraftID != action.DraftID || first.DraftVersion != action.DraftVersion {
		t.Errorf("ссылка на черновик: got %s@%d", first.DraftID, first.DraftVersion)
	}
	if first.Channel != action.Channel {
		t.Errorf("канал: got %q, want %q", first.Channel, action.Channel)
	}
	if first.IdempotencyKey != action.IdempotencyKey() {
		t.Errorf("ключ идемпотентности: got %q, want %q", first.IdempotencyKey, action.IdempotencyKey())
	}
	if second.IdempotencyKey != first.IdempotencyKey {
		t.Errorf("повтор должен нести тот же ключ идемпотентности: %q != %q", second.IdempotencyKey, first.IdempotencyKey)
	}
}

// ownerAudienceSnapshotDTO — DTO владельца (audience) в тесте. Потребитель
// объявляет свой contracts.AudienceSnapshot независимо; тест проверяет, что
// проводная форма владельца принимается антикоррупционным DTO потребителя.
type ownerAudienceSnapshotDTO struct {
	EventID       string    `json:"event_id"`
	OccurredAt    time.Time `json:"occurred_at"`
	TenantID      string    `json:"tenant_id"`
	CorrelationID string    `json:"correlation_id"`
	Name          string    `json:"name"`
	SchemaVersion string    `json:"schema_version"`
	SnapshotID    string    `json:"snapshot_id"`
	SegmentID     string    `json:"segment_id"`
	Version       int64     `json:"version"`
	MemberCount   int64     `json:"member_count"`
	GeneratedAt   time.Time `json:"generated_at"`
}

// TestAudienceToOutreachAudienceSnapshotOverHTTP — контрактный тест связи
// audience-to-outreach (architecture/domain-catalog.yaml): интеграция query,
// транспорт sync/http-json.
//
// Владелец контракта — audience, потребитель — outreach-orchestration (этот
// контекст). Обе стороны смоделированы in-memory: HTTP-обработчик владельца
// (platform/transport/http.NewServer) против клиента потребителя
// (AudienceSnapshotClient поверх platform/transport/http.NewClient). Внешняя сеть
// не используется.
func TestAudienceToOutreachAudienceSnapshotOverHTTP(t *testing.T) {
	snapshotBody, err := json.Marshal(ownerAudienceSnapshotDTO{
		EventID:       "evt-snap",
		OccurredAt:    testInstant,
		TenantID:      "tenant-1",
		CorrelationID: "corr-1",
		Name:          contracts.ContractAudienceSnapshot,
		SchemaVersion: contracts.AudienceSnapshotSchemaVersion,
		SnapshotID:    "snap-1",
		SegmentID:     "segment-1",
		Version:       3,
		MemberCount:   120,
		GeneratedAt:   testInstant,
	})
	if err != nil {
		t.Fatalf("сериализация фикстуры владельца: %v", err)
	}

	owner := httptransport.NewServer(func(_ context.Context, req httptransport.Request) (httptransport.Response, error) {
		if req.Method != http.MethodGet {
			return httptransport.Response{StatusCode: http.StatusMethodNotAllowed}, nil
		}
		if !strings.HasSuffix(req.URL, "/audience-snapshots/snap-1") {
			return httptransport.Response{StatusCode: http.StatusNotFound}, nil
		}
		return httptransport.Response{StatusCode: http.StatusOK, Body: snapshotBody}, nil
	})
	server := httptest.NewServer(owner)
	defer server.Close()

	client := httptransport.NewClient(server.Client().Transport, resilience.RetryPolicy{MaxAttempts: 1}, nil)
	snapshots := NewAudienceSnapshotClient(client, server.URL, time.Second)

	env := testEnvelope(contracts.ContractAudienceSnapshot, contracts.AudienceSnapshotSchemaVersion)
	got, err := snapshots.Fetch(context.Background(), env, "snap-1")
	if err != nil {
		t.Fatalf("запрос audience-snapshot: %v", err)
	}
	if got.SnapshotID != "snap-1" || got.SegmentID != "segment-1" {
		t.Errorf("ссылки снимка: got %s / %s", got.SnapshotID, got.SegmentID)
	}
	if got.Version != 3 || got.MemberCount != 120 {
		t.Errorf("версия/размер снимка: got %d / %d", got.Version, got.MemberCount)
	}
	if got.Name != contracts.ContractAudienceSnapshot {
		t.Errorf("имя контракта после разбора: got %q", got.Name)
	}

	// Неизвестный снимок — явная ошибка, а не пустой контракт.
	if _, err := snapshots.Fetch(context.Background(), env, "missing"); err == nil {
		t.Fatal("ответ владельца 404 должен быть ошибкой потребителя")
	}
}
