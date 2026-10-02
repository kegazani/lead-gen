package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/community-intelligence/internal/domain"
)

// ErrDigestNotFound — выжимка отсутствует в хранилище или принадлежит другому
// тенанту.
var ErrDigestNotFound = errors.New("порты: выжимка не найдена")

// DigestRepository — порт сохранения и загрузки aggregate root community-digest.
// Порт существует только для aggregate root и не знает о драйвере БД и схеме
// таблиц.
type DigestRepository interface {
	// Save сохраняет агрегат целиком. Повторный вызов для той же выжимки
	// обновляет ранее сохранённое состояние (upsert по идентификатору).
	Save(ctx context.Context, digest *domain.CommunityDigest) error

	// FindByID загружает выжимку по идентификатору. Если выжимки нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrDigestNotFound).
	FindByID(ctx context.Context, id domain.DigestID) (*domain.CommunityDigest, error)

	// ListByCommunity возвращает выжимки сообщества в порядке построения. Метод
	// обслуживает чтение экрана, а не доменное правило.
	ListByCommunity(ctx context.Context, id domain.CommunityID) ([]*domain.CommunityDigest, error)
}
