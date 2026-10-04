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

	"lead-gen/backend/contexts/engagement-observation/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	platformoutbox "lead-gen/backend/platform/messaging/outbox"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптеров запускаются против реальной базы: contract suite
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

// TestRepositoryContract проверяет round-trip снимка агрегата, оптимистичную
// блокировку по версии и чтение событий тенанта против PostgreSQL.
func TestRepositoryContract(t *testing.T) {
	ctx := context.Background()
	repo := postgres.New(cleanPool(t))
	event := deliveryEvent(t, "evt-1")

	if err := repo.Save(ctx, event, 0); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Повторное создание по ожидаемой версии 0 — конфликт: строка уже занята.
	if err := repo.Save(ctx, event, 0); !errors.Is(err, ports.ErrVersionConflict) {
		t.Fatalf("повторное создание: ошибка = %v, ожидалась ErrVersionConflict", err)
	}
	if err := repo.Save(ctx, event, 1); err != nil {
		t.Fatalf("Save по версии 1: %v", err)
	}

	found, err := repo.FindByID(ctx, "evt-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if found.CompanyID() != "company-1" || found.Kind() != domain.KindDelivery || found.Version() != 1 {
		t.Fatalf("событие = %+v", found.Snapshot())
	}
	if _, err := repo.FindByID(ctx, "missing"); !errors.Is(err, ports.ErrEventNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrEventNotFound", err)
	}

	list, err := repo.ListByTenant(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("ListByTenant: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("событий = %d, ожидалось 1", len(list))
	}
	other, err := repo.ListByTenant(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("ListByTenant чужого тенанта: %v", err)
	}
	if len(other) != 0 {
		t.Fatalf("событий чужого тенанта = %d, ожидалось 0", len(other))
	}
}

// TestLedgerContract проверяет дедупликацию сырого сигнала: до Mark — не
// обработан, после — обработан; пустой ключ отвергается.
func TestLedgerContract(t *testing.T) {
	ctx := context.Background()
	ledger := postgres.NewLedger(cleanPool(t))

	seen, err := ledger.Seen(ctx, "signal:sig-1")
	if err != nil || seen {
		t.Fatalf("Seen до Mark = %v, %v", seen, err)
	}
	if err := ledger.Mark(ctx, "signal:sig-1"); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	seen, err = ledger.Seen(ctx, "signal:sig-1")
	if err != nil || !seen {
		t.Fatalf("Seen после Mark = %v, %v", seen, err)
	}
	if err := ledger.Mark(ctx, ""); err == nil {
		t.Fatalf("Mark пустого ключа должен падать")
	}
}

// TestLinksContract проверяет регистрацию и разрешение трекинг-ссылки, включая
// переопределение адреса владельцем и ошибку для неизвестной ссылки.
func TestLinksContract(t *testing.T) {
	ctx := context.Background()
	links := postgres.NewLinks(cleanPool(t))

	link, err := domain.NewTransitionLink("lnk-1", "https://example.com", "tenant-1", "company-1")
	if err != nil {
		t.Fatalf("NewTransitionLink: %v", err)
	}
	if err := links.Save(ctx, link); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := links.Resolve(ctx, "lnk-1")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Target != "https://example.com" || got.TenantID != "tenant-1" {
		t.Fatalf("ссылка = %+v", got)
	}
	if _, err := links.Resolve(ctx, "missing"); !errors.Is(err, ports.ErrLinkNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrLinkNotFound", err)
	}
}

// TestUnitOfWorkContract проверяет атомарность транзакции: событие, отметка
// дедупликации и запись outbox фиксируются одной транзакцией, а ошибка use case
// откатывает всё.
func TestUnitOfWorkContract(t *testing.T) {
	ctx := context.Background()
	pool := cleanPool(t)
	uow := postgres.NewUnitOfWork(pool)
	journal := postgres.NewJournal(pool)
	repo := postgres.New(pool)

	event := deliveryEvent(t, "evt-uow")
	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Engagements().Save(ctx, event, 0); err != nil {
			return err
		}
		if err := tx.Signals().Mark(ctx, "signal:sig-uow"); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, event.DomainEvents()...)
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}
	if _, err := repo.FindByID(ctx, "evt-uow"); err != nil {
		t.Fatalf("событие после commit: %v", err)
	}
	ledger := postgres.NewLedger(pool)
	if seen, err := ledger.Seen(ctx, "signal:sig-uow"); err != nil || !seen {
		t.Fatalf("отметка после commit = %v, %v", seen, err)
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("записей outbox = %d, err = %v", len(pending), err)
	}

	rollback := deliveryEvent(t, "evt-rb")
	wantErr := errors.New("отказ после записи")
	err = uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Engagements().Save(ctx, rollback, 0); err != nil {
			return err
		}
		if err := tx.Signals().Mark(ctx, "signal:sig-rb"); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, rollback.DomainEvents()...); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("ошибка = %v, ожидалась %v", err, wantErr)
	}
	if _, err := repo.FindByID(ctx, "evt-rb"); !errors.Is(err, ports.ErrEventNotFound) {
		t.Fatalf("событие после отката: ошибка = %v, ожидалась ErrEventNotFound", err)
	}
	if seen, err := ledger.Seen(ctx, "signal:sig-rb"); err != nil || seen {
		t.Fatalf("отметка после отката = %v, %v", seen, err)
	}
}

// TestJournalContract проверяет журнал outbox: запись, выдачу неподтверждённых
// записей, идемпотентность Append по ID и подтверждение публикации.
func TestJournalContract(t *testing.T) {
	ctx := context.Background()
	journal := postgres.NewJournal(cleanPool(t))

	first := platformoutbox.Message{
		ID:         "msg-1",
		Exchange:   "engagement-observation",
		RoutingKey: "engagement-observed.v1",
		Body:       []byte(`{"event_id":"msg-1"}`),
		Headers:    map[string]string{"event_id": "msg-1"},
	}
	if err := journal.Append(ctx, first); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Повторная запись с тем же ID — no-op.
	if err := journal.Append(ctx, first); err != nil {
		t.Fatalf("повторный Append: %v", err)
	}
	pending, err := journal.Pending(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("записей до подтверждения = %d, err = %v", len(pending), err)
	}
	if pending[0].Headers["event_id"] != "msg-1" {
		t.Fatalf("заголовки записи = %+v", pending[0].Headers)
	}
	if err := journal.MarkPublished(ctx, "msg-1"); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	pending, err = journal.Pending(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("записей после подтверждения = %d, err = %v", len(pending), err)
	}
	if err := journal.MarkPublished(ctx, "missing"); err == nil {
		t.Fatalf("подтверждение неизвестной записи должно падать")
	}
}

// cleanPool возвращает пул с очищенными таблицами сервиса: каждый тест ожидает
// изоляцию прогона.
func cleanPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := testPool(t)
	if _, err := pool.Exec(context.Background(), truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
	return pool
}

// deliveryEvent собирает квалифицированное событие доставки для теста.
func deliveryEvent(t *testing.T, id domain.EngagementEventID) *domain.EngagementEvent {
	t.Helper()
	event, err := domain.Qualify(domain.QualifyParams{
		ID: id,
		Signal: domain.RawSignal{
			SignalID:   domain.SignalID("sig-" + string(id)),
			Source:     "provider",
			TenantID:   "tenant-1",
			CompanyID:  "company-1",
			KindHint:   "delivery",
			Outcome:    "delivered",
			OccurredAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
		EventID: domain.EventID("event-" + string(id)),
	})
	if err != nil {
		t.Fatalf("Qualify: %v", err)
	}
	return event
}

// testPool подключается к базе из окружения и применяет миграции один раз за
// прогон. Без заданной строки подключения тесты пропускаются.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv(dsnEnv)
	if dsn == "" {
		t.Skipf("%s не задан: PostgreSQL-тесты адаптеров пропущены", dsnEnv)
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

// truncateTables очищает таблицы сервиса, сохраняя схему. Ссылочных связей между
// таблицами нет, поэтому обычного TRUNCATE достаточно.
const truncateTables = `TRUNCATE "engagement-observation".engagement_event, "engagement-observation".signal_ledger, "engagement-observation".transition_link, "engagement-observation".outbox`
