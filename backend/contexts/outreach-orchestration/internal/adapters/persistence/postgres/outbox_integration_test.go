package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"lead-gen/backend/contexts/outreach-orchestration/internal/adapters/persistence/postgres"
	"lead-gen/backend/contexts/outreach-orchestration/internal/app/scheduling"
	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// PostgreSQL-suite транзакции и outbox: проверяет, что агрегат и его события
// пишутся атомарно через ports.UnitOfWork, что события переживают рестарт (кодек
// хранения) и что проход движка последовательностей сохраняет результат через
// тот же порт, а не только на in-memory-реализации. Тесты пропускаются без
// LEADGEN_POSTGRES_TEST_DSN, как и остальные PG-suite.

var atomicityNow = time.Date(2026, time.March, 4, 12, 0, 0, 0, time.UTC)

// fixedEngineClock — часы с фиксированным моментом для порта Clock.
type fixedEngineClock struct{ now time.Time }

func (c fixedEngineClock) Now() time.Time { return c.now }

// cleanTables очищает кампании и outbox между подтестами.
func cleanTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), truncateTables); err != nil {
		t.Fatalf("очистка таблиц: %v", err)
	}
}

// mustCampaign создаёт кампанию в окне для PG-тестов.
func mustCampaign(t *testing.T, id domain.CampaignID) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(atomicityNow, atomicityNow.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       id,
		TenantID: "tenant-1",
		Name:     "Кампания транзакции",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	return campaign
}

// mustApprovedEvent собирает событие approved-action для проверки кодека outbox.
func mustApprovedEvent(t *testing.T, campaign domain.CampaignID) domain.ActionApproved {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return domain.ActionApproved{
		CampaignID:   campaign,
		TenantID:     "tenant-1",
		DraftID:      "draft-1",
		DraftVersion: 1,
		DecisionID:   "decision-1",
		Actor:        "operator-1",
		Channel:      channel,
		Material:     "asset-1@ver-1",
		At:           atomicityNow,
	}
}

// TestUnitOfWorkCommitsCampaignAndEventAtomically проверяет, что успешная
// транзакция сохраняет агрегат и его событие вместе, а журнал отдаёт событие
// обратно восстановленным (кодек хранения).
func TestUnitOfWorkCommitsCampaignAndEventAtomically(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	cleanTables(t, pool)

	uow := postgres.NewUnitOfWork(pool)
	journal := postgres.NewJournal(pool)
	repo := postgres.New(pool)

	campaign := mustCampaign(t, "campaign-uow")
	event := mustApprovedEvent(t, campaign.ID())

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if _, err := tx.Campaigns().FindByID(ctx, campaign.ID()); !errors.Is(err, ports.ErrCampaignNotFound) {
			return fmt.Errorf("до записи кампания должна отсутствовать: %v", err)
		}
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, event)
	})
	if err != nil {
		t.Fatalf("WithinTransaction: %v", err)
	}

	if _, err := repo.FindByID(ctx, campaign.ID()); err != nil {
		t.Fatalf("кампания не сохранена: %v", err)
	}

	pending, err := journal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("ожидалось одно событие в outbox, получено %d", len(pending))
	}
	got, ok := pending[0].Event.(domain.ActionApproved)
	if !ok {
		t.Fatalf("событие восстановлено как %T, ожидался domain.ActionApproved", pending[0].Event)
	}
	if got.Channel != event.Channel || got.DraftVersion != event.DraftVersion ||
		got.DecisionID != event.DecisionID || got.Material != event.Material || !got.At.Equal(event.At) {
		t.Fatalf("событие не пережило круговой обход: %+v", got)
	}

	if err := journal.MarkPublished(ctx, pending[0].ID); err != nil {
		t.Fatalf("MarkPublished: %v", err)
	}
	pending, err = journal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending после подтверждения: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("после подтверждения outbox должен быть пуст, осталось %d", len(pending))
	}
}

// TestUnitOfWorkRollsBackCampaignAndEvent проверяет, что ошибка транзакции не
// оставляет ни агрегата, ни события outbox.
func TestUnitOfWorkRollsBackCampaignAndEvent(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	cleanTables(t, pool)

	uow := postgres.NewUnitOfWork(pool)
	journal := postgres.NewJournal(pool)
	repo := postgres.New(pool)

	campaign := mustCampaign(t, "campaign-rollback")
	event := mustApprovedEvent(t, campaign.ID())
	sentinel := errors.New("откат транзакции")

	err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		if err := tx.Campaigns().Save(ctx, campaign); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("ожидалась ошибка транзакции, получено %v", err)
	}
	if _, err := repo.FindByID(ctx, campaign.ID()); !errors.Is(err, ports.ErrCampaignNotFound) {
		t.Fatalf("откат должен удалить кампанию, получено %v", err)
	}
	pending, err := journal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) != 0 {
		t.Fatalf("откат не должен оставлять событий, осталось %d", len(pending))
	}
}

// TestSequenceEnginePersistsThroughPort прогоняет проход движка последовательностей
// против PostgreSQL-адаптеров: агрегат и событие формирования черновика
// сохраняются через ports.UnitOfWork и переживают загрузку из базы.
func TestSequenceEnginePersistsThroughPort(t *testing.T) {
	ctx := context.Background()
	pool := testPool(t)
	cleanTables(t, pool)

	repo := postgres.New(pool)
	uow := postgres.NewUnitOfWork(pool)
	journal := postgres.NewJournal(pool)

	campaign := engineCampaignForPG(t)
	if err := uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Campaigns().Save(ctx, campaign)
	}); err != nil {
		t.Fatalf("сохранение исходной кампании: %v", err)
	}

	engine, err := scheduling.NewEngine(repo, uow, fixedEngineClock{now: atomicityNow}, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	report, err := engine.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Drafted != 1 {
		t.Fatalf("ожидался один сформированный черновик, получено %d", report.Drafted)
	}

	loaded, err := repo.FindByID(ctx, campaign.ID())
	if err != nil {
		t.Fatalf("загрузка кампании после прохода: %v", err)
	}
	if len(loaded.PendingApproval()) != 1 {
		t.Fatalf("черновик шага должен ждать утверждения, получено %d", len(loaded.PendingApproval()))
	}

	pending, err := journal.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if len(pending) == 0 {
		t.Fatal("событие формирования черновика должно быть записано в outbox")
	}
	foundOutcome := false
	for _, message := range pending {
		if _, ok := message.Event.(domain.OutreachOutcomeRecorded); ok {
			foundOutcome = true
		}
	}
	if !foundOutcome {
		t.Fatalf("в outbox ожидалось событие outreach-outcome-recorded, получено %d записей", len(pending))
	}
}

// engineCampaignForPG собирает кампанию с активным участием, как это делает
// движок в срезе: сценарий из двух шагов и добавленный лид. Буфер событий
// очищается, чтобы в базу попало только исходное состояние.
func engineCampaignForPG(t *testing.T) *domain.Campaign {
	t.Helper()
	campaign := mustCampaign(t, "campaign-engine")

	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	step, err := domain.NewSequenceStep(domain.SequenceStepParams{
		Action:   channel,
		Material: "material-1",
		Segment:  "segment-1",
	})
	if err != nil {
		t.Fatalf("NewSequenceStep: %v", err)
	}
	if _, err := campaign.DefineSequence(domain.SequenceDefinitionParams{
		ID:       "sequence-1",
		Campaign: campaign.ID(),
		Tenant:   campaign.TenantID(),
		Name:     "Сценарий",
		Steps:    []domain.SequenceStep{step, step},
	}); err != nil {
		t.Fatalf("DefineSequence: %v", err)
	}
	if _, err := campaign.EnrollLead(domain.EnrollmentParams{
		ExecutionID: "execution-1",
		SequenceID:  "sequence-1",
		TenantID:    campaign.TenantID(),
		Lead:        "lead-1",
		Recipient:   "lead-1",
		At:          atomicityNow,
	}); err != nil {
		t.Fatalf("EnrollLead: %v", err)
	}
	campaign.PullDomainEvents()
	return campaign
}
