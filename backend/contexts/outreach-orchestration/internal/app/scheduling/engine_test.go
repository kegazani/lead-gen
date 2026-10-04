package scheduling

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Локальные тест-дубли: тесты движка не зависят от adapters.

var engineNow = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type recordingOutbox struct{ events []domain.DomainEvent }

func (o *recordingOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

type engineTransaction struct {
	repo   *lister
	outbox *recordingOutbox
}

func (t *engineTransaction) Campaigns() ports.CampaignRepository { return t.repo }
func (t *engineTransaction) Outbox() ports.Outbox                { return t.outbox }

type engineUnitOfWork struct {
	repo   *lister
	outbox *recordingOutbox
}

func (u *engineUnitOfWork) WithinTransaction(ctx context.Context, fn func(ports.Transaction) error) error {
	return fn(&engineTransaction{repo: u.repo, outbox: u.outbox})
}

// lister реализует и ports.CampaignLister, и ports.CampaignRepository.
type lister struct {
	ids       []domain.CampaignID
	campaigns map[domain.CampaignID]*domain.Campaign
}

func newLister(campaigns ...*domain.Campaign) *lister {
	l := &lister{campaigns: make(map[domain.CampaignID]*domain.Campaign)}
	for _, campaign := range campaigns {
		l.campaigns[campaign.ID()] = campaign
		l.ids = append(l.ids, campaign.ID())
	}
	return l
}

func (l *lister) ListCampaignIDs(context.Context) ([]domain.CampaignID, error) {
	return l.ids, nil
}

func (l *lister) Save(_ context.Context, campaign *domain.Campaign) error {
	l.campaigns[campaign.ID()] = campaign
	return nil
}

func (l *lister) FindByID(_ context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	campaign, ok := l.campaigns[id]
	if !ok {
		return nil, ports.ErrCampaignNotFound
	}
	return campaign, nil
}

func engineStep(t *testing.T) domain.SequenceStep {
	t.Helper()
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
	return step
}

func engineCampaign(t *testing.T) *domain.Campaign {
	t.Helper()
	window, err := domain.NewCampaignWindow(engineNow, engineNow.Add(72*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	campaign, err := domain.CreateCampaign(domain.CreateCampaignParams{
		ID:       "campaign-1",
		TenantID: "tenant-1",
		Name:     "Кампания движка",
		Window:   window,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if _, err := campaign.DefineSequence(domain.SequenceDefinitionParams{
		ID:       "sequence-1",
		Campaign: campaign.ID(),
		Tenant:   campaign.TenantID(),
		Name:     "Сценарий",
		Steps:    []domain.SequenceStep{engineStep(t), engineStep(t)},
	}); err != nil {
		t.Fatalf("DefineSequence: %v", err)
	}
	if _, err := campaign.EnrollLead(domain.EnrollmentParams{
		ExecutionID: "execution-1",
		SequenceID:  "sequence-1",
		TenantID:    campaign.TenantID(),
		Lead:        "lead-1",
		Recipient:   "lead-1",
		At:          engineNow,
	}); err != nil {
		t.Fatalf("EnrollLead: %v", err)
	}
	campaign.PullDomainEvents()
	return campaign
}

func TestEngineFormsDraftAndReportsStall(t *testing.T) {
	campaign := engineCampaign(t)
	repo := newLister(campaign)
	outbox := &recordingOutbox{}
	engine, err := NewEngine(repo, &engineUnitOfWork{repo: repo, outbox: outbox}, fixedClock{now: engineNow}, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	report, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Drafted != 1 {
		t.Fatalf("ожидался один сформированный черновик, получено %d", report.Drafted)
	}
	if len(campaign.PendingApproval()) != 1 {
		t.Fatalf("черновик должен ждать утверждения, получено %d", len(campaign.PendingApproval()))
	}
	if len(outbox.events) == 0 {
		t.Fatal("событие формирования черновика должно попасть в outbox")
	}

	// Без утверждения следующий проход фиксирует простой и не продвигает цепочку.
	report, err = engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if len(report.Stalled) != 1 {
		t.Fatalf("ожидался простой по кампании, получено %d", len(report.Stalled))
	}
	if report.Advanced != 0 {
		t.Fatalf("без утверждения цепочка не продвигается, advanced=%d", report.Advanced)
	}
}

func TestEngineAdvancesAfterApproval(t *testing.T) {
	campaign := engineCampaign(t)
	repo := newLister(campaign)
	engine, err := NewEngine(repo, &engineUnitOfWork{repo: repo, outbox: &recordingOutbox{}}, fixedClock{now: engineNow}, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := engine.RunOnce(context.Background()); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if _, _, err := campaign.ApproveDraft(domain.DecisionParams{
		DecisionID:   "decision-1",
		DraftID:      "execution-1.step-0",
		DraftVersion: 1,
		Actor:        "operator-1",
		DecidedAt:    engineNow,
	}); err != nil {
		t.Fatalf("ApproveDraft: %v", err)
	}

	report, err := engine.RunOnce(context.Background())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if report.Advanced != 1 {
		t.Fatalf("после утверждения цепочка должна продвинуться, advanced=%d", report.Advanced)
	}
	if report.Drafted != 1 {
		t.Fatalf("следующий шаг без задержки должен быть сформирован, drafted=%d", report.Drafted)
	}
}

func TestEngineSkipsMissingCampaign(t *testing.T) {
	repo := &lister{ids: []domain.CampaignID{"campaign-missing"}, campaigns: map[domain.CampaignID]*domain.Campaign{}}
	engine, err := NewEngine(repo, &engineUnitOfWork{repo: repo, outbox: &recordingOutbox{}}, fixedClock{now: engineNow}, domain.DefaultParticipationLimits())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if _, err := engine.RunOnce(context.Background()); err != nil {
		t.Fatalf("отсутствующая кампания не должна ломать проход: %v", err)
	}
}
