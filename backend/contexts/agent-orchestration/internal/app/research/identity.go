package research

import (
	"context"
	"reflect"
	"sort"

	"lead-gen/backend/contexts/agent-orchestration/internal/domain"
	rm "lead-gen/contracts/agent-orchestration/researchmission"
	memory "lead-gen/contracts/lead-management/researchmemory"
)

type identityReader interface {
	ResolveIdentityBatch(context.Context, memory.IdentityBatchRequest) ([]memory.IdentityEntityState, error)
}

// Перечитывание текущей owner-проекции происходит до claim/завершения, вне
// транзакции mission. CAS не позволяет применить её к уже изменённому составу.
func (s *Service) refreshIdentity(ctx context.Context, scope rm.Scope, id string) error {
	reader, hasIdentity := s.config.Memory.(identityReader)
	mission, err := s.config.Repository.Get(ctx, scope, id)
	if err != nil {
		return err
	}
	if mission.Terminal() || len(mission.Members) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mission.Members))
	expected := map[string]bool{}
	for _, member := range mission.Members {
		ids = append(ids, member.EntityID)
		expected[member.EntityID] = true
	}
	var states []memory.IdentityEntityState
	if hasIdentity {
		states, err = reader.ResolveIdentityBatch(ctx, memory.IdentityBatchRequest{Scope: memory.Scope{TenantID: scope.TenantID, Purpose: scope.PurposeID}, EntityIDs: ids})
		if err != nil {
			return err
		}
	} else {
		for _, member := range mission.Members {
			canonical := member.CanonicalEntityID
			if canonical == "" {
				canonical = member.EntityID
			}
			states = append(states, memory.IdentityEntityState{EntityID: member.EntityID, CanonicalEntityID: canonical, EntityRevision: member.CurrentEntityRevision, IdentityRevision: member.IdentityRevision, Name: member.Name, HasContact: member.HasContact, HasEmail: member.HasEmail, Available: !member.SourceUnavailable, DistinctCanonicalEntityIDs: member.DistinctCanonicalEntityIDs})
		}
	}
	supports := map[string]CriteriaSupport{}
	for _, member := range mission.Members {
		support, err := resolveCriteriaSupport(ctx, s.config.Memory, scope, member.EntityID, mission.Request.CriteriaVersion, memberAssessments(member))
		if err != nil {
			return err
		}
		supports[member.EntityID] = support
	}
	byID := map[string]memory.IdentityEntityState{}
	for _, state := range states {
		if !expected[state.EntityID] || state.EntityID == "" || state.CanonicalEntityID == "" {
			return rm.ErrConflict
		}
		if _, duplicate := byID[state.EntityID]; duplicate {
			return rm.ErrConflict
		}
		byID[state.EntityID] = state
	}
	if len(byID) != len(expected) {
		return rm.ErrConflict
	}
	// Различение опирается на действующие решения владельца или независимые
	// регистрационные значения в одном namespace, не на два разных домена.
	for id, state := range byID {
		known := map[string]bool{}
		for _, otherID := range state.DistinctCanonicalEntityIDs {
			if otherID != "" && otherID != state.CanonicalEntityID {
				known[otherID] = true
			}
		}
		for _, other := range states {
			if state.CanonicalEntityID != other.CanonicalEntityID && groundedDistinct(state.GroundedIdentifiers, other.GroundedIdentifiers) {
				known[other.CanonicalEntityID] = true
			}
		}
		state.DistinctCanonicalEntityIDs = nil
		for key := range known {
			state.DistinctCanonicalEntityIDs = append(state.DistinctCanonicalEntityIDs, key)
		}
		sort.Strings(state.DistinctCanonicalEntityIDs)
		byID[id] = state
	}
	_, err = s.config.Repository.Update(ctx, scope, id, func(current *domain.ResearchMission) error {
		if current.Revision != mission.Revision {
			return rm.ErrConflict
		}
		if current.Terminal() {
			return nil
		}
		before := append([]rm.Member(nil), current.Members...)
		for i := range current.Members {
			member := &current.Members[i]
			state := byID[member.EntityID]
			support := supports[member.EntityID]
			member.CriteriaAccepted = support.Accepted
			member.EvidenceRefs = append([]string(nil), support.EvidenceRefs...)
			member.Reason = support.Reason
			if state.IdentityRevision < member.IdentityRevision {
				continue
			}
			if member.CurrentEntityRevision != state.EntityRevision {
				member.EmailReady = false
			}
			if state.Name != "" {
				member.Name = state.Name
			}
			member.DistinctCanonicalEntityIDs = append([]string(nil), state.DistinctCanonicalEntityIDs...)
			member.CanonicalEntityID = state.CanonicalEntityID
			member.IdentityRevision = state.IdentityRevision
			member.CurrentEntityRevision = state.EntityRevision
			member.SourceUnavailable = !state.Available
			member.HasContact = state.HasContact && state.Available
			member.HasEmail = state.HasEmail && state.Available
			member.EmailReady = member.EmailReady && member.HasEmail
			if current.Request.Plan.Brief.Goal.Unit == "email-ready" && member.HasEmail && !member.EmailReady {
				member.Reason = "email-policy-readiness-not-confirmed"
			}
		}
		current.Recredit()
		if !reflect.DeepEqual(before, current.Members) {
			current.LastProgressAt = s.now()
			current.Touch(s.now())
		}
		return nil
	})
	return err
}

func groundedDistinct(left, right []memory.IdentityGround) bool {
	different := false
	for _, a := range left {
		if a.Kind == "" || a.Authority == "" || a.Jurisdiction == "" || a.Value == "" {
			continue
		}
		for _, b := range right {
			if a.Kind != b.Kind || a.Authority != b.Authority || a.Jurisdiction != b.Jurisdiction || b.Value == "" {
				continue
			}
			if a.Value == b.Value {
				return false
			}
			different = true
		}
	}
	return different
}
