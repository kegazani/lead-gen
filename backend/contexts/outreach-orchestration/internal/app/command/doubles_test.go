package command

import (
	"context"
	"testing"
	"time"

	"lead-gen/backend/contexts/outreach-orchestration/internal/domain"
	"lead-gen/backend/contexts/outreach-orchestration/internal/ports"
)

// Локальные тест-дубли портов: app-тесты не зависят от adapters и проверяют
// сценарии на управляемых времени, доступе, репозитории и outbox.

func testNow() time.Time {
	return time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
}

func testWindow(t *testing.T) domain.CampaignWindow {
	t.Helper()
	window, err := domain.NewCampaignWindow(testNow(), testNow().Add(24*time.Hour))
	if err != nil {
		t.Fatalf("NewCampaignWindow: %v", err)
	}
	return window
}

func testSpec(t *testing.T) domain.DraftSpec {
	t.Helper()
	channel, err := domain.ParseActionChannel(domain.ActionChannelEmailName)
	if err != nil {
		t.Fatalf("ParseActionChannel: %v", err)
	}
	return domain.DraftSpec{
		Material:  domain.MaterialID("material-1"),
		Segment:   domain.SegmentID("segment-1"),
		Recipient: domain.RecipientID("recipient-1"),
		Channel:   channel,
	}
}

type fakeAccess struct {
	allowed bool
	err     error
	calls   int
}

func (f *fakeAccess) CanAct(_ context.Context, _ domain.ActorID, _ domain.TenantID) (bool, error) {
	f.calls++
	return f.allowed, f.err
}

type fixedClock struct{ now time.Time }

func (c *fixedClock) Now() time.Time { return c.now }

type fakeRepository struct {
	campaigns map[domain.CampaignID]*domain.Campaign
	saves     int
	findErr   error
}

func newFakeRepository() *fakeRepository {
	return &fakeRepository{campaigns: make(map[domain.CampaignID]*domain.Campaign)}
}

func (r *fakeRepository) Save(_ context.Context, campaign *domain.Campaign) error {
	r.campaigns[campaign.ID()] = campaign
	r.saves++
	return nil
}

func (r *fakeRepository) FindByID(_ context.Context, id domain.CampaignID) (*domain.Campaign, error) {
	if r.findErr != nil {
		return nil, r.findErr
	}
	campaign, ok := r.campaigns[id]
	if !ok {
		return nil, ports.ErrCampaignNotFound
	}
	return campaign, nil
}

type fakeOutbox struct {
	events []domain.DomainEvent
}

func (o *fakeOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	o.events = append(o.events, events...)
	return nil
}

type transactionOutbox struct {
	err      error
	buffered []domain.DomainEvent
}

func (o *transactionOutbox) Append(_ context.Context, events ...domain.DomainEvent) error {
	if o.err != nil {
		return o.err
	}
	o.buffered = append(o.buffered, events...)
	return nil
}

type fakeTransaction struct {
	repo   *fakeRepository
	outbox *transactionOutbox
}

func (t *fakeTransaction) Campaigns() ports.CampaignRepository { return t.repo }
func (t *fakeTransaction) Outbox() ports.Outbox                { return t.outbox }

type fakeUnitOfWork struct {
	repo        *fakeRepository
	outbox      *fakeOutbox
	txOutboxErr error
	commits     int
	rollbacks   int
}

func newFakeUnitOfWork(repo *fakeRepository, outbox *fakeOutbox) *fakeUnitOfWork {
	return &fakeUnitOfWork{repo: repo, outbox: outbox}
}

func (u *fakeUnitOfWork) WithinTransaction(ctx context.Context, fn func(ports.Transaction) error) error {
	snapshot := make(map[domain.CampaignID]*domain.Campaign, len(u.repo.campaigns))
	for id, campaign := range u.repo.campaigns {
		snapshot[id] = campaign
	}

	txOutbox := &transactionOutbox{err: u.txOutboxErr}
	if err := fn(&fakeTransaction{repo: u.repo, outbox: txOutbox}); err != nil {
		u.repo.campaigns = snapshot
		u.rollbacks++
		return err
	}
	u.commits++
	u.outbox.events = append(u.outbox.events, txOutbox.buffered...)
	return nil
}

// testKit — общий набор дублей для сценариев command.
type testKit struct {
	repo   *fakeRepository
	outbox *fakeOutbox
	uow    *fakeUnitOfWork
	access *fakeAccess
	clock  *fixedClock
}

func newTestKit(t *testing.T) *testKit {
	t.Helper()
	repo := newFakeRepository()
	outbox := &fakeOutbox{}
	return &testKit{
		repo:   repo,
		outbox: outbox,
		uow:    newFakeUnitOfWork(repo, outbox),
		access: &fakeAccess{allowed: true},
		clock:  &fixedClock{now: testNow()},
	}
}

func createCommand(t *testing.T) CreateCampaign {
	t.Helper()
	return CreateCampaign{
		CommandID:  domain.CommandID("command-1"),
		CampaignID: domain.CampaignID("campaign-1"),
		TenantID:   domain.TenantID("tenant-1"),
		Actor:      domain.ActorID("operator-1"),
		Name:       "Кампания среза",
		Window:     testWindow(t),
	}
}

func (k *testKit) draftCommand(t *testing.T, spec domain.DraftSpec) DraftAction {
	t.Helper()
	return DraftAction{
		CampaignID: domain.CampaignID("campaign-1"),
		TenantID:   domain.TenantID("tenant-1"),
		Actor:      domain.ActorID("operator-1"),
		DraftID:    domain.DraftID("draft-1"),
		Spec:       spec,
	}
}

func (k *testKit) mustCreate(t *testing.T) CreateCampaignResult {
	t.Helper()
	result, err := NewCreateCampaignHandler(k.access, k.uow).Handle(context.Background(), createCommand(t))
	if err != nil {
		t.Fatalf("CreateCampaign.Handle: %v", err)
	}
	return result
}

func (k *testKit) mustDraft(t *testing.T, spec domain.DraftSpec) DraftActionResult {
	t.Helper()
	result, err := NewDraftActionHandler(k.access, k.clock, k.uow).Handle(context.Background(), k.draftCommand(t, spec))
	if err != nil {
		t.Fatalf("DraftAction.Handle: %v", err)
	}
	return result
}

func (k *testKit) campaign(t *testing.T) *domain.Campaign {
	t.Helper()
	campaign, err := k.repo.FindByID(context.Background(), domain.CampaignID("campaign-1"))
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	return campaign
}

func countApprovedEvents(events []domain.DomainEvent) int {
	n := 0
	for _, event := range events {
		if _, ok := event.(domain.ActionApproved); ok {
			n++
		}
	}
	return n
}
