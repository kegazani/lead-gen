package command

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// PublicationRequest — один материал команды: площадка, материал и ключ
// идемпотентности. Материал подготовлен под формат площадки заранее; отдельный
// материал на каждую площадку выражается отдельным элементом команды.
type PublicationRequest struct {
	PublicationID  domain.PublicationID
	Target         domain.PublicationTarget
	Title          string
	Body           string
	IdempotencyKey domain.IdempotencyKey
}

// RequestPublication — команда на публикацию утверждённого материала:
// контракт publication-command. Источник — выжимка сообщества; сырых сообщений
// команда не несёт.
type RequestPublication struct {
	TenantID      domain.TenantID
	Actor         domain.ActorID
	Digest        domain.DigestRef
	Targets       []PublicationRequest
	At            time.Time
	CorrelationID domain.CorrelationID
	CausationID   domain.CausationID
}

// RequestPublicationResult — принятые публикации: по одной на материал команды.
type RequestPublicationResult struct {
	Publications []PublicationState
}

// RequestPublicationHandler — use case приёма команды на публикацию.
type RequestPublicationHandler struct {
	uow    ports.UnitOfWork
	digest ports.DigestSource
	clock  ports.Clock
}

// NewRequestPublicationHandler собирает use case из порта транзакции, источника
// выжимок и часов.
func NewRequestPublicationHandler(uow ports.UnitOfWork, digest ports.DigestSource, clock ports.Clock) *RequestPublicationHandler {
	return &RequestPublicationHandler{uow: uow, digest: digest, clock: clock}
}

// Handle принимает команду: проверяет источник, создаёт по черновику на каждый
// материал и сохраняет их в одной транзакции. Повтор элемента с уже
// существующим ключом идемпотентности не создаёт вторую публикацию — возвращается
// прежняя.
func (h *RequestPublicationHandler) Handle(ctx context.Context, cmd RequestPublication) (RequestPublicationResult, error) {
	if len(cmd.Targets) == 0 {
		return RequestPublicationResult{}, ErrNoTargets
	}
	if err := h.verifySource(ctx, cmd.Digest); err != nil {
		return RequestPublicationResult{}, err
	}
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}

	var result RequestPublicationResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		seen := make(map[domain.IdempotencyKey]struct{}, len(cmd.Targets))
		states := make([]PublicationState, 0, len(cmd.Targets))
		for _, target := range cmd.Targets {
			if _, dup := seen[target.IdempotencyKey]; dup {
				return fmt.Errorf("%w: %s", ErrDuplicateTarget, target.IdempotencyKey)
			}
			seen[target.IdempotencyKey] = struct{}{}

			existing, err := tx.Publications().FindByIdempotencyKey(ctx, target.IdempotencyKey)
			switch {
			case err == nil:
				if existing.TenantID() != cmd.TenantID {
					return ErrTenantMismatch
				}
				states = append(states, stateOf(existing))
				continue
			case errors.Is(err, ports.ErrPublicationNotFound):
				// Продолжаем создание.
			default:
				return err
			}

			material, err := domain.NewMaterial(target.Target, target.Title, target.Body)
			if err != nil {
				return err
			}
			publication, err := domain.NewPublication(domain.NewPublicationParams{
				ID:             target.PublicationID,
				TenantID:       cmd.TenantID,
				Digest:         cmd.Digest,
				Target:         target.Target,
				Material:       material,
				IdempotencyKey: target.IdempotencyKey,
				At:             at,
			})
			if err != nil {
				return err
			}
			if err := tx.Publications().Save(ctx, publication, 0); err != nil {
				return err
			}
			states = append(states, stateOf(publication))
		}
		result = RequestPublicationResult{Publications: states}
		return nil
	})
	if err != nil {
		return RequestPublicationResult{}, err
	}
	return result, nil
}

// verifySource проверяет наличие выжимки-источника. Неподключённый источник не
// блокирует команду (честно: контракт community-digest не собран), а вот
// отсутствие выжимки по ссылке — отказ.
func (h *RequestPublicationHandler) verifySource(ctx context.Context, ref domain.DigestRef) error {
	if h.digest == nil {
		return nil
	}
	if _, err := h.digest.Fetch(ctx, ref); err != nil {
		switch {
		case errors.Is(err, ports.ErrDigestNotFound):
			return fmt.Errorf("%w: %v", ErrSourceNotFound, err)
		case errors.Is(err, ports.ErrDigestUnavailable):
			return nil
		default:
			return err
		}
	}
	return nil
}
