package command

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// RestoreContentVersion — команда возврата к предыдущей версии наполнения.
//
// Возврат не правит прежние версии (политика immutable-content-version): текст
// выбранной версии публикуется как новая неизменяемая версия. Так история
// сохраняется целиком, а «текущей» снова становится восстановленное наполнение.
type RestoreContentVersion struct {
	AssetID  domain.ContentAssetID
	TenantID domain.TenantID

	// SourceVersionID — версия, к которой возвращаются.
	SourceVersionID domain.ContentVersionID
	// NewVersionID — идентификатор новой версии, фиксирующей возврат.
	NewVersionID domain.ContentVersionID
	Actor        domain.ActorID
	At           time.Time

	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// RestoreContentVersionResult — результат возврата к версии.
type RestoreContentVersionResult struct {
	AssetID      domain.ContentAssetID
	VersionID    domain.ContentVersionID
	RestoredFrom domain.ContentVersionID
	Revision     int
	Version      int
	Events       []domain.DomainEvent
}

// RestoreContentVersionHandler — use case возврата к версии.
type RestoreContentVersionHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewRestoreContentVersionHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewRestoreContentVersionHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *RestoreContentVersionHandler {
	return &RestoreContentVersionHandler{uow: uow, clock: clock, ids: ids}
}

// Handle восстанавливает выбранную версию как новую. Возврат к текущей версии —
// no-op, отвергается доменом; отсутствующая версия — ошибка «не найдено».
func (h *RestoreContentVersionHandler) Handle(ctx context.Context, cmd RestoreContentVersion) (RestoreContentVersionResult, error) {
	at := resolveAt(cmd.At, h.clock)

	var result RestoreContentVersionResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		pkg, err := tx.Packages().FindByID(ctx, cmd.AssetID)
		if err != nil {
			return err
		}
		if pkg.TenantID() != cmd.TenantID {
			return fmt.Errorf("%w: %s", ErrTenantMismatch, cmd.AssetID)
		}
		source, ok := pkg.FindVersion(cmd.SourceVersionID)
		if !ok {
			return fmt.Errorf("%w: %s", domain.ErrVersionNotFound, cmd.SourceVersionID)
		}
		if latest, ok := pkg.LatestVersion(); ok && latest.ID() == cmd.SourceVersionID {
			return fmt.Errorf("%w: %s", domain.ErrRestoreSelf, cmd.SourceVersionID)
		}

		loadedVersion := pkg.Version()
		version, err := pkg.PublishVersion(domain.PublishVersionInput{
			VersionID:     cmd.NewVersionID,
			Body:          source.Body(),
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
		result = RestoreContentVersionResult{
			AssetID:      pkg.ID(),
			VersionID:    version.ID(),
			RestoredFrom: cmd.SourceVersionID,
			Revision:     version.Revision(),
			Version:      pkg.Version(),
			Events:       events,
		}
		return nil
	})
	if err != nil {
		return RestoreContentVersionResult{}, err
	}
	return result, nil
}
