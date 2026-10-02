package command

import (
	"context"
	"time"

	"lead-gen/backend/contexts/engagement-observation/internal/domain"
	"lead-gen/backend/contexts/engagement-observation/internal/ports"
)

// RecordTransition — команда учёта состоявшегося перехода по трекинг-ссылке.
// Это запись факта перехода (read-signal), а не метрика открытия: пикселей здесь
// нет.
type RecordTransition struct {
	// Ref — непрозрачная ссылка перехода.
	Ref string
	// Source — источник перехода (провайдер, редирект).
	Source string
	// OccurredAt — момент перехода. Нулевое — время часов сборки.
	OccurredAt time.Time
	// CorrelationID — сквозной сценарий.
	CorrelationID string
}

// RecordTransitionResult — итог учёта перехода: адрес, на который уходит
// посетитель, и идентификатор зафиксированного события.
type RecordTransitionResult struct {
	// Target — адрес назначения перехода.
	Target string
	// EngagementEventID — идентификатор зафиксированного события перехода.
	EngagementEventID string
}

// RecordTransitionHandler — use case read-tracking: разрешает трекинг-ссылку и
// фиксирует состоявшийся переход квалифицированным событием вида transition.
type RecordTransitionHandler struct {
	links  ports.TransitionLinks
	ingest *IngestEventHandler
	ids    ports.IDGenerator
}

// NewRecordTransitionHandler собирает use case из реестра ссылок, use case приёма
// и генератора идентификаторов.
func NewRecordTransitionHandler(links ports.TransitionLinks, ingest *IngestEventHandler, ids ports.IDGenerator) *RecordTransitionHandler {
	return &RecordTransitionHandler{links: links, ingest: ingest, ids: ids}
}

// Handle разрешает ссылку и фиксирует переход. Неизвестная ссылка даёт ошибку,
// для которой истинно errors.Is(err, ports.ErrLinkNotFound); посетитель тогда не
// переходит по несуществующему адресу.
func (h *RecordTransitionHandler) Handle(ctx context.Context, cmd RecordTransition) (RecordTransitionResult, error) {
	link, err := h.links.Resolve(ctx, domain.LinkRef(cmd.Ref))
	if err != nil {
		return RecordTransitionResult{}, err
	}

	// Каждый переход — отдельный факт: идентификатор сигнала уникален, поэтому
	// дедупликация не схлопывает разные посещения в одно.
	signalID := string(h.ids.NewEventID())
	result, err := h.ingest.Handle(ctx, IngestEvent{
		SignalID:      signalID,
		Source:        cmd.Source,
		TenantID:      string(link.TenantID),
		CompanyID:     string(link.CompanyID),
		Kind:          string(domain.KindTransition),
		LinkRef:       string(link.Ref),
		Target:        link.Target,
		OccurredAt:    cmd.OccurredAt,
		CorrelationID: cmd.CorrelationID,
	})
	if err != nil {
		return RecordTransitionResult{}, err
	}
	return RecordTransitionResult{
		Target:            link.Target,
		EngagementEventID: result.EngagementEventID,
	}, nil
}
