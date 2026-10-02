package ports

import (
	"context"
	"errors"

	"lead-gen/backend/contexts/publishing/internal/domain"
)

// Ошибки порта источника выжимки.
var (
	// ErrDigestUnavailable — источник выжимок не подключён (контракт
	// community-digest не собран). Это отказ чтения, а не отсутствие выжимки.
	ErrDigestUnavailable = errors.New("порты: источник выжимок недоступен")

	// ErrDigestNotFound — по ссылке выжимка не построена.
	ErrDigestNotFound = errors.New("порты: выжимка не найдена")
)

// DigestSource — порт чтения выжимки-источника. Контекст работает только с
// готовой выжимкой: сырых сообщений и данных авторов порт не отдаёт (design.md,
// Решение 3), поэтому перенести авторство в публикацию структурно невозможно.
type DigestSource interface {
	// Fetch возвращает выжимку по ссылке. Отсутствие выжимки — ErrDigestNotFound,
	// неподключённый источник — ErrDigestUnavailable.
	Fetch(ctx context.Context, ref domain.DigestRef) (domain.Digest, error)
}
