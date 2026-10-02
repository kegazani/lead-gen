package publishing

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/publishing/internal/app/command"
	"lead-gen/backend/contexts/publishing/internal/app/query"
	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
	"lead-gen/contracts/publishing/publicationcommand"
	"lead-gen/contracts/publishing/publicationresult"
)

// Contracts — публикуемые контракты контекста publishing, собранные поверх среза.
// Точка сборки (cmd/lead-gen) не имеет доступа к внутренним пакетам контекста,
// поэтому прямые адаптеры контрактов объявлены здесь: при co-location вызов идёт
// в процессе, при разделении сервисов та же форма даётся сетевым клиентом.
type Contracts struct {
	Command publicationcommand.Port
	Result  publicationresult.Port
}

// NewContracts собирает оба публикуемых контракта контекста.
func NewContracts(slice *Slice) Contracts {
	return Contracts{
		Command: NewPublicationCommand(slice),
		Result:  NewPublicationResult(slice),
	}
}

// NewPublicationCommand собирает контракт publication-command поверх среза:
// приём команды на публикацию утверждённого материала.
func NewPublicationCommand(slice *Slice) publicationcommand.Port {
	if slice == nil {
		return publicationcommand.InProc(publicationcommand.InProcHandler{})
	}
	return publicationcommand.InProc(publicationcommand.InProcHandler{
		SubmitFunc: func(ctx context.Context, cmd publicationcommand.Command) (publicationcommand.Result, error) {
			if err := cmd.Validate(); err != nil {
				return publicationcommand.Result{}, fmt.Errorf("%w: %v", publicationcommand.ErrRejected, err)
			}
			targets := make([]command.PublicationRequest, 0, len(cmd.Targets))
			for _, target := range cmd.Targets {
				parsed, err := domain.NewPublicationTarget(target.Platform, target.Account)
				if err != nil {
					return publicationcommand.Result{}, fmt.Errorf("%w: %v", publicationcommand.ErrRejected, err)
				}
				targets = append(targets, command.PublicationRequest{
					PublicationID:  domain.PublicationID(target.PublicationID),
					Target:         parsed,
					Title:          target.Material.Title,
					Body:           target.Material.Body,
					IdempotencyKey: domain.IdempotencyKey(target.IdempotencyKey),
				})
			}
			result, err := slice.RequestPublication.Handle(ctx, command.RequestPublication{
				TenantID:      domain.TenantID(cmd.TenantID),
				Actor:         domain.ActorID(cmd.Actor),
				Digest:        domain.DigestRef{ID: cmd.DigestID, CommunityID: cmd.CommunityID, Period: cmd.Period},
				Targets:       targets,
				CorrelationID: domain.CorrelationID(cmd.CorrelationID),
				CausationID:   domain.CausationID(cmd.CausationID),
			})
			if err != nil {
				return publicationcommand.Result{}, publicationCommandError(err)
			}
			accepted := make([]publicationcommand.Accepted, 0, len(result.Publications))
			for _, state := range result.Publications {
				accepted = append(accepted, publicationcommand.Accepted{
					PublicationID: state.ID.String(),
					Platform:      string(state.Target.Platform()),
					Status:        string(state.Status),
					Shortened:     state.Shortened,
				})
			}
			return publicationcommand.Result{Publications: accepted}, nil
		},
	})
}

// NewPublicationResult собирает контракт publication-result поверх среза: чтение
// исходов публикаций по идентификатору или статусу.
func NewPublicationResult(slice *Slice) publicationresult.Port {
	if slice == nil {
		return publicationresult.InProc(publicationresult.InProcHandler{})
	}
	return publicationresult.InProc(publicationresult.InProcHandler{
		QueryFunc: func(ctx context.Context, q publicationresult.Query) (publicationresult.Result, error) {
			tenant := domain.TenantID(q.TenantID)
			if q.PublicationID != "" {
				view, err := slice.GetPublication.Handle(ctx, query.GetPublication{
					PublicationID: domain.PublicationID(q.PublicationID),
					TenantID:      tenant,
				})
				if err != nil {
					return publicationresult.Result{}, publicationResultError(err)
				}
				return publicationresult.Result{Records: []publicationresult.Record{recordOf(view)}}, nil
			}
			if q.Status == "" {
				return publicationresult.Result{}, fmt.Errorf("%w: не задан ни publication_id, ни status", publicationresult.ErrUnavailable)
			}
			views, err := slice.PublicationQueue.Handle(ctx, query.PublicationQueue{
				TenantID: tenant,
				Status:   domain.PublicationStatus(q.Status),
			})
			if err != nil {
				return publicationresult.Result{}, publicationResultError(err)
			}
			records := make([]publicationresult.Record, 0, len(views))
			for _, view := range views {
				records = append(records, recordOf(view))
			}
			return publicationresult.Result{Records: records}, nil
		},
	})
}

// recordOf переводит проекцию публикации в запись контракта.
func recordOf(view query.PublicationView) publicationresult.Record {
	return publicationresult.Record{
		PublicationID: view.ID.String(),
		TenantID:      view.TenantID.String(),
		Platform:      string(view.Platform),
		Account:       view.Account,
		Status:        string(view.Status),
		Address:       view.Address,
		FailureReason: view.FailureReason,
		ApprovedBy:    view.ApprovedBy.String(),
		Shortened:     view.Shortened,
		PublishedAt:   view.PublishedAt,
	}
}

// publicationCommandError переводит ошибку сценария в исход контракта
// publication-command: доменный отказ — ErrRejected, отсутствие — ErrNotFound,
// прочее — ErrUnavailable.
func publicationCommandError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, command.ErrSourceNotFound),
		errors.Is(err, command.ErrNoTargets),
		errors.Is(err, command.ErrDuplicateTarget),
		errors.Is(err, domain.ErrEmptyMaterial),
		errors.Is(err, domain.ErrUnknownPlatform),
		errors.Is(err, domain.ErrEmptyAccount),
		errors.Is(err, domain.ErrDigestRequired),
		errors.Is(err, domain.ErrEmptyID),
		errors.Is(err, domain.ErrMissingTime):
		return fmt.Errorf("%w: %v", publicationcommand.ErrRejected, err)
	case errors.Is(err, ports.ErrPublicationNotFound),
		errors.Is(err, command.ErrTenantMismatch):
		return fmt.Errorf("%w: %v", publicationcommand.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", publicationcommand.ErrUnavailable, err)
	}
}

// publicationResultError переводит ошибку чтения в исход контракта
// publication-result.
func publicationResultError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ports.ErrPublicationNotFound),
		errors.Is(err, command.ErrTenantMismatch):
		return fmt.Errorf("%w: %v", publicationresult.ErrNotFound, err)
	default:
		return fmt.Errorf("%w: %v", publicationresult.ErrUnavailable, err)
	}
}
