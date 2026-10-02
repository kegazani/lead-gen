package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/data-governance/internal/domain"
	"lead-gen/backend/contexts/data-governance/internal/ports"
)

// SystemActor — актор фоновых операций. Истечение полномочия инициирует тикер,
// а не человек, поэтому актор назван явно и отличим в аудите от оператора.
const SystemActor domain.ActorID = "system:retention-sweep"

// ExpireAuthorities — команда фонового истечения полномочий, у которых закончилось
// окно действия.
type ExpireAuthorities struct {
	// At — момент проверки; пусто — текущее время часов.
	At time.Time
}

// ExpireAuthoritiesResult — результат прохода: сколько полномочий отозвано и
// какие события произведены.
type ExpireAuthoritiesResult struct {
	Expired int
	Events  []domain.DomainEvent
}

// ExpireAuthoritiesHandler — use case, обслуживающий роль worker.
type ExpireAuthoritiesHandler struct {
	repo  ports.ProcessingBasisRepository
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewExpireAuthoritiesHandler собирает use case.
func NewExpireAuthoritiesHandler(repo ports.ProcessingBasisRepository, clock ports.Clock, ids ports.IDGenerator) *ExpireAuthoritiesHandler {
	return &ExpireAuthoritiesHandler{repo: repo, clock: clock, ids: ids}
}

// Handle отзывает все действующие основания с истёкшим окном действия. Каждое
// основание сохраняется отдельно со своей ожидаемой версией; конфликт версии по
// одному основанию не отменяет обработку остальных.
func (h *ExpireAuthoritiesHandler) Handle(ctx context.Context, cmd ExpireAuthorities) (ExpireAuthoritiesResult, error) {
	at := cmd.At
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	bases, err := h.repo.ListExpired(ctx, at)
	if err != nil {
		return ExpireAuthoritiesResult{}, err
	}

	var result ExpireAuthoritiesResult
	for _, basis := range bases {
		expected := basis.Version()
		events, err := basis.Revoke(domain.RevokeParams{
			Actor:         SystemActor,
			Reason:        "истёк срок действия полномочия",
			At:            at,
			EventID:       h.ids.NewEventID(),
			CorrelationID: domain.CorrelationID("retention-sweep"),
			CausationID:   domain.CausationID(basis.ID()),
		})
		if err != nil {
			return result, err
		}
		if err := h.repo.Save(ctx, basis, expected); err != nil {
			return result, err
		}
		basis.PullDomainEvents()
		result.Expired++
		result.Events = append(result.Events, events...)
	}
	return result, nil
}
