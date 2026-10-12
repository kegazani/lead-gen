package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/postgres"
	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports/contracttest"
	"lead-gen/backend/platform/migrate"
	contracteditorial "lead-gen/contracts/outreach-orchestration/editorialreview"
)

// PostgreSQL-тесты адаптера запускаются против реальной базы: contract suite
// проверяет поведение хранилища, а не мока. Строка подключения берётся из
// переменной окружения LEADGEN_POSTGRES_TEST_DSN; если она не задана, тесты
// пропускаются, поэтому `make check` не требует базы.
//
// Примеры запуска: цель `make test-postgres` поднимает одноразовую базу сама;
// при ручном запуске задайте LEADGEN_POSTGRES_TEST_DSN на свою базу.
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres).
var migrationsDir = filepath.Join("..", "..", "..", "..", "migrations")

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

// TestMain освобождает пул соединений после прогона.
func TestMain(m *testing.M) {
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	os.Exit(code)
}

func TestEditorialStoreDecisionCASAndReplay(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, truncateTables); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewEditorialStore(pool)
	h := appeditorial.Handoff{TenantID: "editorial-pg-tenant", RequestID: "editorial-pg-request", Items: []appeditorial.Item{{LeadID: "lead-pg", MaterialRef: "asset-pg@version-pg", AssetID: "asset-pg", VersionID: "version-pg"}, {LeadID: "lead-race", MaterialRef: "asset-race@version-race", AssetID: "asset-race", VersionID: "version-race"}}}
	campaign, repeated, err := store.Submit(ctx, h)
	if err != nil || repeated || campaign.Purpose != "editorial" {
		t.Fatalf("Submit=%+v repeated=%v err=%v", campaign, repeated, err)
	}
	decision := appeditorial.Decide{TenantID: h.TenantID, CampaignID: campaign.CampaignID, LeadID: "lead-pg", MaterialRef: "asset-pg@version-pg", ExpectedVersion: "version-pg", Outcome: "approved", Actor: "operator-pg", At: time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)}
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _, e := store.Decide(ctx, decision); errs <- e }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatalf("same decision replay failed: %v", e)
		}
	}
	decision.Outcome = "rejected"
	decision.Reason = "reason"
	if _, _, err := store.Decide(ctx, decision); !errors.Is(err, appeditorial.ErrConflict) {
		t.Fatalf("concurrent different decision must conflict: %v", err)
	}
	start := make(chan struct{})
	raceResults := make(chan error, 2)
	var raceWG sync.WaitGroup
	for _, outcome := range []string{"approved", "rejected"} {
		raceWG.Add(1)
		go func(outcome string) {
			defer raceWG.Done()
			<-start
			r := appeditorial.Decide{TenantID: h.TenantID, CampaignID: campaign.CampaignID, LeadID: "lead-race", MaterialRef: "asset-race@version-race", ExpectedVersion: "version-race", Outcome: outcome, Actor: "operator-" + outcome, Reason: "reason", At: time.Now().UTC()}
			_, _, e := store.Decide(ctx, r)
			raceResults <- e
		}(outcome)
	}
	close(start)
	raceWG.Wait()
	close(raceResults)
	success, conflicts := 0, 0
	for e := range raceResults {
		if e == nil {
			success++
		} else if errors.Is(e, appeditorial.ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected concurrent decision error: %v", e)
		}
	}
	if success != 1 || conflicts != 1 {
		t.Fatalf("CAS race: success=%d conflicts=%d", success, conflicts)
	}
	item, _, err := store.Get(ctx, contracteditorial.Query{TenantID: h.TenantID, CampaignID: campaign.CampaignID, LeadID: "lead-pg", MaterialRef: "asset-pg@version-pg", VersionID: "version-pg"})
	if err != nil || item.Decision == nil || item.Decision.Outcome != "approved" || item.Decision.Actor != "operator-pg" {
		t.Fatalf("decision not durable: %+v err=%v", item, err)
	}
	events, err := store.Events(ctx, campaign.CampaignID)
	if err != nil || len(events) != 2 {
		t.Fatalf("editorial event log=%+v err=%v", events, err)
	}
	for _, event := range events {
		if event.EventType != appeditorial.EventTypeDecisionRecorded {
			t.Fatalf("неожиданный event type: %+v", event)
		}
	}
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM "outreach-orchestration".outbox`).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 0 {
		t.Fatalf("editorial решения попали в approved-action outbox: %d", outboxCount)
	}
}

func TestCampaignPurposePostgresSnapshot(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, truncateTables); err != nil {
		t.Fatal(err)
	}
	repo := postgres.New(pool)
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	window, err := domain.NewCampaignWindow(now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		id      string
		purpose domain.CampaignPurpose
	}{{id: "delivery-default", purpose: domain.CampaignPurposeDelivery}, {id: "editorial-purpose", purpose: domain.CampaignPurposeEditorial}} {
		campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{ID: domain.CampaignID(test.id), TenantID: "purpose-tenant", Name: test.id, Window: window, Purpose: test.purpose})
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Save(ctx, campaign); err != nil {
			t.Fatal(err)
		}
		loaded, err := repo.FindByID(ctx, campaign.ID())
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Purpose() != test.purpose {
			t.Fatalf("purpose after PG snapshot=%q want %q", loaded.Purpose(), test.purpose)
		}
	}
}

// TestCampaignRepositoryContract прогоняет общий contract suite порта
// CampaignRepository против PostgreSQL-реализации (задача 5.2). Тот же suite
// проходит и против in-memory-реализации.
func TestCampaignRepositoryContract(t *testing.T) {
	contracttest.RunCampaignRepositoryContract(t, func(t *testing.T) ports.CampaignRepository {
		t.Helper()
		return newRepository(t)
	})
}

// TestDraftWindowContract прогоняет общий contract suite загрузки окна
// черновиков против PostgreSQL-реализации (задача 5.2).
func TestDraftWindowContract(t *testing.T) {
	contracttest.RunDraftWindowContract(t, func(t *testing.T) ports.DraftWindowRepository {
		t.Helper()
		return newRepository(t)
	})
}

// newRepository возвращает репозиторий с пустым состоянием: suite вызывает
// фабрику перед каждым подтестом и ожидает изоляцию прогонов.
func newRepository(t *testing.T) ports.DraftWindowRepository {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(), truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	return postgres.New(pool)
}

// testPool подключается к базе из окружения и применяет миграции один раз за
// прогон. Без заданной строки подключения тесты пропускаются.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптера пропущены", dsnEnv)
	}

	poolOnce.Do(func() {
		ctx := context.Background()
		pool, poolErr = pgxpool.New(ctx, dsn)
		if poolErr != nil {
			return
		}
		if err := pool.Ping(ctx); err != nil {
			poolErr = err
			return
		}
		poolErr = applyMigrations(ctx, pool)
	})
	if poolErr != nil {
		t.Fatalf("подключение к PostgreSQL: %v", poolErr)
	}
	return pool
}

// applyMigrations применяет миграции схемы сервиса. Файлы читаются загрузчиком
// backend/platform/migrate, который до выполнения проверяет принадлежность схеме
// сервиса, поэтому чужая миграция сюда попасть не может. Файл содержит несколько
// операторов, поэтому выполняется без аргументов (простой протокол).
func applyMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := migrate.Load(migrationsDir, migrate.OwnSchema(postgres.Schema))
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			return err
		}
	}
	return nil
}

// TRUNCATE ... CASCADE очищает кампании и подчинённые строки черновиков и
// решений вместе с журналом outbox, сохраняя схему и таблицы.
const truncateTables = `TRUNCATE "outreach-orchestration".editorial_decision_event, "outreach-orchestration".editorial_decision, "outreach-orchestration".editorial_campaign, "outreach-orchestration".request_cancellation_fence, "outreach-orchestration".campaign, "outreach-orchestration".outbox CASCADE`

// TestListCampaignIDs проверяет порт CampaignLister против PostgreSQL. Тест
// пропускается без заданной строки подключения, как и остальные PG-suite.
func TestListCampaignIDs(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	if _, err := pool.Exec(ctx, truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	repo := postgres.New(pool)

	ids, err := repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs пустого репозитория: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("пустой репозиторий вернул %d кампаний", len(ids))
	}

	window, err := domain.NewCampaignWindow(time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-list",
		TenantID: "tenant-1",
		Name:     "Кампания перечисления",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if err := repo.Save(ctx, campaign); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ids, err = repo.ListCampaignIDs(ctx)
	if err != nil {
		t.Fatalf("ListCampaignIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != "campaign-list" {
		t.Fatalf("ожидалась одна кампания campaign-list, получено %v", ids)
	}
}
