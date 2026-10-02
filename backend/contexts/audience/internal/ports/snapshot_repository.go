package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/audience/internal/domain"
)

// ErrSnapshotNotFound — снимок аудитории отсутствует в хранилище (в том числе
// принадлежит другому тенанту). Ошибка порта, а не домена.
var ErrSnapshotNotFound = errors.New("порты: снимок аудитории не найден")

// AudienceSnapshotRepository — порт сохранения и загрузки aggregate root
// audience-snapshot.
//
// Снимок неизменяем, поэтому у репозитория нет оптимистичной блокировки: Save —
// это вставка нового снимка (upsert по идентификатору), а не правка существующего.
type AudienceSnapshotRepository interface {
	// Save сохраняет зафиксированный снимок. Повторное сохранение с тем же
	// идентификатором заменяет запись.
	Save(ctx context.Context, snapshot *domain.AudienceSnapshot) error

	// FindByID загружает снимок по идентификатору. Если снимка нет, возвращается
	// ошибка, для которой истинно errors.Is(err, ErrSnapshotNotFound).
	FindByID(ctx context.Context, id domain.SnapshotID) (*domain.AudienceSnapshot, error)

	// ListBySegment возвращает снимки сегмента, упорядоченные по идентификатору.
	ListBySegment(ctx context.Context, segment domain.SegmentID) ([]*domain.AudienceSnapshot, error)
}
