// Package editorialreview реализует отдельное чтение и решение по материалам
// редакторской кампании. Пакет не вызывает risk screening или delivery ports.
package editorialreview

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"lead-gen/contracts/content-production/contentversion"
	"lead-gen/contracts/outreach-orchestration/editorialreview"
)

var (
	ErrConflict = errors.New("editorialreview: stale version or conflicting decision")
	ErrNotFound = editorialreview.ErrNotFound
)

const EventTypeDecisionRecorded = "editorial-decision-recorded"

type Item struct {
	LeadID      string `json:"lead_id"`
	MaterialRef string `json:"material_ref"`
	AssetID     string `json:"asset_id"`
	VersionID   string `json:"version_id"`
}

type Decision struct {
	Outcome   string    `json:"outcome"`
	Reason    string    `json:"reason"`
	Actor     string    `json:"actor"`
	At        time.Time `json:"decided_at"`
	VersionID string    `json:"version_id"`
}

type ItemRecord struct {
	Item
	Decision *Decision `json:"decision,omitempty"`
}

type Campaign struct {
	CampaignID string       `json:"campaign_id"`
	TenantID   string       `json:"tenant_id"`
	RequestID  string       `json:"request_id"`
	Purpose    string       `json:"purpose"`
	Items      []ItemRecord `json:"items"`
}

type Handoff struct {
	CampaignID string
	TenantID   string
	RequestID  string
	Items      []Item
}

type Decide struct {
	TenantID        string
	CampaignID      string
	LeadID          string
	MaterialRef     string
	ExpectedVersion string
	Outcome         string
	Reason          string
	Actor           string
	At              time.Time
}

// DecisionEvent — durable событие фиксации редакторского решения; единственный
// event type этого контура не используется для delivery.
type DecisionEvent struct {
	EventType   string    `json:"event_type"`
	CampaignID  string    `json:"campaign_id"`
	TenantID    string    `json:"tenant_id"`
	LeadID      string    `json:"lead_id"`
	MaterialRef string    `json:"material_ref"`
	VersionID   string    `json:"version_id"`
	Outcome     string    `json:"outcome"`
	Reason      string    `json:"reason"`
	Actor       string    `json:"actor"`
	At          time.Time `json:"occurred_at"`
}

// Store фиксирует editorial campaign и ее решения; реализации должны атомарно
// сериализовать конкурентные Decide для одного материала.
type Store interface {
	Submit(context.Context, Handoff) (Campaign, bool, error)
	Get(context.Context, editorialreview.Query) (ItemRecord, string, error)
	Decide(context.Context, Decide) (ItemRecord, string, error)
	Events(context.Context, string) ([]DecisionEvent, error)
}

type Service struct {
	store    Store
	versions contentversion.Source
	clock    func() time.Time
}

func New(store Store, versions contentversion.Source, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	return &Service{store: store, versions: versions, clock: clock}
}

func (s *Service) Submit(ctx context.Context, h Handoff) (Campaign, bool, error) {
	if s == nil || s.store == nil {
		return Campaign{}, false, editorialreview.ErrUnavailable
	}
	return s.store.Submit(ctx, h)
}

func (s *Service) Read(ctx context.Context, q editorialreview.Query) (editorialreview.Record, error) {
	if s == nil || s.store == nil || s.versions == nil {
		return editorialreview.Record{}, editorialreview.ErrUnavailable
	}
	item, campaignID, err := s.store.Get(ctx, q)
	if err != nil {
		return editorialreview.Record{}, err
	}
	v, err := s.versions.Version(ctx, contentversion.Query{TenantID: q.TenantID, AssetID: item.AssetID, VersionID: item.VersionID})
	if err != nil {
		if errors.Is(err, contentversion.ErrNotFound) {
			return editorialreview.Record{}, editorialreview.ErrNotFound
		}
		return editorialreview.Record{}, fmt.Errorf("%w: чтение точной версии", editorialreview.ErrUnavailable)
	}
	if v.VersionID != item.VersionID || v.AssetID != item.AssetID {
		return editorialreview.Record{}, editorialreview.ErrNotFound
	}
	q.CampaignID = campaignID
	record := editorialreview.Record{Query: q, Subject: v.Subject, Body: v.Body}
	if item.Decision != nil {
		record.Decision = &editorialreview.Decision{EventType: EventTypeDecisionRecorded, Outcome: item.Decision.Outcome, Reason: item.Decision.Reason, Actor: item.Decision.Actor, DecidedAt: item.Decision.At.UTC().Format(time.RFC3339Nano), VersionID: item.Decision.VersionID, MaterialRef: item.MaterialRef}
	}
	return record, nil
}

func (s *Service) Decide(ctx context.Context, c editorialreview.Command) (editorialreview.Record, error) {
	if c.Outcome != "approved" && c.Outcome != "rejected" {
		return editorialreview.Record{}, editorialreview.ErrRejected
	}
	if strings.TrimSpace(c.Actor) == "" || strings.TrimSpace(c.ExpectedVersion) == "" || c.ExpectedVersion != c.VersionID {
		return editorialreview.Record{}, ErrConflict
	}
	if c.Outcome == "rejected" && strings.TrimSpace(c.Reason) == "" {
		return editorialreview.Record{}, editorialreview.ErrRejected
	}
	_, _, err := s.store.Decide(ctx, Decide{TenantID: c.TenantID, CampaignID: c.CampaignID, LeadID: c.LeadID, MaterialRef: c.MaterialRef, ExpectedVersion: c.ExpectedVersion, Outcome: c.Outcome, Reason: c.Reason, Actor: c.Actor, At: s.clock().UTC()})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return editorialreview.Record{}, editorialreview.ErrConflict
		}
		if errors.Is(err, ErrNotFound) {
			return editorialreview.Record{}, editorialreview.ErrNotFound
		}
		return editorialreview.Record{}, editorialreview.ErrUnavailable
	}
	return s.Read(ctx, c.Query)
}

// Events возвращает журнал editorial-decision-recorded кампании.
func (s *Service) Events(ctx context.Context, campaignID string) ([]DecisionEvent, error) {
	if s == nil || s.store == nil {
		return nil, editorialreview.ErrUnavailable
	}
	return s.store.Events(ctx, campaignID)
}

var _ editorialreview.Port = (*Service)(nil)
