package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/message-delivery/internal/domain"
)

// ErrMessageNotFound — сообщения нет в хранилище.
var ErrMessageNotFound = errors.New("порты: сообщение не найдено")

// MessageRepository — хранилище агрегатов message.
//
// FindByIdempotencyKey служит барьером повторной отправки: сообщение ищется по
// неизменяемому ключу идемпотентности, поэтому повторная команда с тем же ключом
// не создаёт второе сообщение. Сохранение заменяет снимок целиком (upsert по
// идентификатору).
type MessageRepository interface {
	// Save сохраняет агрегат целиком как снимок.
	Save(ctx context.Context, message *domain.Message) error

	// FindByID загружает сообщение по идентификатору. Отсутствие даёт ошибку,
	// для которой истинно errors.Is(err, ErrMessageNotFound).
	FindByID(ctx context.Context, id domain.MessageID) (*domain.Message, error)

	// FindByIdempotencyKey загружает сообщение по ключу идемпотентности.
	// Отсутствие даёт ошибку, для которой истинно errors.Is(err, ErrMessageNotFound).
	FindByIdempotencyKey(ctx context.Context, key string) (*domain.Message, error)
}
