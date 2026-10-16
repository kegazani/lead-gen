package researchmemory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	domain "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
)

func (s *Service) DecideIdentity(ctx context.Context, q contract.IdentityDecisionRequest) (contract.IdentityDecisionResult, error) {
	fail := func(reason string) (contract.IdentityDecisionResult, error) {
		return contract.IdentityDecisionResult{}, fmt.Errorf("%w: %s", contract.ErrInvalid, reason)
	}
	if e := domain.ValidateScope(q.Scope); e != nil {
		return contract.IdentityDecisionResult{}, e
	}
	q.Actor = strings.TrimSpace(q.Actor)
	q.Reason = strings.TrimSpace(q.Reason)
	if q.DecisionID == "" || len(q.DecisionID) > 200 || q.Actor == "" || len(q.Actor) > 200 || q.Reason == "" || len(q.Reason) > 4000 {
		return fail("идентификатор, автор и причина решения обязательны")
	}
	switch q.Action {
	case "link", "reject":
		if q.LeftEntityID == "" || q.RightEntityID == "" || q.LeftEntityID == q.RightEntityID || q.RevokeDecisionID != "" {
			return fail("два разных исходных субъекта обязательны")
		}
	case "revoke":
		if q.RevokeDecisionID == "" || len(q.EvidenceAssertionIDs) > 0 {
			return fail("ссылка отменяемого решения обязательна")
		}
	default:
		return fail("тип решения")
	}
	if len(q.ExpectedRevisions) < 2 || len(q.ExpectedRevisions) > 200 {
		return fail("версии всех участников обязательны")
	}
	for id, revision := range q.ExpectedRevisions {
		if id == "" || revision < 1 {
			return fail("версия участника")
		}
	}
	if (q.Action == "link" || q.Action == "reject") && (len(q.EvidenceAssertionIDs) < 2 || len(q.EvidenceAssertionIDs) > 100) {
		return fail("основания обоих субъектов обязательны")
	}
	if q.Action == "revoke" && len(q.EvidenceAssertionIDs) > 0 {
		return fail("основания задаются при связывании")
	}
	q.EvidenceAssertionIDs = append([]string(nil), q.EvidenceAssertionIDs...)
	sort.Strings(q.EvidenceAssertionIDs)
	return s.repo.DecideIdentity(ctx, q)
}
func (s *Service) ResolveIdentity(ctx context.Context, q contract.GetRequest) (contract.IdentityGroup, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return contract.IdentityGroup{}, e
	}
	if q.EntityID == "" {
		return contract.IdentityGroup{}, contract.ErrInvalid
	}
	return s.repo.ResolveIdentity(ctx, q)
}
func (s *Service) ListIdentityDecisions(ctx context.Context, q contract.IdentityHistoryRequest) ([]contract.IdentityDecision, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return nil, e
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > 200 {
		return nil, contract.ErrInvalid
	}
	return s.repo.ListIdentityDecisions(ctx, q)
}
func (s *Service) ResolveIdentityBatch(ctx context.Context, q contract.IdentityBatchRequest) ([]contract.IdentityEntityState, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return nil, e
	}
	if len(q.EntityIDs) > 1000 {
		return nil, contract.ErrInvalid
	}
	for _, id := range q.EntityIDs {
		if id == "" {
			return nil, contract.ErrInvalid
		}
	}
	return s.repo.ResolveIdentityBatch(ctx, q)
}
func (s *Service) ReadIdentityChanges(ctx context.Context, q contract.IdentityChangesRequest) ([]contract.IdentityChange, error) {
	if e := domain.ValidateScope(q.Scope); e != nil {
		return nil, e
	}
	if q.Limit == 0 {
		q.Limit = 100
	}
	if q.Limit < 1 || q.Limit > 500 || q.AfterRevision < 0 {
		return nil, contract.ErrInvalid
	}
	return s.repo.ReadIdentityChanges(ctx, q)
}
