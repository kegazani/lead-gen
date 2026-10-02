package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// IngestEvent — команда приёма сырого сигнала взаимодействия. Поля — сырые
// подписи источника, а не квалифицированные значения: квалификацию выполняет
// домен. Пиксельных сигналов здесь нет.
type IngestEvent struct {
	// SignalID — идентификатор сигнала у источника. Пусто — ключ дедупликации
	// выводится из содержимого сигнала.
	SignalID string
	// Source — источник сигнала (провайдер, письмо, событие доставки).
	Source string
	// TenantID — тенант-владелец данных.
	TenantID string
	// CompanyID — компания, к которой отнесён сигнал.
	CompanyID string
	// Kind — сырая подпись вида взаимодействия.
	Kind string
	// Outcome — сырая подпись исхода.
	Outcome string
	// LinkRef — непрозрачная ссылка перехода (для вида transition).
	LinkRef string
	// Target — адрес назначения перехода (для вида transition).
	Target string
	// OccurredAt — момент сигнала. Нулевое — берётся время часов сборки.
	OccurredAt time.Time
	// Confidence — уверенность в квалификации (подпись). Пусто — выводится из
	// источника.
	Confidence string
	// CorrelationID — сквозной сценарий.
	CorrelationID string
	// CausationID — сообщение-причина.
	CausationID string
}

// IngestEventResult — итог приёма сигнала.
type IngestEventResult struct {
	// EngagementEventID — идентификатор зафиксированного события. Пусто при
	// повторе сигнала.
	EngagementEventID string
	// Kind — квалифицированный вид взаимодействия.
	Kind string
	// Duplicate — сигнал уже был обработан; повтор не создал второго события.
	Duplicate bool
}

// IngestEventHandler — use case приёма и квалификации сигнала. Дедупликация,
// квалификация и запись события выполняются в одной транзакции.
type IngestEventHandler struct {
	uow   ports.UnitOfWork
	clock ports.Clock
	ids   ports.IDGenerator
}

// NewIngestEventHandler собирает use case из порта транзакции, часов и
// генератора идентификаторов.
func NewIngestEventHandler(uow ports.UnitOfWork, clock ports.Clock, ids ports.IDGenerator) *IngestEventHandler {
	return &IngestEventHandler{uow: uow, clock: clock, ids: ids}
}

// Handle принимает сырой сигнал, дедуплицирует его и квалифицирует в событие
// взаимодействия. Повтор сигнала не создаёт второго события и не пишет в outbox.
func (h *IngestEventHandler) Handle(ctx context.Context, cmd IngestEvent) (IngestEventResult, error) {
	signal := h.signal(cmd)
	key := signal.DedupKey()
	if key == "" {
		// Дедуплицировать нечем: сигнал без идентификатора и содержимого
		// не принимается, иначе повтор нельзя отличить от нового факта.
		return IngestEventResult{}, domain.ErrEmptyID
	}

	var result IngestEventResult
	err := h.uow.WithinTransaction(ctx, func(tx ports.Transaction) error {
		seen, err := tx.Signals().Seen(ctx, key)
		if err != nil {
			return err
		}
		if seen {
			result = IngestEventResult{Duplicate: true}
			return nil
		}

		event, err := domain.Qualify(domain.QualifyParams{
			ID:            domain.EngagementEventID(h.ids.NewEventID()),
			Signal:        signal,
			EventID:       h.ids.NewEventID(),
			Confidence:    domain.SignalConfidence(cmd.Confidence),
			CorrelationID: domain.CorrelationID(cmd.CorrelationID),
			CausationID:   domain.CausationID(cmd.CausationID),
		})
		if err != nil {
			return err
		}

		if err := tx.Engagements().Save(ctx, event, 0); err != nil {
			return err
		}
		if event.Kind() == domain.KindTransition {
			link, err := domain.NewTransitionLink(event.LinkRef(), event.Target(), event.TenantID(), event.CompanyID())
			if err != nil {
				return err
			}
			if err := tx.Links().Save(ctx, link); err != nil {
				return err
			}
		}
		if err := tx.Outbox().Append(ctx, event.DomainEvents()...); err != nil {
			return err
		}
		if err := tx.Signals().Mark(ctx, key); err != nil {
			return err
		}
		event.PullDomainEvents()

		result = IngestEventResult{
			EngagementEventID: string(event.ID()),
			Kind:              string(event.Kind()),
		}
		return nil
	})
	if err != nil {
		return IngestEventResult{}, err
	}
	return result, nil
}

// signal собирает сырой сигнал из команды, подставляя время часов, если момент
// не назван источником.
func (h *IngestEventHandler) signal(cmd IngestEvent) domain.RawSignal {
	at := cmd.OccurredAt
	if at.IsZero() && h.clock != nil {
		at = h.clock.Now()
	}
	return domain.RawSignal{
		SignalID:      domain.SignalID(cmd.SignalID),
		Source:        cmd.Source,
		TenantID:      domain.TenantID(cmd.TenantID),
		CompanyID:     domain.CompanyID(cmd.CompanyID),
		KindHint:      cmd.Kind,
		Outcome:       cmd.Outcome,
		LinkRef:       domain.LinkRef(cmd.LinkRef),
		Target:        cmd.Target,
		OccurredAt:    at,
		CorrelationID: domain.CorrelationID(cmd.CorrelationID),
		CausationID:   domain.CausationID(cmd.CausationID),
	}
}
