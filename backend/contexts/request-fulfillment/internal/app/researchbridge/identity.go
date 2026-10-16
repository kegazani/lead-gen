package researchbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"lead-gen/contracts/lead-management/researchmemory"
)

// IdentityReview содержит текущие основания и версии всей затрагиваемой группы.
// Решение принимает человек; модель не получает этот командный порт.
type IdentityRevokeOption struct {
	Decision          researchmemory.IdentityDecision
	ExpectedRevisions map[string]int64
}

type IdentityReview struct {
	RevokeOptions     []IdentityRevokeOption
	View              View
	Left              researchmemory.Dossier
	Right             *researchmemory.Dossier
	Choices           []researchmemory.Entity
	Decisions         []researchmemory.IdentityDecision
	ExpectedRevisions map[string]int64
}
type IdentityInput struct {
	Input
	EntityID             string
	RightEntityID        string
	DecisionID           string
	Action               string
	Reason               string
	EvidenceAssertionIDs []string
	ExpectedRevisions    map[string]int64
	RevokeDecisionID     string
}

func (s *Service) IdentityAvailable() bool { return s.cfg.Identity != nil }

func (s *Service) ReviewIdentity(ctx context.Context, in Input, entityID, rightID, search string) (IdentityReview, error) {
	if s.cfg.Identity == nil {
		return IdentityReview{}, ErrUnavailable
	}
	view, left, err := s.identityDossier(ctx, in, entityID)
	if err != nil {
		return IdentityReview{}, err
	}
	out := IdentityReview{View: view, Left: left, ExpectedRevisions: map[string]int64{}}
	candidates, err := s.cfg.Memory.List(ctx, researchmemory.ListRequest{Scope: memoryScope(view.Request), Query: search, Limit: 50})
	if err != nil {
		return IdentityReview{}, err
	}
	out.Choices = candidates.Entities
	out.Decisions, err = s.cfg.Identity.ListIdentityDecisions(ctx, researchmemory.IdentityHistoryRequest{Scope: memoryScope(view.Request), EntityID: entityID, Limit: 100})
	if err != nil {
		return IdentityReview{}, err
	}
	ids := []string{entityID}
	if rightID != "" {
		ids = append(ids, rightID)
		right, getErr := s.cfg.Memory.Get(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: rightID})
		if errors.Is(getErr, researchmemory.ErrRevoked) {
			right = researchmemory.Dossier{Entity: researchmemory.Entity{ID: rightID, Name: "Основания недоступны"}}
		} else if getErr != nil {
			return IdentityReview{}, getErr
		}
		out.Right = &right
	}
	for _, id := range ids {
		group, e := s.cfg.Identity.ResolveIdentity(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: id})
		if e != nil {
			return IdentityReview{}, e
		}
		for memberID, revision := range group.MemberRevisions {
			out.ExpectedRevisions[memberID] = revision
		}
	}
	for _, decision := range out.Decisions {
		if decision.State == "revoked" || (decision.Action != "link" && decision.Action != "reject") {
			continue
		}
		option := IdentityRevokeOption{Decision: decision, ExpectedRevisions: map[string]int64{}}
		for _, id := range []string{decision.LeftEntityID, decision.RightEntityID} {
			group, e := s.cfg.Identity.ResolveIdentity(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: id})
			if e != nil {
				return IdentityReview{}, e
			}
			for memberID, revision := range group.MemberRevisions {
				option.ExpectedRevisions[memberID] = revision
			}
		}
		out.RevokeOptions = append(out.RevokeOptions, option)
	}
	return out, nil
}
func (s *Service) DecideIdentity(ctx context.Context, in IdentityInput) (researchmemory.IdentityDecisionResult, error) {
	if s.cfg.Identity == nil {
		return researchmemory.IdentityDecisionResult{}, ErrUnavailable
	}
	if in.Action != "link" && in.Action != "reject" && in.Action != "revoke" {
		return researchmemory.IdentityDecisionResult{}, ErrInvalid
	}
	if strings.TrimSpace(in.Reason) == "" || len(in.Reason) > 4000 || strings.TrimSpace(in.DecisionID) == "" {
		return researchmemory.IdentityDecisionResult{}, fmt.Errorf("%w: необходимы причина и идентификатор решения", ErrInvalid)
	}
	view, left, err := s.identityDossier(ctx, in.Input, in.EntityID)
	if err != nil {
		return researchmemory.IdentityDecisionResult{}, err
	}
	rightID := in.RightEntityID
	if in.Action == "revoke" {
		decisions, e := s.cfg.Identity.ListIdentityDecisions(ctx, researchmemory.IdentityHistoryRequest{Scope: memoryScope(view.Request), EntityID: in.EntityID, Limit: 100})
		if e != nil {
			return researchmemory.IdentityDecisionResult{}, e
		}
		var selected *researchmemory.IdentityDecision
		for i := range decisions {
			if decisions[i].ID == in.RevokeDecisionID {
				selected = &decisions[i]
				break
			}
		}
		if selected == nil {
			return researchmemory.IdentityDecisionResult{}, researchmemory.ErrNotFound
		}
		// Отмена адресована конкретной проверке, не произвольной паре из формы.
		in.EntityID, rightID = selected.LeftEntityID, selected.RightEntityID
	} else {
		if rightID == "" || rightID == left.Entity.ID {
			return researchmemory.IdentityDecisionResult{}, ErrInvalid
		}
		if _, e := s.cfg.Memory.Get(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: rightID}); e != nil {
			return researchmemory.IdentityDecisionResult{}, e
		}
	}
	// Полномочия взяты из запроса владельца; scope/actor формы не принимаются.
	return s.cfg.Identity.DecideIdentity(ctx, researchmemory.IdentityDecisionRequest{Scope: memoryScope(view.Request), DecisionID: in.DecisionID, Action: in.Action, LeftEntityID: in.EntityID, RightEntityID: rightID, ExpectedRevisions: in.ExpectedRevisions, Actor: in.Actor, Reason: in.Reason, EvidenceAssertionIDs: in.EvidenceAssertionIDs, RevokeDecisionID: in.RevokeDecisionID})
}

// Отозванные payload не читаются даже для ручной отмены связи. Идентичность
// строки и CAS доступны отдельным scoped metadata-контрактом владельца.
func (s *Service) identityDossier(ctx context.Context, in Input, entityID string) (View, researchmemory.Dossier, error) {
	view, err := s.Load(ctx, in, "all")
	if err != nil {
		return View{}, researchmemory.Dossier{}, err
	}
	found := false
	if view.Snapshot != nil {
		for _, member := range view.Snapshot.Members {
			if member.EntityID == entityID {
				found = true
				break
			}
		}
	}
	if !found {
		return View{}, researchmemory.Dossier{}, researchmemory.ErrNotFound
	}
	dossier, err := s.cfg.Memory.Get(ctx, researchmemory.GetRequest{Scope: memoryScope(view.Request), EntityID: entityID})
	if errors.Is(err, researchmemory.ErrRevoked) {
		return view, researchmemory.Dossier{Entity: researchmemory.Entity{ID: entityID, Name: "Основания недоступны"}}, nil
	}
	return view, dossier, err
}
