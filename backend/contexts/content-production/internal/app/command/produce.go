package command

import (
	"context"
	"errors"
	"strings"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// ProduceContent — команда производства контента по контракту content-request:
// обеспечить пакет и версию (создать при отсутствии, опубликовать новую при
// новом тексте) и поставить задание рендеринга. Идемпотентна по RequestID: он же
// служит идентификатором задания рендеринга, поэтому повтор команды не создаёт
// второе задание.
type ProduceContent struct {
	// RequestID — идентификатор запроса и ключ идемпотентности.
	RequestID  string
	AssetID    domain.ContentAssetID
	TenantID   domain.TenantID
	Language   string
	Region     string
	Format     domain.RenderFormat
	TemplateID domain.TemplateID
	// Body — текст контента. Для нового пакета обязателен; для существующего
	// непустой и отличный текст публикует новую версию.
	Body  string
	Actor domain.ActorID

	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// ProduceContentResult — результат производства контента.
type ProduceContentResult struct {
	AssetID     domain.ContentAssetID
	VersionID   domain.ContentVersionID
	RenderJobID domain.RenderJobID
	State       domain.RenderState
	Events      []domain.DomainEvent
}

// ProduceContentHandler — use case производства контента.
type ProduceContentHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewProduceContentHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewProduceContentHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *ProduceContentHandler {
	return &ProduceContentHandler{uow: uow, clock: clock, ids: ids}
}

// Handle обеспечивает пакет и версию контента и ставит задание рендеринга в одной
// транзакции. Повтор с тем же RequestID возвращает уже созданное задание.
func (h *ProduceContentHandler) Handle(ctx context.Context, cmd ProduceContent) (ProduceContentResult, error) {
	jobID := domain.RenderJobID(strings.TrimSpace(cmd.RequestID))
	at := resolveAt(time.Time{}, h.clock)
	correlation := correlationOrDefault(cmd.CorrelationID, cmd.RequestID)

	var result ProduceContentResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		existing, err := tx.RenderJobs().FindByID(ctx, jobID)
		if err == nil {
			if existing.TenantID() != cmd.TenantID {
				return ErrTenantMismatch
			}
			pkg, err := tx.Packages().FindByID(ctx, existing.AssetID())
			if err != nil {
				return err
			}
			latest, _ := pkg.LatestVersion()
			result = ProduceContentResult{
				AssetID:     pkg.ID(),
				VersionID:   latest.ID(),
				RenderJobID: existing.ID(),
				State:       existing.State(),
			}
			return nil
		}
		if !errors.Is(err, ports.ErrRenderJobNotFound) {
			return err
		}

		var events []domain.DomainEvent
		pkg, err := tx.Packages().FindByID(ctx, cmd.AssetID)
		switch {
		case errors.Is(err, ports.ErrContentPackageNotFound):
			locale, localeErr := domain.NewContentLocale(cmd.Language, cmd.Region)
			if localeErr != nil {
				return localeErr
			}
			if strings.TrimSpace(cmd.Body) == "" {
				return ErrPackageRequired
			}
			pkg, err = domain.NewContentPackage(domain.NewContentPackageParams{
				ID:            cmd.AssetID,
				TenantID:      cmd.TenantID,
				Locale:        locale,
				VersionID:     domain.ContentVersionID(cmd.RequestID),
				Body:          cmd.Body,
				Actor:         cmd.Actor,
				At:            at,
				EventID:       h.ids.NewEventID(),
				CorrelationID: correlation,
				CausationID:   cmd.CausationID,
			})
			if err != nil {
				return err
			}
			if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
				return err
			}
			events = append(events, pkg.DomainEvents()...)
			pkg.PullDomainEvents()
		case err != nil:
			return err
		default:
			if pkg.TenantID() != cmd.TenantID {
				return ErrTenantMismatch
			}
			latest, _ := pkg.LatestVersion()
			if body := strings.TrimSpace(cmd.Body); body != "" && body != latest.Body() {
				loadedVersion := pkg.Version()
				_, publishErr := pkg.PublishVersion(domain.PublishVersionInput{
					VersionID:     domain.ContentVersionID(cmd.RequestID),
					Body:          body,
					Actor:         cmd.Actor,
					At:            at,
					EventID:       h.ids.NewEventID(),
					CorrelationID: correlation,
					CausationID:   cmd.CausationID,
				})
				if publishErr != nil {
					return publishErr
				}
				if err := tx.Packages().Save(ctx, pkg, loadedVersion); err != nil {
					return err
				}
				events = append(events, pkg.DomainEvents()...)
				pkg.PullDomainEvents()
			}
		}

		latest, ok := pkg.LatestVersion()
		if !ok {
			return ErrPackageRequired
		}
		job, err := domain.NewRenderJob(domain.NewRenderJobParams{
			ID:         jobID,
			TenantID:   cmd.TenantID,
			AssetID:    pkg.ID(),
			VersionID:  latest.ID(),
			TemplateID: cmd.TemplateID,
			Format:     cmd.Format,
		})
		if err != nil {
			return err
		}
		if err := tx.RenderJobs().Save(ctx, job, 0); err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		result = ProduceContentResult{
			AssetID:     pkg.ID(),
			VersionID:   latest.ID(),
			RenderJobID: job.ID(),
			State:       job.State(),
			Events:      events,
		}
		return nil
	})
	if err != nil {
		return ProduceContentResult{}, err
	}
	return result, nil
}
