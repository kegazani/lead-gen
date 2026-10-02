package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// ErrSenderReputationNotFound — репутации домена ещё нет в хранилище. Это не
// ошибка исполнения: домен без истории считается неограниченным.
var ErrSenderReputationNotFound = errors.New("порты: репутация отправляющего домена не найдена")

// SenderReputationRepository — хранилище агрегатов sender-reputation.
type SenderReputationRepository interface {
	// Save сохраняет агрегат целиком как снимок.
	Save(ctx context.Context, reputation *domain.SenderReputation) error

	// FindByDomain загружает репутацию по отправляющему домену. Отсутствие даёт
	// ошибку, для которой истинно errors.Is(err, ErrSenderReputationNotFound).
	FindByDomain(ctx context.Context, sender domain.SenderDomain) (*domain.SenderReputation, error)
}
