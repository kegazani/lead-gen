package ports

import (
	"context"

	"lead-gen/backend/contexts/lead-management/internal/domain"
)

// OutboundMessageRepository — репозиторий исходящих писем (зелёная зона).
type OutboundMessageRepository interface {
	// Save сохраняет сообщение (создаёт или дополняет).
	Save(ctx context.Context, message *domain.OutboundMessage) error
	// FindByID возвращает сообщение по идентификатору.
	FindByID(ctx context.Context, id domain.OutboundMessageID) (*domain.OutboundMessage, error)
	// ListByLead возвращает историю сообщений лида, старые первыми.
	ListByLead(ctx context.Context, leadID domain.CompanyID) ([]*domain.OutboundMessage, error)
	// SuppressedLeads возвращает идентификаторы лидов, у которых есть хотя бы одно
	// сообщение с подавляющим исходом (bounced/unsubscribed/rejected).
	SuppressedLeads(ctx context.Context) ([]domain.CompanyID, error)
}
