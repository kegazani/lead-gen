// Package researchmemory применяет предметные правила до записи наблюдения.
package researchmemory

import (
	"context"
	"fmt"
	domain "lead-gen/backend/contexts/lead-management/internal/domain/researchmemory"
	ports "lead-gen/backend/contexts/lead-management/internal/ports/researchmemory"
	contract "lead-gen/contracts/lead-management/researchmemory"
	"strings"
	"time"
)

type Service struct{ repo ports.Repository }

func New(repo ports.Repository) *Service {
	if repo == nil {
		panic("researchmemory: nil repository")
	}
	return &Service{repo: repo}
}
func (s *Service) SaveObservation(ctx context.Context, r contract.SaveObservationRequest) (contract.SaveObservationResult, error) {
	n, e := domain.NormalizeObservation(r)
	if e != nil {
		return contract.SaveObservationResult{}, e
	}
	return s.repo.SaveObservation(ctx, n)
}
func (s *Service) Get(ctx context.Context, r contract.GetRequest) (contract.Dossier, error) {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return contract.Dossier{}, e
	}
	if r.EntityID == "" {
		return contract.Dossier{}, contract.ErrInvalid
	}
	return s.repo.Get(ctx, r)
}
func (s *Service) List(ctx context.Context, r contract.ListRequest) (contract.ListResult, error) {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return contract.ListResult{}, e
	}
	if r.Limit == 0 {
		r.Limit = 50
	}
	if r.Limit < 1 || r.Limit > 200 || r.Offset < 0 || len(r.Query) > 1000 {
		return contract.ListResult{}, contract.ErrInvalid
	}
	return s.repo.List(ctx, r)
}
func (s *Service) SaveAttempt(ctx context.Context, r contract.Attempt) error {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return e
	}
	if r.ID == "" || r.MissionID == "" || r.ActionID == "" || strings.TrimSpace(r.Question) == "" || r.Tool == "" || r.ObservedAt.IsZero() || !r.ExpiresAt.After(r.ObservedAt) || r.ExpiresAt.Sub(r.ObservedAt) > 365*24*time.Hour {
		return contract.ErrInvalid
	}
	switch r.Outcome {
	case "new-fact", "no-match-on-version", "ambiguous", "unavailable", "error":
	default:
		return fmt.Errorf("%w: исход попытки", contract.ErrInvalid)
	}
	if r.Outcome == "no-match-on-version" && (r.SourceURL == "" || r.SourceRevision == "") {
		return fmt.Errorf("%w: отрицание требует версии источника", contract.ErrInvalid)
	}
	r.Question = strings.Join(strings.Fields(strings.ToLower(r.Question)), " ")
	return s.repo.SaveAttempt(ctx, r)
}
func (s *Service) RecallAttempts(ctx context.Context, r contract.RecallAttemptsRequest) ([]contract.Attempt, error) {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return nil, e
	}
	if r.Question == "" {
		return nil, contract.ErrInvalid
	}
	r.Question = strings.Join(strings.Fields(strings.ToLower(r.Question)), " ")
	if r.Limit == 0 {
		r.Limit = 20
	}
	if r.Limit < 1 || r.Limit > 100 {
		return nil, contract.ErrInvalid
	}
	return s.repo.RecallAttempts(ctx, r)
}
func (s *Service) SetNeedExecution(ctx context.Context, r contract.NeedExecutionRequest) error {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return e
	}
	if r.EntityID == "" || r.MissionID == "" {
		return contract.ErrInvalid
	}
	return s.repo.SetNeedExecution(ctx, r)
}
func (s *Service) RevokeSource(ctx context.Context, r contract.RevokeSourceRequest) error {
	if e := domain.ValidateScope(r.Scope); e != nil {
		return e
	}
	if r.SourceID == "" || strings.TrimSpace(r.Reason) == "" {
		return contract.ErrInvalid
	}
	return s.repo.RevokeSource(ctx, r)
}
