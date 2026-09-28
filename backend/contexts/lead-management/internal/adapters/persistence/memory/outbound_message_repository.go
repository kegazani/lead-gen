package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"lead-gen/backend/contexts/lead-management/internal/domain"
	"lead-gen/backend/contexts/lead-management/internal/ports"
)

// OutboundMessageRepository — in-memory реализация ports.OutboundMessageRepository.
type OutboundMessageRepository struct {
	mu       sync.RWMutex
	messages map[domain.OutboundMessageID]domain.OutboundMessageSnapshot
}

// OutboundMessageRepository реализует порт репозитория исходящих писем.
var _ ports.OutboundMessageRepository = (*OutboundMessageRepository)(nil)

// NewOutboundMessageRepository создаёт пустой репозиторий.
func NewOutboundMessageRepository() *OutboundMessageRepository {
	return &OutboundMessageRepository{messages: make(map[domain.OutboundMessageID]domain.OutboundMessageSnapshot)}
}

// Save сохраняет сообщение целиком как снимок. Повторный вызов для того же
// сообщения заменяет прежний снимок (upsert по идентификатору).
func (r *OutboundMessageRepository) Save(_ context.Context, message *domain.OutboundMessage) error {
	if message == nil {
		return errors.New("memory: сохранение nil-сообщения")
	}
	snapshot := message.Snapshot()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.messages[snapshot.ID] = snapshot
	return nil
}

// FindByID загружает сообщение по идентификатору через
// domain.RehydrateOutboundMessage. Отсутствие даёт ports.ErrMessageNotFound.
func (r *OutboundMessageRepository) FindByID(_ context.Context, id domain.OutboundMessageID) (*domain.OutboundMessage, error) {
	r.mu.RLock()
	snapshot, ok := r.messages[id]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ports.ErrMessageNotFound, id)
	}
	message, err := domain.RehydrateOutboundMessage(snapshot)
	if err != nil {
		return nil, fmt.Errorf("memory: восстановление сообщения %s: %w", id, err)
	}
	return message, nil
}

// ListByLead возвращает историю сообщений лида, старые первыми.
func (r *OutboundMessageRepository) ListByLead(_ context.Context, leadID domain.CompanyID) ([]*domain.OutboundMessage, error) {
	r.mu.RLock()
	snapshots := make([]domain.OutboundMessageSnapshot, 0)
	for _, snapshot := range r.messages {
		if snapshot.LeadID != leadID {
			continue
		}
		snapshots = append(snapshots, snapshot)
	}
	r.mu.RUnlock()

	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].CreatedAt.Equal(snapshots[j].CreatedAt) {
			return snapshots[i].ID < snapshots[j].ID
		}
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})

	messages := make([]*domain.OutboundMessage, 0, len(snapshots))
	for _, snapshot := range snapshots {
		message, err := domain.RehydrateOutboundMessage(snapshot)
		if err != nil {
			return nil, fmt.Errorf("memory: восстановление сообщения %s: %w", snapshot.ID, err)
		}
		messages = append(messages, message)
	}
	return messages, nil
}

// SuppressedLeads возвращает идентификаторы лидов с хотя бы одним сообщением с
// подавляющим исходом, в порядке возрастания идентификатора.
func (r *OutboundMessageRepository) SuppressedLeads(_ context.Context) ([]domain.CompanyID, error) {
	r.mu.RLock()
	leads := make(map[domain.CompanyID]bool)
	for _, snapshot := range r.messages {
		if snapshot.Status.Suppresses() {
			leads[snapshot.LeadID] = true
		}
	}
	r.mu.RUnlock()

	ids := make([]domain.CompanyID, 0, len(leads))
	for leadID := range leads {
		ids = append(ids, leadID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}
