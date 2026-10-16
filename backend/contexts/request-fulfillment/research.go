package requestfulfillment

import (
	"context"
	"fmt"
	"time"

	"lead-gen/backend/contexts/request-fulfillment/internal/app/command"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/query"
	"lead-gen/backend/contexts/request-fulfillment/internal/app/researchbridge"
	"lead-gen/backend/contexts/request-fulfillment/internal/ports"
	"lead-gen/backend/platform/observability/observ"
)

// ResearchConfig подключает отдельный явно выбираемый исследовательский путь.
// Все порты обязательны; nil Config сохраняет прежний путь заявки.
type ResearchConfig = researchbridge.Config

func buildResearchBridge(cfg *ResearchConfig, requests *query.GetRequestHandler, approve *command.ValidateRequestHandler, legacy ports.WorkflowStatusReader) (*researchbridge.Service, error) {
	if cfg == nil {
		return nil, nil
	}
	return researchbridge.New(*cfg, requests, approve, legacy)
}

// ReconcileResearch обновляет статусы по уже сохранённым миссиям. Это явный
// worker-проход, чтение экранов его не вызывает.
func (s *Slice) ReconcileResearch(ctx context.Context) error {
	if s.researchReconciler == nil {
		return nil
	}
	return s.researchReconciler.Scan(ctx)
}
func (s *Service) runResearchReconciler(ctx context.Context) error {
	if s.slice == nil || s.slice.researchReconciler == nil || s.readinessResearchWorker == nil {
		return fmt.Errorf("worker: обработчик статусов исследования не собран")
	}
	s.readinessResearchWorker.healthy.Store(false)
	s.readinessResearchWorker.active.Store(true)
	defer s.readinessResearchWorker.active.Store(false)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		err := s.slice.ReconcileResearch(ctx)
		s.readinessResearchWorker.observe(err)
		if err != nil && ctx.Err() == nil {
			s.logger.Warn("Не удалось обновить часть статусов исследования; следующий проход повторит попытку", observ.Fields{}, "service", ServiceID)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
