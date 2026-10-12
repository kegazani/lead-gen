package outreachorchestration

import (
	"context"

	contract "lead-gen/contracts/outreach-orchestration/editorialreview"
)

// EditorialReviewPort публикует контракт чтения/решения для co-located
// потребителей; адаптер не соединяет его с approved-action или доставкой.
func (s *Service) EditorialReviewPort() contract.Port {
	if s == nil || s.slice == nil || s.slice.EditorialReview == nil {
		return contract.InProc(contract.InProcHandler{})
	}
	review := s.slice.EditorialReview
	return contract.InProc(contract.InProcHandler{
		ReadFunc:   func(ctx context.Context, q contract.Query) (contract.Record, error) { return review.Read(ctx, q) },
		DecideFunc: func(ctx context.Context, c contract.Command) (contract.Record, error) { return review.Decide(ctx, c) },
	})
}
