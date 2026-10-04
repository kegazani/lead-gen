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

	"lead-gen/backend/contexts/message-delivery/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/message-delivery/internal/domain"
	"lead-gen/backend/contexts/message-delivery/internal/ports"
	"lead-gen/backend/platform/migrate"
)

// PostgreSQL-тесты адаптеров запускаются против реальной базы. Строка
// подключения берётся из переменной окружения LEADGEN_POSTGRES_TEST_DSN; если
// она не задана, тесты пропускаются, поэтому `make check` не требует базы.
//
// Примеры запуска: цель `make test-postgres` поднимает одноразовую базу сама;
// при ручном запуске задайте LEADGEN_POSTGRES_TEST_DSN (порт 5439 в контуре
// разработки).
const dsnEnv = "LEADGEN_POSTGRES_TEST_DSN"

// migrationsDir — каталог SQL-миграций сервиса относительно каталога теста
// (internal/adapters/persistence/postgres). Зона данных у контекста одна — green.
var migrationsDir = filepath.Join("..", "..", "..", "..", "migrations", "green")

var (
	poolOnce sync.Once
	pool     *pgxpool.Pool
	poolErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if pool != nil {
		pool.Close()
	}
	os.Exit(code)
}

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

func truncate(t *testing.T) {
	t.Helper()
	const sql = `TRUNCATE "message-delivery".message, "message-delivery".sender_reputation, "message-delivery".outbox`
	if _, err := testPool(t).Exec(context.Background(), sql); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
}

func newMessage(t *testing.T, id, key string) *domain.Message {
	t.Helper()
	address, err := domain.ParseDeliveryAddress("lead@example.com")
	if err != nil {
		t.Fatal(err)
	}
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	message, err := domain.NewMessage(domain.MessageParams{
		ID:               domain.MessageID(id),
		TenantID:         domain.TenantID("tenant-1"),
		ApprovedActionID: domain.ApprovedActionID("act-1"),
		RecipientRef:     domain.RecipientRef("ref-1"),
		Address:          address,
		SenderDomain:     sender,
		Subject:          "Тема",
		Body:             "Тело",
		IdempotencyKey:   key,
		CreatedAt:        time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	return message
}

func TestMessageRepository_RoundTrip(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	repo := postgres.NewMessageRepository(pool)
	ctx := context.Background()

	message := newMessage(t, "msg-1", "tenant-1|act-1")
	if err := repo.Save(ctx, message); err != nil {
		t.Fatalf("Save: %v", err)
	}

	byID, err := repo.FindByID(ctx, "msg-1")
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if byID.Subject() != "Тема" || byID.Body() != "Тело" {
		t.Fatalf("содержимое не сохранено: %q / %q", byID.Subject(), byID.Body())
	}
	byKey, err := repo.FindByIdempotencyKey(ctx, "tenant-1|act-1")
	if err != nil {
		t.Fatalf("FindByIdempotencyKey: %v", err)
	}
	if byKey.ID() != "msg-1" {
		t.Fatalf("by key: %s", byKey.ID())
	}
	if _, err := repo.FindByID(ctx, "nope"); !errors.Is(err, ports.ErrMessageNotFound) {
		t.Fatalf("отсутствие должно давать ErrMessageNotFound: %v", err)
	}
}

func TestMessageRepository_RejectsDuplicateKey(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	repo := postgres.NewMessageRepository(pool)
	ctx := context.Background()
	if err := repo.Save(ctx, newMessage(t, "msg-1", "tenant-1|act-1")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, newMessage(t, "msg-2", "tenant-1|act-1")); err == nil {
		t.Fatal("ключ идемпотентности должен быть уникален")
	}
}

func TestMessageRepository_ListRetryable(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	repo := postgres.NewMessageRepository(pool)
	ctx := context.Background()

	temporary := newMessage(t, "msg-temporary", "tenant-1|act-temporary")
	startAndRecord(t, temporary, domain.OutcomeTemporary, "mailer-unavailable")
	if err := repo.Save(ctx, temporary); err != nil {
		t.Fatal(err)
	}

	hard := newMessage(t, "msg-hard", "tenant-1|act-hard")
	startAndRecord(t, hard, domain.OutcomeHardBounce, "550")
	if err := repo.Save(ctx, hard); err != nil {
		t.Fatal(err)
	}

	// Рестарт: новые адаптеры на том же пуле видят сохранённое состояние.
	restarted := postgres.NewMessageRepository(pool)
	due, err := restarted.ListRetryable(ctx, 3, 10)
	if err != nil {
		t.Fatalf("ListRetryable: %v", err)
	}
	if len(due) != 1 || due[0].ID() != "msg-temporary" {
		t.Fatalf("ожидалось одно повторяемое сообщение, got %+v", due)
	}

	// Исчерпанный лимит попыток исключает сообщение.
	exhausted, err := restarted.ListRetryable(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListRetryable: %v", err)
	}
	if len(exhausted) != 0 {
		t.Fatalf("исчерпанный лимит не должен возвращать сообщение: %+v", exhausted)
	}
}

func TestReputationRepository_RoundTrip(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	repo := postgres.NewSenderReputationRepository(pool)
	ctx := context.Background()
	sender, err := domain.ParseSenderDomain("mail.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.FindByDomain(ctx, sender); !errors.Is(err, ports.ErrSenderReputationNotFound) {
		t.Fatalf("отсутствие должно давать ErrSenderReputationNotFound: %v", err)
	}
	reputation, err := domain.NewSenderReputation(sender, time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := reputation.ApplyNormalizedOutcome(domain.NormalizedOutcome{Outcome: domain.OutcomeDelivered}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, reputation); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restored, err := postgres.NewSenderReputationRepository(pool).FindByDomain(ctx, sender)
	if err != nil {
		t.Fatalf("FindByDomain: %v", err)
	}
	if restored.DeliveredCount() != 1 {
		t.Fatalf("счётчик доставок: %d", restored.DeliveredCount())
	}
}

func TestUnitOfWork_TransactionalOutbox(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	ctx := context.Background()
	journal := postgres.NewJournal(pool)
	uow := postgres.NewUnitOfWork(pool)
	repo := postgres.NewMessageRepository(pool)

	message := newMessage(t, "msg-1", "tenant-1|act-1")
	if err := message.StartDelivery(time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(domain.OutcomeDelivered, "250", time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Messages().Save(ctx, message); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, message.DomainEvents()...)
	}); err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	// Событие и агрегат зафиксированы атомарно и переживают «рестарт».
	if _, err := repo.FindByID(ctx, "msg-1"); err != nil {
		t.Fatalf("агрегат не сохранён: %v", err)
	}
	pending, err := postgres.NewJournal(pool).Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ожидалось одно событие outbox, got %d", len(pending))
	}
	if _, ok := pending[0].Event.(domain.MessageDelivered); !ok {
		t.Fatalf("ожидалось MessageDelivered, got %T", pending[0].Event)
	}
	if err := journal.MarkPublished(ctx, pending[0].ID); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	remaining, err := journal.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 0 {
		t.Fatalf("подтверждённое событие не должно выдаваться повторно: %d", len(remaining))
	}
}

func TestUnitOfWork_RollbackDropsAggregateAndEvent(t *testing.T) {
	pool := testPool(t)
	truncate(t)
	ctx := context.Background()
	uow := postgres.NewUnitOfWork(pool)
	repo := postgres.NewMessageRepository(pool)
	journal := postgres.NewJournal(pool)

	message := newMessage(t, "msg-rollback", "tenant-1|act-rollback")
	message.StartDelivery(time.Now())
	if err := message.RecordDelivery(domain.OutcomeDelivered, "250", time.Now()); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("откат")
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Messages().Save(ctx, message); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, message.DomainEvents()...); err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("ожидалась ошибка отката, got %v", err)
	}

	if _, err := repo.FindByID(ctx, "msg-rollback"); !errors.Is(err, ports.ErrMessageNotFound) {
		t.Fatalf("откат не должен сохранять агрегат: %v", err)
	}
	pending, err := journal.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("откат не должен сохранять события: %d", len(pending))
	}
}

// startAndRecord переводит сообщение в терминальное состояние с заданным исходом.
func startAndRecord(t *testing.T, message *domain.Message, outcome domain.AttemptOutcome, code string) {
	t.Helper()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if err := message.StartDelivery(now); err != nil {
		t.Fatal(err)
	}
	if err := message.RecordDelivery(outcome, code, now); err != nil {
		t.Fatal(err)
	}
	message.PullDomainEvents()
}
