package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// PublishContentVersion — команда публикации новой неизменяемой версии контента.
type PublishContentVersion struct {
	AssetID   domain.ContentAssetID
	TenantID  domain.TenantID
	VersionID domain.ContentVersionID
	Body      string
	Actor     domain.ActorID
	At        time.Time

	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// PublishContentVersionResult — результат публикации версии.
type PublishContentVersionResult struct {
	AssetID   domain.ContentAssetID
	VersionID domain.ContentVersionID
	Revision  int
	Version   int
	Events    []domain.DomainEvent
}

// PublishContentVersionHandler — use case публикации версии.
type PublishContentVersionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewPublishContentVersionHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewPublishContentVersionHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *PublishContentVersionHandler {
	return &PublishContentVersionHandler{uow: uow, clock: clock, ids: ids}
}

// Handle публикует новый текст как новую версию загруженного пакета, сохраняет
// его с оптимистичной блокировкой и пишет событие content-version-created.
func (h *PublishContentVersionHandler) Handle(ctx context.Context, cmd PublishContentVersion) (PublishContentVersionResult, error) {
	at := resolveAt(cmd.At, h.clock)

	var result PublishContentVersionResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		pkg, err := tx.Packages().FindByID(ctx, cmd.AssetID)
		if err != nil {
			return err
		}
		if pkg.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: %s", ErrTenantMismatch, cmd.AssetID)
		}
		loadedVersion := pkg.Version()
		version, err := pkg.PublishVersion(domain.PublishVersionInput{
			VersionID:     cmd.VersionID,
			Body:          cmd.Body,
			Actor:         cmd.Actor,
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: correlationOrDefault(cmd.CorrelationID, string(cmd.AssetID)),
			CausationID:   cmd.CausationID,
		})
		if err != nil {
			return err
		}
		if err := tx.Packages().Save(ctx, pkg, loadedVersion); err != nil {
			return err
		}
		events := pkg.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		pkg.PullDomainEvents()
		result = PublishContentVersionResult{
			AssetID:   pkg.ID(),
			VersionID: version.ID(),
			Revision:  version.Revision(),
			Version:   pkg.Version(),
			Events:    events,
		}
		return nil
	})
	if err != nil {
		return PublishContentVersionResult{}, err
	}
	return result, nil
}
