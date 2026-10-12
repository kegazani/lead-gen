package inmemory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"sync"

	appeditorial "lead-gen/backend/contexts/outreach-orchestration/internal/app/editorialreview"
	contract "lead-gen/contracts/outreach-orchestration/editorialreview"
)

type editorialState struct {
	campaigns    map[string]appeditorial.Campaign
	fingerprints map[string]string
	events       map[string][]appeditorial.DecisionEvent
}

// EditorialStore хранит редакторские кампании отдельно от обычной очереди
// черновиков и delivery outbox.
type EditorialStore struct {
	mu    sync.RWMutex
	state *editorialState
}

func NewEditorialStore() *EditorialStore {
	return &EditorialStore{state: &editorialState{campaigns: map[string]appeditorial.Campaign{}, fingerprints: map[string]string{}, events: map[string][]appeditorial.DecisionEvent{}}}
}

func (s *EditorialStore) Submit(_ context.Context, h appeditorial.Handoff) (appeditorial.Campaign, bool, error) {
	if strings.TrimSpace(h.TenantID) == "" || strings.TrimSpace(h.RequestID) == "" || len(h.Items) == 0 {
		return appeditorial.Campaign{}, false, contract.ErrRejected
	}
	items := append([]appeditorial.Item(nil), h.Items...)
	sort.Slice(items, func(i, j int) bool { return items[i].LeadID < items[j].LeadID })
	for i := range items {
		if strings.TrimSpace(items[i].LeadID) == "" || strings.TrimSpace(items[i].AssetID) == "" || strings.TrimSpace(items[i].VersionID) == "" || strings.TrimSpace(items[i].MaterialRef) == "" {
			return appeditorial.Campaign{}, false, contract.ErrRejected
		}
		if i > 0 && items[i-1].LeadID == items[i].LeadID {
			return appeditorial.Campaign{}, false, contract.ErrRejected
		}
	}
	raw, _ := json.Marshal(items)
	digest := sha256.Sum256(raw)
	fingerprint := hex.EncodeToString(digest[:])
	id := "handoff-" + h.RequestID
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.state.campaigns[id]; ok {
		if existing.TenantID != h.TenantID || s.state.fingerprints[id] != fingerprint {
			return appeditorial.Campaign{}, false, contract.ErrConflict
		}
		return cloneEditorial(existing), true, nil
	}
	c := appeditorial.Campaign{CampaignID: id, TenantID: h.TenantID, RequestID: h.RequestID, Purpose: "editorial"}
	for _, item := range items {
		c.Items = append(c.Items, appeditorial.ItemRecord{Item: item})
	}
	s.state.campaigns[id] = c
	s.state.fingerprints[id] = fingerprint
	return cloneEditorial(c), false, nil
}

func (s *EditorialStore) Get(_ context.Context, q contract.Query) (appeditorial.ItemRecord, string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.state.campaigns[q.CampaignID]
	if !ok || c.TenantID != q.TenantID {
		return appeditorial.ItemRecord{}, "", contract.ErrNotFound
	}
	for _, item := range c.Items {
		if item.LeadID == q.LeadID && item.MaterialRef == q.MaterialRef && item.VersionID == q.VersionID {
			return item, c.CampaignID, nil
		}
	}
	return appeditorial.ItemRecord{}, "", contract.ErrNotFound
}

func (s *EditorialStore) Decide(_ context.Context, d appeditorial.Decide) (appeditorial.ItemRecord, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.state.campaigns[d.CampaignID]
	if !ok || c.TenantID != d.TenantID {
		return appeditorial.ItemRecord{}, "", contract.ErrNotFound
	}
	for i, item := range c.Items {
		if item.LeadID != d.LeadID || item.MaterialRef != d.MaterialRef {
			continue
		}
		if item.VersionID != d.ExpectedVersion {
			return appeditorial.ItemRecord{}, "", appeditorial.ErrConflict
		}
		if item.Decision != nil {
			old := item.Decision
			if old.Outcome == d.Outcome && old.Reason == d.Reason && old.Actor == d.Actor && old.VersionID == d.ExpectedVersion {
				return item, c.CampaignID, nil
			}
			return appeditorial.ItemRecord{}, "", appeditorial.ErrConflict
		}
		item.Decision = &appeditorial.Decision{Outcome: d.Outcome, Reason: d.Reason, Actor: d.Actor, At: d.At, VersionID: d.ExpectedVersion}
		c.Items[i] = item
		s.state.campaigns[d.CampaignID] = c
		s.state.events[d.CampaignID] = append(s.state.events[d.CampaignID], appeditorial.DecisionEvent{EventType: appeditorial.EventTypeDecisionRecorded, CampaignID: d.CampaignID, TenantID: d.TenantID, LeadID: d.LeadID, MaterialRef: d.MaterialRef, VersionID: d.ExpectedVersion, Outcome: d.Outcome, Reason: d.Reason, Actor: d.Actor, At: d.At})
		return item, c.CampaignID, nil
	}
	return appeditorial.ItemRecord{}, "", contract.ErrNotFound
}

func (s *EditorialStore) Events(_ context.Context, campaignID string) ([]appeditorial.DecisionEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.state.campaigns[campaignID]; !ok {
		return nil, contract.ErrNotFound
	}
	return append([]appeditorial.DecisionEvent(nil), s.state.events[campaignID]...), nil
}

func cloneEditorial(c appeditorial.Campaign) appeditorial.Campaign {
	out := c
	out.Items = append([]appeditorial.ItemRecord(nil), c.Items...)
	return out
}

var _ appeditorial.Store = (*EditorialStore)(nil)
