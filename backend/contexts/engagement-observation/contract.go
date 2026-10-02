package engagementobservation

import (
	"context"
	"errors"
	"fmt"

	"lead-gen/backend/contexts/engagement-observation/internal/app/command"
	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
	"lead-gen/contracts/engagement-observation/engagementevent"
	"lead-gen/contracts/engagement-observation/readsignal"
)

// Публичные фабрики контрактов контекста. Точка сборки (cmd/lead-gen) не имеет
// доступа к внутренним пакетам контекста, поэтому адаптеры контрактов объявлены
// здесь: при co-location вызов идёт в процессе, при разделении сервисов та же
// форма даётся сетевым клиентом.

// NewEngagementEventPublisher собирает опубликованный контракт engagement-event
// поверх собранного среза: квалифицированное событие принимается к публикации и
// попадает в transactional outbox.
func NewEngagementEventPublisher(slice *Slice) engagementevent.Publisher {
	if slice == nil {
		return engagementEventPublisher{}
	}
	return engagementEventPublisher{slice: slice}
}

// engagementEventPublisher — реализация engagementevent.Publisher владельцем.
type engagementEventPublisher struct {
	slice *Slice
}

// Publish проверяет контракт и переводит его в доменное событие engagement-observed,
// которое записывается в outbox в транзакции. Невалидный контракт не публикуется.
func (p engagementEventPublisher) Publish(ctx context.Context, event engagementevent.EngagementEvent) error {
	if p.slice == nil {
		return errors.New("engagement-observation: издатель не собран")
	}
	if err := event.Validate(); err != nil {
		return err
	}
	observed, err := toDomainObserved(event)
	if err != nil {
		return err
	}
	return p.slice.UnitOfWork.WithinTransaction(ctx, func(tx ports.Transaction) error {
		return tx.Outbox().Append(ctx, observed)
	})
}

// toDomainObserved переводит контрактную форму в доменное событие, проверяя вид,
// исход и уверенность теми же правилами домена, что и приём сигнала. Пиксельный
// вид здесь отвергается повторно: контракт не становится лазейкой.
func toDomainObserved(event engagementevent.EngagementEvent) (domain.EngagementObserved, error) {
	kind, err := domain.ParseEngagementKind(event.Kind)
	if err != nil {
		return domain.EngagementObserved{}, err
	}
	outcome, err := domain.ParseOutcome(kind, event.Outcome)
	if err != nil {
		return domain.EngagementObserved{}, err
	}
	confidence, err := domain.ParseSignalConfidence(event.Confidence)
	if err != nil {
		return domain.EngagementObserved{}, err
	}
	if kind == domain.KindTransition {
		if _, err := domain.NewTransitionLink(domain.LinkRef(event.LinkRef), event.Target, domain.TenantID(event.TenantID), domain.CompanyID(event.CompanyID)); err != nil {
			return domain.EngagementObserved{}, err
		}
	}
	return domain.EngagementObserved{
		EventHeader: domain.EventHeader{
			EventID:       domain.EventID(event.EventID),
			OccurredAt:    event.OccurredAt,
			TenantID:      domain.TenantID(event.TenantID),
			CorrelationID: domain.CorrelationID(event.CorrelationID),
			CausationID:   domain.CausationID(event.CausationID),
		},
		EngagementEventID: domain.EngagementEventID(event.EngagementEventID),
		CompanyID:         domain.CompanyID(event.CompanyID),
		Kind:              kind,
		Confidence:        confidence,
		Outcome:           outcome,
		LinkRef:           domain.LinkRef(event.LinkRef),
		Target:            event.Target,
	}, nil
}

// NewReadSignalResolver собирает операцию Resolver контракта read-signal поверх
// реестра трекинг-ссылок среза. Неизвестная ссылка отдаёт readsignal.ErrNotFound.
func NewReadSignalResolver(slice *Slice) readsignal.Resolver {
	return readSignalResolver{slice: slice}
}

type readSignalResolver struct {
	slice *Slice
}

// Resolve реализует readsignal.Resolver.
func (r readSignalResolver) Resolve(ctx context.Context, ref readsignal.LinkRef) (string, error) {
	if r.slice == nil {
		return "", readsignal.ErrNotFound
	}
	link, err := r.slice.Links.Resolve(ctx, domain.LinkRef(ref))
	if err != nil {
		if errors.Is(err, ports.ErrLinkNotFound) {
			return "", fmt.Errorf("%w: %v", readsignal.ErrNotFound, err)
		}
		return "", err
	}
	return link.Target, nil
}

// NewReadSignalRecorder собирает операцию Recorder контракта read-signal поверх
// сценария учёта перехода. Неизвестная ссылка отдаёт readsignal.ErrNotFound.
func NewReadSignalRecorder(slice *Slice) readsignal.Recorder {
	return readSignalRecorder{slice: slice}
}

type readSignalRecorder struct {
	slice *Slice
}

// Record реализует readsignal.Recorder: фиксирует состоявшийся переход
// квалифицированным событием вида transition.
func (r readSignalRecorder) Record(ctx context.Context, transition readsignal.Transition) error {
	if r.slice == nil {
		return readsignal.ErrNotFound
	}
	_, err := r.slice.RecordTransition.Handle(ctx, command.RecordTransition{
		Ref:        string(transition.Link),
		Source:     "read-signal",
		OccurredAt: transition.OccurredAt,
	})
	if err != nil {
		if errors.Is(err, ports.ErrLinkNotFound) {
			return fmt.Errorf("%w: %v", readsignal.ErrNotFound, err)
		}
		return err
	}
	return nil
}

// Проверки соответствия контрактам на этапе компиляции.
var (
	_ engagementevent.Publisher = engagementEventPublisher{}
	_ readsignal.Resolver       = readSignalResolver{}
	_ readsignal.Recorder       = readSignalRecorder{}
)
