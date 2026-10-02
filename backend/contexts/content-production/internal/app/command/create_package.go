package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/content-production/internal/domain"
	"lead-gen/backend/contexts/content-production/internal/ports"
)

// CreateContentPackage — команда создания контент-пакета с первой версией.
type CreateContentPackage struct {
	AssetID   domain.ContentAssetID
	TenantID  domain.TenantID
	VersionID domain.ContentVersionID
	Language  string
	Region    string
	Body      string
	Actor     domain.ActorID
	At        time.Time

	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// CreateContentPackageResult — результат создания пакета.
type CreateContentPackageResult struct {
	AssetID   domain.ContentAssetID
	VersionID domain.ContentVersionID
	Revision  int
	Version   int
	Events    []domain.DomainEvent
}

// CreateContentPackageHandler — use case создания контент-пакета.
type CreateContentPackageHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewCreateContentPackageHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов событий.
func NewCreateContentPackageHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *CreateContentPackageHandler {
	return &CreateContentPackageHandler{uow: uow, clock: clock, ids: ids}
}

// Handle создаёт пакет с первой версией и сохраняет его вместе с событием
// content-version-created в одной транзакции. Пустой текст отвергается доменом.
func (h *CreateContentPackageHandler) Handle(ctx context.Context, cmd CreateContentPackage) (CreateContentPackageResult, error) {
	locale, err := domain.NewContentLocale(cmd.Language, cmd.Region)
	if err != nil {
		return CreateContentPackageResult{}, err
	}
	at := resolveAt(cmd.At, h.clock)

	var result CreateContentPackageResult
	err = h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		pkg, err := domain.NewContentPackage(domain.NewContentPackageParams{
			ID:            cmd.AssetID,
			TenantID:      cmd.TenantID,
			Locale:        locale,
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
		if err := tx.Packages().Save(ctx, pkg, 0); err != nil {
			return err
		}
		events := pkg.DomainEvents()
		if err := tx.Outbox().Append(ctx, events...); err != nil {
			return err
		}
		pkg.PullDomainEvents()
		latest, _ := pkg.LatestVersion()
		result = CreateContentPackageResult{
			AssetID:   pkg.ID(),
			VersionID: latest.ID(),
			Revision:  latest.Revision(),
			Version:   pkg.Version(),
			Events:    events,
		}
		return nil
	})
	if err != nil {
		return CreateContentPackageResult{}, err
	}
	return result, nil
}

// resolveAt возвращает время команды, а при нулевом — время часов.
func resolveAt(at time.Time, clock ports.Clock) time.Time {
	if !at.IsZero() {
		return at
	}
	if clock != nil {
		return clock.Now()
	}
	return time.Now().UTC()
}

// correlationOrDefault берёт сквозной идентификатор команды, а при пустом —
// запасное значение (идентификатор создаваемого агрегата).
func correlationOrDefault(correlation domain.CorrelationID, fallback string) domain.CorrelationID {
	if correlation != "" {
		return correlation
	}
	return domain.CorrelationID(fallback)
}
