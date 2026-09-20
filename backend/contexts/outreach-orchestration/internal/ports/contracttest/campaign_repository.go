// Package contracttest содержит общий contract suite портов контекста
// outreach-orchestration. Suite запускается против любой реализации через
// фабрику и потому не зависит от адаптера: in-memory и PostgreSQL проверяются
// одним и тем же набором проверок.
//
// Пакет предназначен для использования из тестов адаптеров и импортирует
// только сам ports и его domain.
package contracttest

import (
	"context"
	"errors"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// CampaignRepositoryFactory создаёт репозиторий для одного прогона suite.
//
// Фабрика обязана вернуть репозиторий с пустым состоянием. Для общей
// PostgreSQL-базы она же отвечает за изоляцию прогонов (очистку или
// собственную транзакцию): suite вызывает фабрику перед каждым подтестом и
// ничего не знает о хранилище.
type CampaignRepositoryFactory func(t *testing.T) ports.CampaignRepository

// baseTime — детерминированная точка отсчёта, чтобы проверки не зависели от
// системных часов и совпадали для всех реализаций.
var baseTime = time.Date(2025, time.January, 2, 10, 0, 0, 0, time.UTC)

// RunCampaignRepositoryContract проверяет, что реализация CampaignRepository
// соблюдает контракт порта: сохраняет и загружает агрегат, различает отсутствие
// сущности и сохраняет версию черновика и журнал решений.
func RunCampaignRepositoryContract(t *testing.T, newRepository CampaignRepositoryFactory) {
	t.Helper()
	ctx := context.Background()

	t.Run("сохранение и загрузка", func(t *testing.T) {
		repository := newRepository(t)
		campaign := mustCampaign(t, "campaign-roundtrip")
		if _, _, err := campaign.DraftAction("draft-1", sampleSpec(t), at(1)); err != nil {
			t.Fatalf("постановка черновика: %v", err)
		}
		if err := repository.Save(ctx, campaign); err != nil {
			t.Fatalf("Save: %v", err)
		}
		loaded, err := repository.FindByID(ctx, campaign.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		assertCampaignSnapshot(t, loaded, campaign)
	})

	t.Run("отсутствие сущности", func(t *testing.T) {
		repository := newRepository(t)
		loaded, err := repository.FindByID(ctx, "campaign-missing")
		if err == nil {
			t.Fatal("FindByID отсутствующей кампании вернул nil-ошибку")
		}
		if !errors.Is(err, ports.ErrCampaignNotFound) {
			t.Fatalf("ошибка %v не удовлетворяет errors.Is(err, ErrCampaignNotFound)", err)
		}
		if loaded != nil {
			t.Fatalf("для отсутствующей кампании ожидался nil, получено %v", loaded)
		}
	})

	t.Run("версия черновика и решения", func(t *testing.T) {
		repository := newRepository(t)
		campaign := mustCampaign(t, "campaign-decisions")
		if _, _, err := campaign.DraftAction("draft-1", sampleSpec(t), at(1)); err != nil {
			t.Fatalf("постановка черновика: %v", err)
		}
		approve := domain.DecisionParams{
			DecisionID:   "decision-1",
			DraftID:      "draft-1",
			DraftVersion: 1,
			Actor:        "actor-1",
			DecidedAt:    at(2),
		}
		if _, _, err := campaign.ApproveDraft(approve); err != nil {
			t.Fatalf("утверждение черновика: %v", err)
		}
		if err := repository.Save(ctx, campaign); err != nil {
			t.Fatalf("Save: %v", err)
		}

		loaded, err := repository.FindByID(ctx, campaign.ID())
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		draft, ok := loaded.Draft("draft-1")
		if !ok {
			t.Fatal("после загрузки черновик draft-1 не найден")
		}
		if draft.Version() != 1 {
			t.Fatalf("версия черновика: получено %d, ожидалось 1", draft.Version())
		}
		if draft.State() != domain.DraftStateApproved {
			t.Fatalf("состояние черновика: получено %q, ожидалось %q", draft.State(), domain.DraftStateApproved)
		}
		decisions := loaded.DecisionsForDraft("draft-1")
		if len(decisions) != 1 {
			t.Fatalf("число решений: получено %d, ожидалось 1", len(decisions))
		}
		assertDecision(t, decisions[0], "decision-1", "draft-1", 1, "actor-1", domain.DecisionApproved, at(2))

		// Доработка повышает версию. Прежнее решение остаётся в журнале, но к
		// новой версии не относится, поэтому исполнение без нового решения
		// отвергается (инварианты I1 и I2).
		if _, err := loaded.ReviseDraft("draft-1", 1, revisedSpec(t)); err != nil {
			t.Fatalf("доработка черновика: %v", err)
		}
		if err := repository.Save(ctx, loaded); err != nil {
			t.Fatalf("повторный Save: %v", err)
		}
		reloaded, err := repository.FindByID(ctx, campaign.ID())
		if err != nil {
			t.Fatalf("повторный FindByID: %v", err)
		}
		revised, ok := reloaded.Draft("draft-1")
		if !ok {
			t.Fatal("после повторной загрузки черновик draft-1 не найден")
		}
		if revised.Version() != 2 {
			t.Fatalf("версия черновика: получено %d, ожидалось 2", revised.Version())
		}
		if revised.State() != domain.DraftStatePendingApproval {
			t.Fatalf("состояние черновика: получено %q, ожидалось %q", revised.State(), domain.DraftStatePendingApproval)
		}
		if got := reloaded.DecisionsForDraft("draft-1"); len(got) != 1 || got[0].DraftVersion() != 1 {
			t.Fatalf("журнал решений после доработки: %v, ожидалось одно решение по версии 1", got)
		}
		if err := reloaded.ExecuteDraft("draft-1", 2); !errors.Is(err, domain.ErrApprovalRequired) {
			t.Fatalf("исполнение новой версии без решения: получено %v, ожидалась ErrApprovalRequired", err)
		}
	})
}

// mustCampaign создаёт кампанию с детерминированными полями или завершает тест.
func mustCampaign(t *testing.T, id domain.CampaignID) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(baseTime, baseTime.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("окно кампании: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       id,
		TenantID: "tenant-1",
		Name:     "Кампания " + id.String(),
		Window:   window,
	})
	if err != nil {
		t.Fatalf("создание кампании: %v", err)
	}
	return campaign
}

// sampleSpec возвращает состав черновика для исходной постановки.
func sampleSpec(t *testing.T) domain.DraftSpec {
	t.Helper()
	return draftSpec(t, domain.ActionChannelEmailName)
}

// revisedSpec возвращает состав доработанного черновика; канал отличается,
// чтобы версия 2 была отличима от версии 1 по всем полям.
func revisedSpec(t *testing.T) domain.DraftSpec {
	t.Helper()
	return draftSpec(t, domain.ActionChannelCallName)
}

func draftSpec(t *testing.T, channelName string) domain.DraftSpec {
	t.Helper()
	channel, err := domain.ParseActionChannel(channelName)
	if err != nil {
		t.Fatalf("канал действия: %v", err)
	}
	return domain.DraftSpec{
		Material:  "material-1",
		Segment:   "segment-1",
		Recipient: "recipient-1",
		Channel:   channel,
	}
}

// at возвращает момент времени, сдвинутый на hours часов от baseTime.
func at(hours int) time.Time {
	return baseTime.Add(time.Duration(hours) * time.Hour)
}

// assertCampaignSnapshot сравнивает загруженный агрегат с ожидаемым по
// наблюдаемому через публичный API состоянию. Буфер неподтверждённых событий не
// сравнивается: события вычитываются use case и пишутся в outbox, а не
// являются частью сохраняемого состояния агрегата.
func assertCampaignSnapshot(t *testing.T, got, want *domain.Campaign) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.TenantID() != want.TenantID() {
		t.Fatalf("TenantID: получено %q, ожидалось %q", got.TenantID(), want.TenantID())
	}
	if got.Name() != want.Name() {
		t.Fatalf("Name: получено %q, ожидалось %q", got.Name(), want.Name())
	}
	if !got.Window().Equals(want.Window()) {
		t.Fatalf("Window: получено %s, ожидалось %s", got.Window(), want.Window())
	}
	if got.State() != want.State() {
		t.Fatalf("State: получено %q, ожидалось %q", got.State(), want.State())
	}
	gotDrafts, wantDrafts := got.Drafts(), want.Drafts()
	if len(gotDrafts) != len(wantDrafts) {
		t.Fatalf("число черновиков: получено %d, ожидалось %d", len(gotDrafts), len(wantDrafts))
	}
	for i := range wantDrafts {
		assertDraft(t, gotDrafts[i], wantDrafts[i])
	}
}

func assertDraft(t *testing.T, got, want *domain.ActionDraft) {
	t.Helper()
	if got.ID() != want.ID() {
		t.Fatalf("Draft.ID: получено %q, ожидалось %q", got.ID(), want.ID())
	}
	if got.CampaignID() != want.CampaignID() {
		t.Fatalf("Draft.CampaignID: получено %q, ожидалось %q", got.CampaignID(), want.CampaignID())
	}
	if got.State() != want.State() {
		t.Fatalf("Draft.State: получено %q, ожидалось %q", got.State(), want.State())
	}
	if got.Version() != want.Version() {
		t.Fatalf("Draft.Version: получено %d, ожидалось %d", got.Version(), want.Version())
	}
	assertSpec(t, got.Spec(), want.Spec())
}

func assertSpec(t *testing.T, got, want domain.DraftSpec) {
	t.Helper()
	if got.Material != want.Material {
		t.Fatalf("Draft.Material: получено %q, ожидалось %q", got.Material, want.Material)
	}
	if got.Segment != want.Segment {
		t.Fatalf("Draft.Segment: получено %q, ожидалось %q", got.Segment, want.Segment)
	}
	if got.Recipient != want.Recipient {
		t.Fatalf("Draft.Recipient: получено %q, ожидалось %q", got.Recipient, want.Recipient)
	}
	if !got.Channel.Equals(want.Channel) {
		t.Fatalf("Draft.Channel: получено %q, ожидалось %q", got.Channel, want.Channel)
	}
}

func assertDecision(
	t *testing.T,
	got domain.OperatorDecision,
	id domain.DecisionID,
	draft domain.DraftID,
	version int,
	actor domain.ActorID,
	outcome domain.DecisionOutcome,
	decidedAt time.Time,
) {
	t.Helper()
	if got.ID() != id {
		t.Fatalf("Decision.ID: получено %q, ожидалось %q", got.ID(), id)
	}
	if got.DraftID() != draft {
		t.Fatalf("Decision.DraftID: получено %q, ожидалось %q", got.DraftID(), draft)
	}
	if got.DraftVersion() != version {
		t.Fatalf("Decision.DraftVersion: получено %d, ожидалось %d", got.DraftVersion(), version)
	}
	if got.Actor() != actor {
		t.Fatalf("Decision.Actor: получено %q, ожидалось %q", got.Actor(), actor)
	}
	if got.Outcome() != outcome {
		t.Fatalf("Decision.Outcome: получено %q, ожидалось %q", got.Outcome(), outcome)
	}
	if !got.DecidedAt().Equal(decidedAt) {
		t.Fatalf("Decision.DecidedAt: получено %s, ожидалось %s", got.DecidedAt(), decidedAt)
	}
}
