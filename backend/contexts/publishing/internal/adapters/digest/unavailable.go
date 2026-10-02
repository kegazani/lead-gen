package digest

import (
	"context"

	"lead-gen/backend/contexts/publishing/internal/domain"
	"lead-gen/backend/contexts/publishing/internal/ports"
)

// Unavailable — контракт community-digest не подключён: порт честно сообщает о
// недоступности источника, а не выдаёт отсутствие выжимки за её отсутствие.
type Unavailable struct{}

// Unavailable реализует порт источника выжимок.
var _ ports.DigestSource = Unavailable{}

// Fetch реализует ports.DigestSource.
func (Unavailable) Fetch(context.Context, domain.DigestRef) (domain.Digest, error) {
	return domain.Digest{}, ports.ErrDigestUnavailable
}
