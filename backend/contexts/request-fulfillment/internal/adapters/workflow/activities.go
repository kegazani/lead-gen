package workflowadapter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/domain"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// defaultActor — автор перехода, если сигнал не назвал человека (автоматический
// шаг workflow).
const defaultActor = domain.ActorID("workflow")

// SearchInput — вход заглушки шага поиска. Только идентификаторы.
type SearchInput struct {
	RequestID string
	TenantID  string
	Target    int
	Round     int
}

// SearchStub — заглушка шага поиска лидов. Реальный вызов lead-management придёт
// с change №4 (semantic-lead-search); до этого шаг детерминированно сообщает,
// сколько лидов «нашлось», не ходя в сеть. Заглушка не выдаёт себя за настоящий
// поиск: она названа явно и заменяется адаптером контракта.
type SearchStub interface {
	Search(ctx context.Context, in SearchInput) (int, error)
}

// FixedSearch — заглушка, возвращающая заданное число найденных лидов.
type FixedSearch struct{ Leads int }

// Search реализует SearchStub.
func (s FixedSearch) Search(context.Context, SearchInput) (int, error) { return s.Leads, nil }

// NoSearch — поиск не подключён: шаг честно сообщает ноль найденных лидов.
type NoSearch struct{}

// Search реализует SearchStub.
func (NoSearch) Search(context.Context, SearchInput) (int, error) { return 0, nil }

// Activities — тонкие адаптеры к application services контекста. Доменной
// логики здесь нет: допустимость перехода решает aggregate root request, а
// инварианты — прикладной слой. Повторный переход из терминального состояния
// вернёт доменную ошибку — activity не подменяет её успехом.
type Activities struct {
	StartProcessing *command.StartProcessingHandler
	Fulfill         *command.FulfillRequestHandler
	Cancel          *command.CancelRequestHandler
	Search          SearchStub
	Clock           ports.Clock
	IDs             ports.IDGenerator

	// Logger — логгер сервиса. nil — без логирования исходов activity.
	Logger observ.Logger
}

// HandleStartProcessing переводит заявку validated → processing.
func (a *Activities) HandleStartProcessing(ctx context.Context, in transitionInput) error {
	if a.StartProcessing == nil {
		return errors.New("activity: сценарий запуска исполнения не собран")
	}
	_, err := a.StartProcessing.Handle(ctx, command.StartProcessing{LifecycleFields: a.fields(in)})
	return a.wrap(ActivityStartProcessing, in, err)
}

// HandleFulfillRequest переводит заявку processing → fulfilled.
func (a *Activities) HandleFulfillRequest(ctx context.Context, in transitionInput) error {
	if a.Fulfill == nil {
		return errors.New("activity: сценарий завершения исполнения не собран")
	}
	_, err := a.Fulfill.Handle(ctx, command.FulfillRequest{LifecycleFields: a.fields(in)})
	return a.wrap(ActivityFulfillRequest, in, err)
}

// HandleCancelRequest переводит заявку из активного статуса в cancelled.
func (a *Activities) HandleCancelRequest(ctx context.Context, in transitionInput) error {
	if a.Cancel == nil {
		return errors.New("activity: сценарий отмены не собран")
	}
	_, err := a.Cancel.Handle(ctx, command.CancelRequest{
		LifecycleFields: a.fields(in),
		Reason:          reasonCode(in.ReasonCode, "утверждение: отказ"),
	})
	return a.wrap(ActivityCancelRequest, in, err)
}

// SearchLeads выполняет шаг поиска лидов (заглушка до change №4).
func (a *Activities) SearchLeads(ctx context.Context, in searchInput) (searchResult, error) {
	if a.Search == nil {
		return searchResult{}, errors.New("activity: шаг поиска не собран")
	}
	found, err := a.Search.Search(ctx, SearchInput{
		RequestID: in.RequestID,
		TenantID:  in.TenantID,
		Target:    in.Target,
		Round:     in.Round,
	})
	if err != nil {
		return searchResult{}, fmt.Errorf("activity: поиск лидов: %w", err)
	}
	return searchResult{Found: found}, nil
}

// fields собирает поля перехода из входа activity, подставляя часы контекста.
func (a *Activities) fields(in transitionInput) command.LifecycleFields {
	at := time.Now()
	if a.Clock != nil {
		at = a.Clock.Now()
	}
	actor := domain.ActorID(in.Actor)
	if actor == "" {
		actor = defaultActor
	}
	return command.LifecycleFields{
		RequestID:     domain.RequestID(in.RequestID),
		TenantID:      domain.TenantID(in.TenantID),
		Actor:         actor,
		At:            at,
		CorrelationID: domain.CorrelationID(in.CorrelationID),
		CausationID:   domain.CausationID(in.CausationID),
	}
}

// wrap добавляет контекст ошибки activity и логирует исход.
func (a *Activities) wrap(name string, in transitionInput, err error) error {
	if err != nil {
		return fmt.Errorf("activity %s: переход заявки %s: %w", name, in.RequestID, err)
	}
	if a.Logger != nil {
		a.Logger.Info("переход заявки выполнен", observ.Fields{
			TenantID:      in.TenantID,
			CorrelationID: in.CorrelationID,
			CausationID:   in.CausationID,
		}, "activity", name, "request_id", in.RequestID)
	}
	return nil
}

// reasonCode возвращает код причины, подставляя значение по умолчанию.
func reasonCode(code, fallback string) string {
	if code == "" {
		return fallback
	}
	return code
}
